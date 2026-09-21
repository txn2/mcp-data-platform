package connview

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// oneConnectionToolkit serves a single connection and lists none, which is the
// shape the datahub kind has.
type oneConnectionToolkit struct {
	registry.Toolkit
	kind     string
	name     string
	readOnly bool
}

func (t oneConnectionToolkit) Kind() string       { return t.kind }
func (t oneConnectionToolkit) Name() string       { return t.name }
func (t oneConnectionToolkit) Connection() string { return t.name }
func (t oneConnectionToolkit) IsReadOnly() bool   { return t.readOnly }

// A connection enumerated through the single-connection path reports its
// writability, so the answer does not depend on which path enumerated it
// (#1805). A toolkit that reports none leaves the field absent rather than
// false, which would read as "this connection accepts writes".
func TestFallbackEntryReportsWritability(t *testing.T) {
	cases := []struct {
		name     string
		toolkit  registry.Toolkit
		wantSaid bool
		want     bool
	}{
		{"a read-only single connection", oneConnectionToolkit{kind: "datahub", name: "acme", readOnly: true}, true, true},
		{"a writable single connection", oneConnectionToolkit{kind: "datahub", name: "acme"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := Build(context.Background(), []registry.Toolkit{tc.toolkit}, Deps{})
			if len(entries.Connections) != 1 {
				t.Fatalf("enumerated %d connections, want 1", len(entries.Connections))
			}
			got := entries.Connections[0].ReadOnly
			if !tc.wantSaid {
				if got != nil {
					t.Fatalf("reported %v; this kind has no writability to report", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("the connection reports no writability")
			}
			if *got != tc.want {
				t.Errorf("read_only = %v, want %v", *got, tc.want)
			}
		})
	}
}

// Verify the shape this file relies on.
var _ readOnlyReporter = oneConnectionToolkit{}
