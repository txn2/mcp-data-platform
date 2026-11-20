package tableparquet

import (
	"bytes"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// FuzzColumns holds the footer reader to a registered file (#2713): a file the
// platform's own writer wrote reads back its columns by name, and any file's
// columns are read or refused without panicking.
func FuzzColumns(f *testing.F) {
	cols := []tabletype.Column{{Name: "id", Type: tabletype.Type{Kind: tabletype.Bigint}}, {Name: "name", Type: tabletype.Type{Kind: tabletype.Varchar}}}
	seed, err := Write(cols, [][]any{{int64(1), "a"}, {int64(2), "b"}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("PAR1PAR1"))
	f.Add([]byte{})
	got, err := Columns(bytes.NewReader(seed), int64(len(seed)))
	if err != nil || len(got) != 2 || got[0].Name != "id" || got[1].Name != "name" {
		f.Fatalf("the writer's own file reads back %v, %v", got, err)
	}
	f.Fuzz(func(_ *testing.T, b []byte) {
		if len(b) > 1<<16 {
			return
		}
		_, _ = Columns(bytes.NewReader(b), int64(len(b)))
	})
}
