package pmtiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// Source reads byte ranges of an archive. A read past the end returns what
// there is.
type Source interface {
	ReadRange(ctx context.Context, offset, length uint64) ([]byte, error)
}

// Request names the part of an archive an extract keeps.
type Request struct {
	Bounds  Bounds
	MinZoom uint8
	MaxZoom uint8
}

// ErrNoOverlap is returned when the request's box or zooms share nothing with
// the source archive.
var ErrNoOverlap = errors.New("pmtiles: the requested area or zooms are not in the source archive")

// Tuning for how reads are grouped. A directory or a tile whose bytes sit
// within mergeGap of the previous one's is read in the same request, up to
// maxRead bytes per request: one request for many small neighbors costs less
// than the bytes between them.
const (
	mergeGap = 256 << 10
	maxRead  = 16 << 20
)

// Plan is an extract whose directories are built and whose size is known, and
// whose tile data has not yet been copied. Planning reads the source's header,
// metadata and the directories that address the requested tiles; writing reads
// the tiles.
type Plan struct {
	header   Header
	root     []byte
	metadata []byte
	leaves   []byte
	copies   []span
	srcData  uint64
	addrs    uint64
	contents uint64
}

// span is one tile content to copy: where it starts in the source's tile data
// section, and its length.
type span struct {
	offset uint64
	length uint64
}

// Header returns the header the extract will be written with.
func (p *Plan) Header() Header { return p.header }

// Metadata returns the source archive's metadata, decoded: the JSON object
// the format stores, which for a Protomaps build carries its attribution,
// schema version and OpenStreetMap replication time.
func (p *Plan) Metadata() (map[string]any, error) {
	return DecodeMetadata(p.metadata, p.header.InternalCompression)
}

// DecodeMetadata decodes an archive's metadata section.
func DecodeMetadata(raw []byte, c Compression) (map[string]any, error) {
	b, err := decompress(raw, c)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(b) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("pmtiles: decoding metadata: %w", err)
	}
	return out, nil
}

// Size returns the extract's size in bytes.
func (p *Plan) Size() uint64 {
	return p.header.TileDataOffset + p.header.TileDataLength
}

// NewPlan reads what an extract of src needs to know before its tiles are
// copied.
func NewPlan(ctx context.Context, src Source, req Request) (*Plan, error) {
	head, err := src.ReadRange(ctx, 0, RootFetchLen)
	if err != nil {
		return nil, fmt.Errorf("pmtiles: reading the header: %w", err)
	}
	h, err := ParseHeader(head)
	if err != nil {
		return nil, err
	}
	sc, err := clampRequest(h, req)
	if err != nil {
		return nil, err
	}
	want := Cover(sc.bounds, sc.minZ, sc.maxZ)

	rootRaw, err := readSection(ctx, src, head, h.RootOffset, h.RootLength)
	if err != nil {
		return nil, fmt.Errorf("pmtiles: reading the root directory: %w", err)
	}
	rootEntries, err := ParseDirectory(rootRaw, h.InternalCompression)
	if err != nil {
		return nil, err
	}
	entries, err := walk(ctx, src, h, rootEntries, want)
	if err != nil {
		return nil, err
	}
	metadata, err := readSection(ctx, src, head, h.MetadataOffset, h.MetadataLength)
	if err != nil {
		return nil, fmt.Errorf("pmtiles: reading the metadata: %w", err)
	}
	return buildPlan(h, entries, metadata, sc)
}

// scope is what an extract keeps once the request is clamped to the source:
// the box, and the zooms.
type scope struct {
	bounds Bounds
	minZ   uint8
	maxZ   uint8
}

func clampRequest(h Header, req Request) (scope, error) {
	if err := req.Bounds.Validate(); err != nil {
		return scope{}, err
	}
	bounds, ok := req.Bounds.Intersect(h.Bounds())
	minZ, maxZ := max(req.MinZoom, h.MinZoom), min(req.MaxZoom, h.MaxZoom, MaxZoom)
	if !ok || minZ > maxZ {
		return scope{}, ErrNoOverlap
	}
	return scope{bounds: bounds, minZ: minZ, maxZ: maxZ}, nil
}

// readSection returns a section of the archive, from the bytes already read
// when it lies inside them.
func readSection(ctx context.Context, src Source, have []byte, offset, length uint64) ([]byte, error) {
	if offset+length <= uint64(len(have)) {
		return have[offset : offset+length], nil
	}
	b, err := src.ReadRange(ctx, offset, length)
	if err != nil {
		return nil, fmt.Errorf("pmtiles: reading %d bytes at %d: %w", length, offset, err)
	}
	if uint64(len(b)) != length {
		return nil, fmt.Errorf("pmtiles: short read at %d: %d of %d bytes", offset, len(b), length)
	}
	return b, nil
}

// leafRef is a leaf directory that addresses some requested tile.
type leafRef struct {
	offset uint64
	length uint64
	end    uint64 // the first tile id past the leaf's span
}

// walk visits the directories that address requested tiles, depth by depth,
// and returns the tile entries clipped to the request, in tile id order. A
// leaf is visited as it is read and then dropped, so what is held is the
// clipped entries, not the directories they came from.
func walk(ctx context.Context, src Source, h Header, root []Entry, want []Range) ([]Entry, error) {
	w := walker{want: want, leafBase: h.LeafOffset}
	w.visit(root, math.MaxUint64)
	for len(w.leaves) > 0 {
		leaves := w.leaves
		w.leaves = nil
		err := readLeaves(ctx, src, h.InternalCompression, leaves, w.visit)
		if err != nil {
			return nil, err
		}
	}
	out := w.out
	sort.Slice(out, func(i, j int) bool { return out[i].TileID < out[j].TileID })
	return coalesce(out), nil
}

// walker collects what the requested tiles' directories address: the tile
// entries clipped to the request, and the leaves still to read.
type walker struct {
	want     []Range
	leafBase uint64
	out      []Entry
	leaves   []leafRef
}

// visit keeps one directory's entries that address requested tiles: a tile
// entry clipped to the request, and a leaf pointer to be read. end is the
// first tile id past the directory's span.
func (w *walker) visit(dir []Entry, end uint64) {
	for i, e := range dir {
		next := end
		if i+1 < len(dir) {
			next = dir[i+1].TileID
		}
		if e.RunLength == 0 {
			if len(clip(w.want, e.TileID, next)) > 0 {
				w.leaves = append(w.leaves, leafRef{offset: w.leafBase + e.Offset, length: uint64(e.Length), end: next})
			}
			continue
		}
		for _, r := range clip(w.want, e.TileID, e.TileID+uint64(e.RunLength)) {
			w.out = append(w.out, Entry{
				TileID: r.Start, Offset: e.Offset, Length: e.Length, RunLength: uint32(r.End - r.Start), // #nosec G115 -- within the source run's u32
			})
		}
	}
}

// readLeaves reads and parses leaf directories, grouping neighbors into one
// request, and hands each to fn with the first tile id past its span.
func readLeaves(ctx context.Context, src Source, c Compression, leaves []leafRef, fn func([]Entry, uint64)) error {
	sort.Slice(leaves, func(a, b int) bool { return leaves[a].offset < leaves[b].offset })
	spans := make([]span, len(leaves))
	for i, l := range leaves {
		spans[i] = span{offset: l.offset, length: l.length}
	}
	i := 0
	for _, g := range groupSpans(spans) {
		buf, err := src.ReadRange(ctx, g.offset, g.length)
		if err != nil {
			return fmt.Errorf("pmtiles: reading leaf directories: %w", err)
		}
		for ; i < len(leaves) && leaves[i].offset < g.offset+g.length; i++ {
			l := leaves[i]
			rel := l.offset - g.offset
			if rel+l.length > uint64(len(buf)) {
				return fmt.Errorf("pmtiles: short read of a leaf directory at %d", l.offset)
			}
			d, err := ParseDirectory(buf[rel:rel+l.length], c)
			if err != nil {
				return err
			}
			fn(d, l.end)
		}
	}
	return nil
}

// groupSpans merges spans, sorted by offset, into reads: a span joins the
// read before it when it starts within mergeGap of that read's end and the
// read stays under maxRead.
func groupSpans(spans []span) []span {
	var out []span
	for _, s := range spans {
		if n := len(out); n > 0 {
			g := &out[n-1]
			end := g.offset + g.length
			if s.offset >= g.offset && s.offset <= end+mergeGap && s.offset+s.length-g.offset <= maxRead {
				g.length = max(end, s.offset+s.length) - g.offset
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// coalesce joins neighboring entries that address consecutive tiles holding
// the same content, which clipping a run can leave split.
func coalesce(entries []Entry) []Entry {
	if len(entries) == 0 {
		return entries
	}
	out := entries[:1]
	for _, e := range entries[1:] {
		last := &out[len(out)-1]
		if e.TileID == last.TileID+uint64(last.RunLength) && e.Offset == last.Offset &&
			e.Length == last.Length && uint64(last.RunLength)+uint64(e.RunLength) <= math.MaxUint32 {
			last.RunLength += e.RunLength
			continue
		}
		out = append(out, e)
	}
	return out
}

// repeated returns the source offsets of the contents more than one entry
// names, or that appear out of the source's order. A clustered source stores
// each content in the order its first tile appears, so an entry naming an
// offset at or below the highest one seen so far is naming a content again.
// Only these need remembering when the extract is laid out; every other
// content is met once.
func repeated(entries []Entry) map[uint64]bool {
	out := map[uint64]bool{}
	var highest uint64
	for i, e := range entries {
		if i > 0 && e.Offset <= highest {
			out[e.Offset] = true
			continue
		}
		highest = e.Offset
	}
	return out
}

func buildPlan(h Header, entries []Entry, metadata []byte, sc scope) (*Plan, error) {
	p := &Plan{metadata: metadata, srcData: h.TileDataOffset}
	again := repeated(entries)
	placed := make(map[uint64]uint64, len(again))
	var dataLen uint64
	for i := range entries {
		e := &entries[i]
		p.addrs += uint64(e.RunLength)
		if dst, ok := placed[e.Offset]; ok {
			e.Offset = dst
			continue
		}
		if again[e.Offset] {
			placed[e.Offset] = dataLen
		}
		p.copies = append(p.copies, span{offset: e.Offset, length: uint64(e.Length)})
		e.Offset = dataLen
		dataLen += uint64(e.Length)
	}
	p.contents = uint64(len(p.copies))
	root, leaves, err := buildDirectories(entries, h.InternalCompression)
	if err != nil {
		return nil, err
	}
	p.root, p.leaves = root, leaves
	p.header = outputHeader(h, p, uint64(len(entries)), dataLen, sc)
	return p, nil
}

func outputHeader(src Header, p *Plan, entries, dataLen uint64, sc scope) Header {
	b, minZ, maxZ := sc.bounds, sc.minZ, sc.maxZ
	rootOff := uint64(HeaderLen)
	metaOff := rootOff + uint64(len(p.root))
	leafOff := metaOff + uint64(len(p.metadata))
	dataOff := leafOff + uint64(len(p.leaves))
	toE7 := func(v float64) int32 { return int32(math.Round(v * e7)) }
	return Header{
		RootOffset: rootOff, RootLength: uint64(len(p.root)),
		MetadataOffset: metaOff, MetadataLength: uint64(len(p.metadata)),
		LeafOffset: leafOff, LeafLength: uint64(len(p.leaves)),
		TileDataOffset: dataOff, TileDataLength: dataLen,
		AddressedTiles: p.addrs, TileEntries: entries, TileContents: p.contents,
		Clustered:           true,
		InternalCompression: src.InternalCompression,
		TileCompression:     src.TileCompression,
		TileType:            src.TileType,
		MinZoom:             minZ, MaxZoom: maxZ,
		MinLonE7: toE7(b.MinLon), MinLatE7: toE7(b.MinLat), MaxLonE7: toE7(b.MaxLon), MaxLatE7: toE7(b.MaxLat),
		CenterZoom:  minZ,
		CenterLonE7: toE7((b.MinLon + b.MaxLon) / 2), CenterLatE7: toE7((b.MinLat + b.MaxLat) / 2),
	}
}

// fetchDepth is how many tile reads are in flight ahead of the writer.
const fetchDepth = 4

// WriteTo writes the extract to w: the header, directories and metadata, then
// every tile content copied from src in the extract's order. progress, when
// set, is called with the bytes written so far after each read is written.
func (p *Plan) WriteTo(ctx context.Context, src Source, w io.Writer, progress func(written uint64)) error {
	var written uint64
	for _, part := range [][]byte{p.header.Bytes(), p.root, p.metadata, p.leaves} {
		if _, err := w.Write(part); err != nil {
			return fmt.Errorf("pmtiles: writing the extract: %w", err)
		}
		written += uint64(len(part))
	}
	reads := p.reads()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan chan fetched, fetchDepth)
	go prefetch(ctx, src, reads, results)
	for r := range results {
		f := <-r
		if f.err != nil {
			return f.err
		}
		if err := writePieces(w, f); err != nil {
			return err
		}
		written += f.size
		if progress != nil {
			progress(written)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pmtiles: writing the extract: %w", err)
	}
	return nil
}

// tileRead is one request for tile data and the contents it holds, in order.
type tileRead struct {
	span
	pieces []span // offsets relative to the read
}

type fetched struct {
	read tileRead
	buf  []byte
	size uint64
	err  error
}

// reads groups the contents to copy into requests. Contents are copied in the
// extract's order, so only a content that follows the one before it in the
// source joins that one's read.
func (p *Plan) reads() []tileRead {
	var out []tileRead
	for _, c := range p.copies {
		abs := span{offset: p.srcData + c.offset, length: c.length}
		if n := len(out); n > 0 {
			r := &out[n-1]
			end := r.offset + r.length
			if abs.offset >= end && abs.offset <= end+mergeGap && abs.offset+abs.length-r.offset <= maxRead {
				r.pieces = append(r.pieces, span{offset: abs.offset - r.offset, length: abs.length})
				r.length = abs.offset + abs.length - r.offset
				continue
			}
		}
		out = append(out, tileRead{span: abs, pieces: []span{{offset: 0, length: abs.length}}})
	}
	return out
}

// prefetch issues reads ahead of the writer, at most fetchDepth at once, and
// hands them over in order.
func prefetch(ctx context.Context, src Source, reads []tileRead, results chan<- chan fetched) {
	defer close(results)
	for _, r := range reads {
		ch := make(chan fetched, 1)
		select {
		case results <- ch:
		case <-ctx.Done():
			return
		}
		go func(r tileRead) {
			buf, err := src.ReadRange(ctx, r.offset, r.length)
			if err == nil && uint64(len(buf)) != r.length {
				err = fmt.Errorf("pmtiles: short read of tile data at %d: %d of %d bytes", r.offset, len(buf), r.length)
			}
			if err != nil {
				err = fmt.Errorf("pmtiles: reading tile data: %w", err)
			}
			var size uint64
			for _, pc := range r.pieces {
				size += pc.length
			}
			ch <- fetched{read: r, buf: buf, size: size, err: err}
		}(r)
	}
}

func writePieces(w io.Writer, f fetched) error {
	for _, pc := range f.read.pieces {
		if _, err := w.Write(f.buf[pc.offset : pc.offset+pc.length]); err != nil {
			return fmt.Errorf("pmtiles: writing the extract: %w", err)
		}
	}
	return nil
}
