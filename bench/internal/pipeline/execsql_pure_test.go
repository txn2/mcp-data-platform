package pipeline

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRowsFromJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
		rows int
	}{
		{"envelope", `{"rows":[{"a":1},{"a":2}]}`, true, 2},
		{"bare array", `[{"a":1}]`, true, 1},
		{"empty envelope rows", `{"rows":[]}`, true, 0},
		{"garbage", `not json`, false, 0},
		{"scalar", `5`, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, ok := rowsFromJSON([]byte(c.raw))
			if ok != c.ok || len(rows) != c.rows {
				t.Errorf("rowsFromJSON(%s) = %d rows, ok=%v; want %d, %v", c.raw, len(rows), ok, c.rows, c.ok)
			}
		})
	}
}

func TestRowsFromResult(t *testing.T) {
	structured := &mcp.CallToolResult{StructuredContent: map[string]any{"rows": []map[string]any{{"n": 1}}}}
	rows, err := rowsFromResult(structured)
	if err != nil || len(rows) != 1 {
		t.Errorf("structured: %d rows, err %v", len(rows), err)
	}
	textOnly := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `[{"n":9}]`}}}
	rows, err = rowsFromResult(textOnly)
	if err != nil || len(rows) != 1 {
		t.Errorf("text fallback: %d rows, err %v", len(rows), err)
	}
	empty := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "no rows here"}}}
	if _, err := rowsFromResult(empty); err == nil {
		t.Error("expected error when no parseable rows")
	}
}
