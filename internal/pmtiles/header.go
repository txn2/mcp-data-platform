// Package pmtiles reads and writes PMTiles version 3 archives, and extracts
// the part of one that covers a bounding box and a zoom range (#2068).
//
// It exists rather than github.com/protomaps/go-pmtiles because the platform
// fetches a basemap through its own HTTP client (internal/outbound, so the
// fetch is traced and counted) and writes the result straight into object
// storage. go-pmtiles' Extract opens its source with http.DefaultClient and
// writes to a local file path, and its one package imports the Google Cloud,
// Azure and SQLite SDKs. The format is small: a 127-byte header, varint
// directories, and tile data addressed by Hilbert tile id.
//
// The specification is https://github.com/protomaps/PMTiles/blob/main/spec/v3/spec.md.
package pmtiles

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderLen is the fixed size of a version 3 header.
const HeaderLen = 127

// RootFetchLen is how much of an archive holds its header and root directory:
// the specification requires both inside the first 16 KiB, so one read of
// this length answers both.
const RootFetchLen = 16384

// specVersion is the version this package reads and writes.
const specVersion = 3

// The byte offset of each header field, in the specification's order.
const (
	offMagic          = 0
	offVersion        = 7
	offRootOffset     = 8
	offRootLength     = 16
	offMetadataOffset = 24
	offMetadataLength = 32
	offLeafOffset     = 40
	offLeafLength     = 48
	offTileDataOffset = 56
	offTileDataLength = 64
	offAddressed      = 72
	offEntries        = 80
	offContents       = 88
	offClustered      = 96
	offInternalComp   = 97
	offTileComp       = 98
	offTileType       = 99
	offMinZoom        = 100
	offMaxZoom        = 101
	offMinLon         = 102
	offMinLat         = 106
	offMaxLon         = 110
	offMaxLat         = 114
	offCenterZoom     = 118
	offCenterLon      = 119
	offCenterLat      = 123
)

// e7 is the scale the format stores degrees at.
const e7 = 1e7

// Compression is the compression of an archive's directories, metadata or
// tiles.
type Compression uint8

// The compressions the specification defines.
const (
	CompressionUnknown Compression = 0
	CompressionNone    Compression = 1
	CompressionGzip    Compression = 2
	CompressionBrotli  Compression = 3
	CompressionZstd    Compression = 4
)

// TileType is the format of an archive's tiles.
type TileType uint8

// The tile types the specification defines.
const (
	TileTypeUnknown TileType = 0
	TileTypeMVT     TileType = 1
	TileTypePNG     TileType = 2
	TileTypeJPEG    TileType = 3
	TileTypeWebP    TileType = 4
	TileTypeAVIF    TileType = 5
)

// Header is a version 3 header. Bounds and center are in degrees times 10^7,
// as the format stores them.
type Header struct {
	RootOffset          uint64
	RootLength          uint64
	MetadataOffset      uint64
	MetadataLength      uint64
	LeafOffset          uint64
	LeafLength          uint64
	TileDataOffset      uint64
	TileDataLength      uint64
	AddressedTiles      uint64
	TileEntries         uint64
	TileContents        uint64
	Clustered           bool
	InternalCompression Compression
	TileCompression     Compression
	TileType            TileType
	MinZoom             uint8
	MaxZoom             uint8
	MinLonE7            int32
	MinLatE7            int32
	MaxLonE7            int32
	MaxLatE7            int32
	CenterZoom          uint8
	CenterLonE7         int32
	CenterLatE7         int32
}

// ErrNotPMTiles is returned for bytes that do not begin a version 3 archive.
var ErrNotPMTiles = errors.New("pmtiles: not a PMTiles version 3 archive")

var magic = []byte("PMTiles")

// i32 reads a two's-complement field.
func i32(b []byte) int32 {
	return int32(binary.LittleEndian.Uint32(b)) // #nosec G115 -- the format stores these fields as two's-complement
}

// putI32 writes a two's-complement field.
func putI32(b []byte, v int32) {
	binary.LittleEndian.PutUint32(b, uint32(v)) // #nosec G115 -- the format stores these fields as two's-complement
}

// ParseHeader reads the header from the first HeaderLen bytes of an archive.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderLen || !bytes.Equal(b[offMagic:offVersion], magic) {
		return Header{}, ErrNotPMTiles
	}
	if b[offVersion] != specVersion {
		return Header{}, fmt.Errorf("pmtiles: spec version %d: %w", b[offVersion], ErrNotPMTiles)
	}
	le := binary.LittleEndian
	return Header{
		RootOffset:          le.Uint64(b[offRootOffset:]),
		RootLength:          le.Uint64(b[offRootLength:]),
		MetadataOffset:      le.Uint64(b[offMetadataOffset:]),
		MetadataLength:      le.Uint64(b[offMetadataLength:]),
		LeafOffset:          le.Uint64(b[offLeafOffset:]),
		LeafLength:          le.Uint64(b[offLeafLength:]),
		TileDataOffset:      le.Uint64(b[offTileDataOffset:]),
		TileDataLength:      le.Uint64(b[offTileDataLength:]),
		AddressedTiles:      le.Uint64(b[offAddressed:]),
		TileEntries:         le.Uint64(b[offEntries:]),
		TileContents:        le.Uint64(b[offContents:]),
		Clustered:           b[offClustered] == 1,
		InternalCompression: Compression(b[offInternalComp]),
		TileCompression:     Compression(b[offTileComp]),
		TileType:            TileType(b[offTileType]),
		MinZoom:             b[offMinZoom],
		MaxZoom:             b[offMaxZoom],
		MinLonE7:            i32(b[offMinLon:]),
		MinLatE7:            i32(b[offMinLat:]),
		MaxLonE7:            i32(b[offMaxLon:]),
		MaxLatE7:            i32(b[offMaxLat:]),
		CenterZoom:          b[offCenterZoom],
		CenterLonE7:         i32(b[offCenterLon:]),
		CenterLatE7:         i32(b[offCenterLat:]),
	}, nil
}

// Bytes serializes the header.
func (h Header) Bytes() []byte {
	b := make([]byte, HeaderLen)
	copy(b, magic)
	b[offVersion] = specVersion
	le := binary.LittleEndian
	le.PutUint64(b[offRootOffset:], h.RootOffset)
	le.PutUint64(b[offRootLength:], h.RootLength)
	le.PutUint64(b[offMetadataOffset:], h.MetadataOffset)
	le.PutUint64(b[offMetadataLength:], h.MetadataLength)
	le.PutUint64(b[offLeafOffset:], h.LeafOffset)
	le.PutUint64(b[offLeafLength:], h.LeafLength)
	le.PutUint64(b[offTileDataOffset:], h.TileDataOffset)
	le.PutUint64(b[offTileDataLength:], h.TileDataLength)
	le.PutUint64(b[offAddressed:], h.AddressedTiles)
	le.PutUint64(b[offEntries:], h.TileEntries)
	le.PutUint64(b[offContents:], h.TileContents)
	if h.Clustered {
		b[offClustered] = 1
	}
	b[offInternalComp] = byte(h.InternalCompression)
	b[offTileComp] = byte(h.TileCompression)
	b[offTileType] = byte(h.TileType)
	b[offMinZoom] = h.MinZoom
	b[offMaxZoom] = h.MaxZoom
	putI32(b[offMinLon:], h.MinLonE7)
	putI32(b[offMinLat:], h.MinLatE7)
	putI32(b[offMaxLon:], h.MaxLonE7)
	putI32(b[offMaxLat:], h.MaxLatE7)
	b[offCenterZoom] = h.CenterZoom
	putI32(b[offCenterLon:], h.CenterLonE7)
	putI32(b[offCenterLat:], h.CenterLatE7)
	return b
}

// Bounds returns the header's bounds in degrees.
func (h Header) Bounds() Bounds {
	return Bounds{
		MinLon: float64(h.MinLonE7) / e7, MinLat: float64(h.MinLatE7) / e7,
		MaxLon: float64(h.MaxLonE7) / e7, MaxLat: float64(h.MaxLatE7) / e7,
	}
}
