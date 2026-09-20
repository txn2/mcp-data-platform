package connstored_test

import (
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/connstored"
)

// Writability is read off the stored config so the enumeration answers the same
// for a connection this replica serves and one another replica saved (#1805).
func TestReadOnly(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		config map[string]any
		want   *bool
	}{
		{"a read-only trino connection", "trino", map[string]any{"read_only": true}, new(true)},
		{"a writable trino connection", "trino", map[string]any{"read_only": false}, new(false)},
		{"a trino connection that never said", "trino", map[string]any{}, new(false)},
		{"an s3 connection", "s3", map[string]any{"read_only": true}, new(true)},
		{"a graphql connection", "graphql", map[string]any{"read_only": true}, new(true)},
		// A kind with no such setting reports NOTHING rather than false, which
		// a caller would read as "this connection accepts writes".
		{"an api connection", "api", map[string]any{"read_only": true}, nil},
		{"an mcp connection", "mcp", map[string]any{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connstored.ReadOnly(tc.kind, tc.config)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("kind %q reported %v; it has no writability to report", tc.kind, *got)
			case tc.want == nil:
				return
			case got == nil:
				t.Fatalf("kind %q reported nothing; want %v", tc.kind, *tc.want)
			case *got != *tc.want:
				t.Errorf("kind %q reported %v, want %v", tc.kind, *got, *tc.want)
			}
		})
	}
}
