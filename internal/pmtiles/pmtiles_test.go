package pmtiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memSource is an archive held in memory, counting its reads.
type memSource struct {
	data  []byte
	reads atomic.Int64
	fail  error
}

func (m *memSource) ReadRange(_ context.Context, offset, length uint64) ([]byte, error) {
	m.reads.Add(1)
	if m.fail != nil {
		return nil, m.fail
	}
	if offset >= uint64(len(m.data)) {
		return nil, nil
	}
	return m.data[offset:min(offset+length, uint64(len(m.data)))], nil
}

// tileAt is the content a synthetic archive holds for a tile: the same bytes
// for every tile of the sea (x even at zoom 6 and deeper, a shared content and
// long runs), and bytes naming the tile everywhere else.
func tileAt(z uint8, x, y uint32) []byte {
	if z >= 6 && x%2 == 0 {
		return []byte("sea")
	}
	return fmt.Appendf(nil, "tile %d/%d/%d", z, x, y)
}

// buildArchive writes a clustered archive holding every tile from zoom 0 to
// maxZ, the way a writer that dedupes contents and joins runs would.
func buildArchive(t *testing.T, maxZ uint8, c Compression) []byte {
	t.Helper()
	type tile struct {
		id   uint64
		data []byte
	}
	var tiles []tile
	for z := uint8(0); z <= maxZ; z++ {
		for x := uint32(0); x < 1<<z; x++ {
			for y := uint32(0); y < 1<<z; y++ {
				tiles = append(tiles, tile{ZxyToID(z, x, y), tileAt(z, x, y)})
			}
		}
	}
	sort.Slice(tiles, func(i, j int) bool { return tiles[i].id < tiles[j].id })
	var data bytes.Buffer
	seen := map[string]uint64{}
	var entries []Entry
	for _, tl := range tiles {
		off, ok := seen[string(tl.data)]
		if !ok {
			off = uint64(data.Len()) //nolint:gosec // a buffer length is never negative
			seen[string(tl.data)] = off
			_, _ = data.Write(tl.data)
		}
		if n := len(entries); n > 0 {
			last := &entries[n-1]
			if last.Offset == off && last.TileID+uint64(last.RunLength) == tl.id {
				last.RunLength++
				continue
			}
		}
		entries = append(entries, Entry{TileID: tl.id, Offset: off, Length: uint32(len(tl.data)), RunLength: 1}) //nolint:gosec // a tile here is a few bytes
	}
	root, leaves, err := buildDirectories(entries, c)
	require.NoError(t, err)
	meta, err := (&compressor{c: c}).compress([]byte(`{"name":"synthetic"}`))
	require.NoError(t, err)
	h := Header{
		RootOffset: HeaderLen, RootLength: uint64(len(root)),
		MetadataOffset: HeaderLen + uint64(len(root)), MetadataLength: uint64(len(meta)),
		TileEntries: uint64(len(entries)), TileContents: uint64(len(seen)), AddressedTiles: uint64(len(tiles)),
		Clustered: true, InternalCompression: c, TileCompression: CompressionNone, TileType: TileTypeMVT,
		MinZoom: 0, MaxZoom: maxZ, MinLonE7: -1800000000, MinLatE7: -850511287, MaxLonE7: 1800000000, MaxLatE7: 850511287,
	}
	h.LeafOffset = h.MetadataOffset + h.MetadataLength
	h.LeafLength = uint64(len(leaves))
	h.TileDataOffset = h.LeafOffset + h.LeafLength
	h.TileDataLength = uint64(data.Len()) //nolint:gosec // a buffer length is never negative
	var out bytes.Buffer
	for _, part := range [][]byte{h.Bytes(), root, meta, leaves, data.Bytes()} {
		_, _ = out.Write(part)
	}
	return out.Bytes()
}

// readAll returns every tile an archive addresses, by tile id.
func readAll(t *testing.T, archive []byte) (h Header, tiles map[uint64]string) {
	t.Helper()
	h, err := ParseHeader(archive)
	require.NoError(t, err)
	tiles = map[uint64]string{}
	var walkDir func(off, length uint64)
	walkDir = func(off, length uint64) {
		dir, err := ParseDirectory(archive[off:off+length], h.InternalCompression)
		require.NoError(t, err)
		for _, e := range dir {
			if e.RunLength == 0 {
				walkDir(h.LeafOffset+e.Offset, uint64(e.Length))
				continue
			}
			start := h.TileDataOffset + e.Offset
			for i := uint64(0); i < uint64(e.RunLength); i++ {
				tiles[e.TileID+i] = string(archive[start : start+uint64(e.Length)])
			}
		}
	}
	walkDir(h.RootOffset, h.RootLength)
	return h, tiles
}

// wantTiles is every tile of the synthetic archive inside a box, computed the
// direct way: by tile coordinates rather than by ranges of ids.
func wantTiles(b Bounds, minZ, maxZ uint8) map[uint64]string {
	out := map[uint64]string{}
	for z := minZ; z <= maxZ; z++ {
		r := rectAt(b, z)
		for x := r.x0; x <= r.x1; x++ {
			for y := r.y0; y <= r.y1; y++ {
				out[ZxyToID(z, x, y)] = string(tileAt(z, x, y))
			}
		}
	}
	return out
}

func TestZxyToIDMatchesTheReferenceValues(t *testing.T) {
	assert.Equal(t, uint64(0), ZxyToID(0, 0, 0))
	assert.Equal(t, uint64(1), ZxyToID(1, 0, 0))
	assert.Equal(t, uint64(2), ZxyToID(1, 0, 1))
	assert.Equal(t, uint64(3), ZxyToID(1, 1, 1))
	assert.Equal(t, uint64(4), ZxyToID(1, 1, 0))
	assert.Equal(t, uint64(5), ZxyToID(2, 0, 0))
	assert.Equal(t, uint64(19078479), ZxyToID(12, 3423, 1763))
}

func TestCoverIsExactlyTheTilesInTheBox(t *testing.T) {
	boxes := []Bounds{
		{MinLon: -122.52, MinLat: 37.70, MaxLon: -122.35, MaxLat: 37.83},
		{MinLon: -125, MinLat: 24, MaxLon: -66, MaxLat: 50},
		{MinLon: -180, MinLat: -90, MaxLon: 180, MaxLat: 90},
		{MinLon: 2.2, MinLat: 48.8, MaxLon: 2.5, MaxLat: 48.95},
	}
	for _, b := range boxes {
		t.Run(fmt.Sprint(b), func(t *testing.T) {
			got := map[uint64]bool{}
			for _, r := range Cover(b, 0, 9) {
				for id := r.Start; id < r.End; id++ {
					got[id] = true
				}
			}
			want := wantTiles(b, 0, 9)
			assert.Len(t, got, len(want))
			for id := range want {
				assert.True(t, got[id], "tile id %d missing from the cover", id)
			}
		})
	}
}

func TestCoverRangesAreSortedAndDisjoint(t *testing.T) {
	rs := Cover(Bounds{MinLon: -125, MinLat: 24, MaxLon: -66, MaxLat: 50}, 0, 12)
	for i := 1; i < len(rs); i++ {
		assert.Less(t, rs[i-1].End, rs[i].Start)
	}
}

func TestDirectoryRoundTrip(t *testing.T) {
	entries := []Entry{
		{TileID: 0, Offset: 0, Length: 10, RunLength: 1},
		{TileID: 1, Offset: 10, Length: 5, RunLength: 3},
		{TileID: 9, Offset: 0, Length: 10, RunLength: 1},
		{TileID: 40, Offset: 900, Length: 0, RunLength: 0},
	}
	for _, c := range []Compression{CompressionNone, CompressionGzip} {
		b, err := SerializeDirectory(entries, c)
		require.NoError(t, err)
		got, err := ParseDirectory(b, c)
		require.NoError(t, err)
		assert.Equal(t, entries, got)
	}
}

func TestParseDirectoryRefusesCorruptBytes(t *testing.T) {
	_, err := ParseDirectory([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, CompressionNone)
	require.Error(t, err)
	_, err = ParseDirectory([]byte{3, 1}, CompressionNone)
	require.Error(t, err)
	_, err = ParseDirectory([]byte{1, 2, 3}, CompressionGzip)
	require.Error(t, err)
	_, err = ParseDirectory([]byte{0}, CompressionBrotli)
	require.Error(t, err)
}

func TestHeaderRoundTrip(t *testing.T) {
	h := Header{
		RootOffset: 127, RootLength: 10, MetadataOffset: 137, MetadataLength: 4, LeafOffset: 141,
		TileDataOffset: 141, TileDataLength: 99, AddressedTiles: 7, TileEntries: 5, TileContents: 4,
		Clustered: true, InternalCompression: CompressionGzip, TileCompression: CompressionGzip,
		TileType: TileTypeMVT, MinZoom: 2, MaxZoom: 9, MinLonE7: -1225200000, MinLatE7: 377000000,
		MaxLonE7: -1223500000, MaxLatE7: 378300000, CenterZoom: 2, CenterLonE7: -1224350000, CenterLatE7: 377650000,
	}
	got, err := ParseHeader(h.Bytes())
	require.NoError(t, err)
	assert.Equal(t, h, got)
	assert.InDelta(t, -122.52, got.Bounds().MinLon, 1e-9)

	_, err = ParseHeader([]byte("not an archive"))
	require.ErrorIs(t, err, ErrNotPMTiles)
	v2 := h.Bytes()
	v2[7] = 2
	_, err = ParseHeader(v2)
	require.ErrorIs(t, err, ErrNotPMTiles)
}

func TestExtractKeepsExactlyTheRequestedTiles(t *testing.T) {
	for _, c := range []Compression{CompressionNone, CompressionGzip} {
		t.Run(fmt.Sprint("compression ", c), func(t *testing.T) {
			src := &memSource{data: buildArchive(t, 9, c)}
			srcHeader, err := ParseHeader(src.data)
			require.NoError(t, err)
			require.NotZero(t, srcHeader.LeafLength, "the source must need leaf directories for this test to walk them")

			req := Request{Bounds: Bounds{MinLon: -125, MinLat: 24, MaxLon: -66, MaxLat: 50}, MinZoom: 0, MaxZoom: 8}
			plan, err := NewPlan(context.Background(), src, req)
			require.NoError(t, err)

			var out bytes.Buffer
			var last uint64
			require.NoError(t, plan.WriteTo(context.Background(), src, &out, func(n uint64) { last = n }))
			assert.Equal(t, plan.Size(), uint64(out.Len()), "the planned size is the written size") //nolint:gosec // a buffer length is never negative
			assert.Equal(t, plan.Size(), last, "the last progress report is the whole extract")

			h, got := readAll(t, out.Bytes())
			want := wantTiles(req.Bounds, 0, 8)
			assert.Equal(t, want, got)
			assert.Equal(t, uint8(0), h.MinZoom)
			assert.Equal(t, uint8(8), h.MaxZoom)
			assert.Equal(t, int32(-1250000000), h.MinLonE7)
			assert.Equal(t, int32(500000000), h.MaxLatE7)
			assert.True(t, h.Clustered)
			assert.Equal(t, uint64(len(want)), h.AddressedTiles)
			assert.Equal(t, plan.Header(), h)
			meta, err := plan.Metadata()
			require.NoError(t, err)
			assert.Equal(t, "synthetic", meta["name"])
		})
	}
}

func TestExtractStoresASharedContentOnce(t *testing.T) {
	src := &memSource{data: buildArchive(t, 8, CompressionGzip)}
	plan, err := NewPlan(context.Background(), src, Request{
		Bounds: Bounds{MinLon: -10, MinLat: -10, MaxLon: 10, MaxLat: 10}, MinZoom: 6, MaxZoom: 8,
	})
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, plan.WriteTo(context.Background(), src, &out, nil))
	h, tiles := readAll(t, out.Bytes())
	distinct := map[string]bool{}
	for _, v := range tiles {
		distinct[v] = true
	}
	assert.Equal(t, uint64(len(distinct)), h.TileContents)
	assert.Less(t, h.TileEntries, h.AddressedTiles, "runs of the shared content are joined")
}

func TestExtractClampsToTheSource(t *testing.T) {
	src := &memSource{data: buildArchive(t, 5, CompressionNone)}
	plan, err := NewPlan(context.Background(), src, Request{
		Bounds: Bounds{MinLon: -10, MinLat: -10, MaxLon: 10, MaxLat: 10}, MinZoom: 3, MaxZoom: 14,
	})
	require.NoError(t, err)
	assert.Equal(t, uint8(3), plan.Header().MinZoom)
	assert.Equal(t, uint8(5), plan.Header().MaxZoom)
}

func TestExtractRefusesWhatTheSourceDoesNotHold(t *testing.T) {
	src := &memSource{data: buildArchive(t, 4, CompressionNone)}
	_, err := NewPlan(context.Background(), src, Request{
		Bounds: Bounds{MinLon: -10, MinLat: -10, MaxLon: 10, MaxLat: 10}, MinZoom: 6, MaxZoom: 8,
	})
	require.ErrorIs(t, err, ErrNoOverlap)

	_, err = NewPlan(context.Background(), src, Request{Bounds: Bounds{MinLon: 10, MaxLon: -10, MinLat: 0, MaxLat: 1}})
	require.Error(t, err)

	_, err = NewPlan(context.Background(), &memSource{data: []byte("plain text, not tiles")}, Request{
		Bounds: Bounds{MinLon: -10, MinLat: -10, MaxLon: 10, MaxLat: 10},
	})
	require.ErrorIs(t, err, ErrNotPMTiles)
}

func TestExtractReportsAFailedRead(t *testing.T) {
	src := &memSource{data: buildArchive(t, 7, CompressionGzip)}
	plan, err := NewPlan(context.Background(), src, Request{
		Bounds: Bounds{MinLon: -10, MinLat: -10, MaxLon: 10, MaxLat: 10}, MinZoom: 0, MaxZoom: 7,
	})
	require.NoError(t, err)
	src.fail = errors.New("connection reset")
	err = plan.WriteTo(context.Background(), src, &bytes.Buffer{}, nil)
	require.ErrorContains(t, err, "connection reset")

	_, err = NewPlan(context.Background(), src, Request{Bounds: Bounds{MinLon: -10, MinLat: -10, MaxLon: 10, MaxLat: 10}})
	require.ErrorContains(t, err, "connection reset")
}

func TestExtractGroupsNeighboringReads(t *testing.T) {
	src := &memSource{data: buildArchive(t, 9, CompressionGzip)}
	plan, err := NewPlan(context.Background(), src, Request{
		Bounds: Bounds{MinLon: -125, MinLat: 24, MaxLon: -66, MaxLat: 50}, MinZoom: 0, MaxZoom: 9,
	})
	require.NoError(t, err)
	before := src.reads.Load()
	require.NoError(t, plan.WriteTo(context.Background(), src, &bytes.Buffer{}, nil))
	assert.Less(t, src.reads.Load()-before, int64(plan.Header().TileContents)/10, //nolint:gosec // a count of a few thousand
		"a few reads cover many small neighboring tiles")
}

func TestBoundsValidate(t *testing.T) {
	for _, b := range []Bounds{
		{MinLon: -181, MinLat: 0, MaxLon: 0, MaxLat: 1},
		{MinLon: 0, MinLat: -91, MaxLon: 1, MaxLat: 1},
		{MinLon: 1, MinLat: 0, MaxLon: 1, MaxLat: 1},
		{MinLon: 0, MinLat: 1, MaxLon: 1, MaxLat: 1},
	} {
		require.Error(t, b.Validate(), "%+v", b)
	}
	require.NoError(t, Bounds{MinLon: -1, MinLat: -1, MaxLon: 1, MaxLat: 1}.Validate())
}

func TestDecodeMetadata(t *testing.T) {
	z := &compressor{c: CompressionGzip}
	raw, err := z.compress([]byte(`{"version":"4.15.2"}`))
	require.NoError(t, err)
	got, err := DecodeMetadata(raw, CompressionGzip)
	require.NoError(t, err)
	assert.Equal(t, "4.15.2", got["version"])

	empty, err := DecodeMetadata(nil, CompressionNone)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = DecodeMetadata([]byte("{not json"), CompressionNone)
	require.Error(t, err)
}
