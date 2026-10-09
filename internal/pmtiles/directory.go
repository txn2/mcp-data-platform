package pmtiles

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Entry is one directory entry. RunLength 0 marks a pointer to a leaf
// directory, whose Offset is relative to the archive's leaf section;
// otherwise Offset is relative to the tile data section and the entry
// addresses RunLength consecutive tile ids holding the same content.
type Entry struct {
	TileID    uint64
	Offset    uint64
	Length    uint32
	RunLength uint32
}

// maxDirectoryEntries bounds the entry count a directory may declare, so a
// corrupt or hostile count cannot size an allocation. The specification sets
// no limit; real archives hold a few thousand entries per leaf.
const maxDirectoryEntries = 1 << 24

// maxDirectoryBytes bounds a directory's decompressed size for the same reason.
const maxDirectoryBytes = 1 << 28

var errCorruptDirectory = errors.New("pmtiles: corrupt directory")

// decompress undoes an archive's internal compression.
func decompress(data []byte, c Compression) ([]byte, error) {
	switch c {
	case CompressionNone, CompressionUnknown:
		return data, nil
	case CompressionGzip:
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("pmtiles: opening gzip directory: %w", err)
		}
		defer func() { _ = zr.Close() }()
		out, err := io.ReadAll(io.LimitReader(zr, maxDirectoryBytes+1))
		if err != nil {
			return nil, fmt.Errorf("pmtiles: reading gzip directory: %w", err)
		}
		if len(out) > maxDirectoryBytes {
			return nil, errCorruptDirectory
		}
		return out, nil
	case CompressionBrotli, CompressionZstd:
		return nil, fmt.Errorf("pmtiles: internal compression %d is not supported", c)
	default:
		return nil, fmt.Errorf("pmtiles: unknown internal compression %d", c)
	}
}

// compressor applies an archive's internal compression, reusing one gzip
// writer across the many directories an extract serializes: a fresh writer
// allocates about a megabyte of state, and a large extract writes thousands
// of leaves for each leaf size it tries.
type compressor struct {
	c  Compression
	zw *gzip.Writer
}

func (z *compressor) compress(data []byte) ([]byte, error) {
	switch z.c {
	case CompressionNone, CompressionUnknown:
		return data, nil
	case CompressionGzip:
		var buf bytes.Buffer
		if z.zw == nil {
			z.zw = gzip.NewWriter(&buf)
		} else {
			z.zw.Reset(&buf)
		}
		if _, err := z.zw.Write(data); err != nil {
			return nil, fmt.Errorf("pmtiles: compressing: %w", err)
		}
		if err := z.zw.Close(); err != nil {
			return nil, fmt.Errorf("pmtiles: compressing: %w", err)
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("pmtiles: internal compression %d is not supported", z.c)
	}
}

// ParseDirectory decodes one directory as stored, under the archive's
// internal compression.
func ParseDirectory(data []byte, c Compression) ([]Entry, error) {
	raw, err := decompress(data, c)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(raw)
	n, err := binary.ReadUvarint(r)
	if err != nil || n > maxDirectoryEntries {
		return nil, errCorruptDirectory
	}
	entries := make([]Entry, n)
	var last uint64
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, errCorruptDirectory
		}
		last += v
		entries[i].TileID = last
	}
	if err := readField(r, entries, func(e *Entry, v uint64) { e.RunLength = uint32(v) }); err != nil { // #nosec G115 -- the format stores u32 here
		return nil, err
	}
	if err := readField(r, entries, func(e *Entry, v uint64) { e.Length = uint32(v) }); err != nil { // #nosec G115 -- the format stores u32 here
		return nil, err
	}
	if err := readOffsets(r, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// readOffsets reads the offset column, where 0 means "directly after the
// entry before".
func readOffsets(r io.ByteReader, entries []Entry) error {
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return errCorruptDirectory
		}
		if v == 0 && i > 0 {
			entries[i].Offset = entries[i-1].Offset + uint64(entries[i-1].Length)
		} else {
			entries[i].Offset = v - 1
		}
	}
	return nil
}

func readField(r io.ByteReader, entries []Entry, set func(*Entry, uint64)) error {
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return errCorruptDirectory
		}
		set(&entries[i], v)
	}
	return nil
}

// SerializeDirectory encodes entries, which must be in tile id order, under
// the given internal compression.
func SerializeDirectory(entries []Entry, c Compression) ([]byte, error) {
	return encodeDirectory(entries, &compressor{c: c})
}

func encodeDirectory(entries []Entry, z *compressor) ([]byte, error) {
	buf := make([]byte, 0, len(entries))
	buf = binary.AppendUvarint(buf, uint64(len(entries)))
	var last uint64
	for _, e := range entries {
		buf = binary.AppendUvarint(buf, e.TileID-last)
		last = e.TileID
	}
	for _, e := range entries {
		buf = binary.AppendUvarint(buf, uint64(e.RunLength))
	}
	for _, e := range entries {
		buf = binary.AppendUvarint(buf, uint64(e.Length))
	}
	for i, e := range entries {
		if i > 0 && e.Offset == entries[i-1].Offset+uint64(entries[i-1].Length) {
			buf = binary.AppendUvarint(buf, 0)
		} else {
			buf = binary.AppendUvarint(buf, e.Offset+1)
		}
	}
	return z.compress(buf)
}

// firstLeafSize is the leaf size buildDirectories tries first, the size the
// reference writer starts at.
const firstLeafSize = 4096

// buildDirectories lays entries out as a root directory and, when they do
// not fit beside the header in the first 16 KiB, a section of leaf
// directories the root points into. It grows the leaf size until the root
// fits, the way the reference writer does.
func buildDirectories(entries []Entry, c Compression) (root, leaves []byte, err error) {
	z := &compressor{c: c}
	root, err = encodeDirectory(entries, z)
	if err != nil {
		return nil, nil, err
	}
	if len(root) <= RootFetchLen-HeaderLen {
		return root, nil, nil
	}
	for leafSize := firstLeafSize; ; leafSize *= 2 {
		root, leaves, err = splitDirectories(entries, leafSize, z)
		if err != nil {
			return nil, nil, err
		}
		if len(root) <= RootFetchLen-HeaderLen {
			return root, leaves, nil
		}
	}
}

func splitDirectories(entries []Entry, leafSize int, z *compressor) (root, leaves []byte, err error) {
	var rootEntries []Entry
	var leafBuf bytes.Buffer
	for start := 0; start < len(entries); start += leafSize {
		end := min(start+leafSize, len(entries))
		leaf, err := encodeDirectory(entries[start:end], z)
		if err != nil {
			return nil, nil, err
		}
		rootEntries = append(rootEntries, Entry{
			TileID: entries[start].TileID, Offset: uint64(leafBuf.Len()), Length: uint32(len(leaf)), // #nosec G115 -- a leaf is far below 4 GiB
		})
		_, _ = leafBuf.Write(leaf)
	}
	root, err = encodeDirectory(rootEntries, z)
	if err != nil {
		return nil, nil, err
	}
	return root, leafBuf.Bytes(), nil
}
