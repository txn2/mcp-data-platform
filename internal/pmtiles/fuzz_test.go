package pmtiles

import (
	"reflect"
	"testing"
)

// A remote archive's header and directories, or a file put in the bucket by
// hand (#2713): the header reader refuses what it cannot read and never
// panics, and a directory that parses serializes to one that parses to the
// same entries.

func FuzzParseHeader(f *testing.F) {
	f.Add(make([]byte, 127))
	f.Add(append([]byte("PMTiles\x03"), make([]byte, 119)...))
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = ParseHeader(b)
	})
}

func FuzzDirectoryRoundTrip(f *testing.F) {
	seed, err := SerializeDirectory([]Entry{{TileID: 0, Offset: 0, Length: 10, RunLength: 1}, {TileID: 5, Offset: 10, Length: 20, RunLength: 2}}, CompressionNone)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<16 {
			return
		}
		entries, err := ParseDirectory(b, CompressionNone)
		if err != nil {
			return
		}
		again, err := SerializeDirectory(entries, CompressionNone)
		if err != nil {
			return
		}
		back, err := ParseDirectory(again, CompressionNone)
		if err != nil {
			t.Fatalf("a serialized directory does not parse: %v", err)
		}
		if len(entries) == 0 && len(back) == 0 {
			return
		}
		if !reflect.DeepEqual(entries, back) {
			t.Fatalf("directory round-trips to %v, want %v", back, entries)
		}
	})
}
