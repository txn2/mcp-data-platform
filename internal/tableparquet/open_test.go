package tableparquet

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// The footer the daily fuzzing found (#2754) declares a schema element with a
// negative number of children, which panicked the library while it laid the
// columns out. It is refused before the library reads it, naming the element.
func TestOpen_AFooterDeclaringNegativeChildrenIsRefused(t *testing.T) {
	raw, err := os.ReadFile("testdata/fuzz/FuzzColumns/aaad4f2256a2749d")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)[1]
	body, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(line, "[]byte("), ")"))
	if err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func() error{
		"Columns": func() error { _, err := Columns(bytes.NewReader([]byte(body)), int64(len(body))); return err },
		"Open":    func() error { _, err := Open(bytes.NewReader([]byte(body)), int64(len(body))); return err },
	} {
		err := open()
		if !errors.Is(err, ErrNotParquet) || !strings.Contains(err.Error(), "children") {
			t.Errorf("%s: %v, want a refusal naming the children", name, err)
		}
	}
}

// Open hands back the library's file for one the platform wrote, and refuses
// what is not Parquet before the library reads it.
func TestOpen(t *testing.T) {
	data, err := Write([]tabletype.Column{{Name: "id", Type: tabletype.Type{Kind: tabletype.Bigint}}}, [][]any{{int64(7)}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil || f.NumRows() != 1 {
		t.Fatalf("Open = %v, %v", f, err)
	}
	if _, err := Open(bytes.NewReader([]byte("not parquet at all")), 18); !errors.Is(err, ErrNotParquet) {
		t.Errorf("text opened: %v", err)
	}
	// A footer of the stated length that does not decode is not Parquet.
	bad := append([]byte(Magic), 0xff, 0xff, 0xff, 0xff, 4, 0, 0, 0)
	bad = append(bad, Magic...)
	if _, err := Open(bytes.NewReader(bad), int64(len(bad))); !errors.Is(err, ErrNotParquet) {
		t.Errorf("an undecodable footer opened: %v", err)
	}
}
