package mcpc

// Connect, Mint, ListTools, and Call are exercised end-to-end against a real
// streamable-HTTP MCP server by internal/pipeline's integration test; these
// unit tests cover the pure transformations.

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStripSessionArg(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sql":        map[string]any{"type": "string"},
			"session_id": map[string]any{"type": "string"},
		},
		"required": []any{"sql", "session_id"},
	}
	out, err := stripSessionArg(schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	props := m["properties"].(map[string]any)
	if _, ok := props["session_id"]; ok {
		t.Error("session_id property not stripped")
	}
	if _, ok := props["sql"]; !ok {
		t.Error("sql property lost")
	}
	req := m["required"].([]any)
	if len(req) != 1 || req[0] != "sql" {
		t.Errorf("required = %v, want [sql]", req)
	}
}

func TestStripSessionArgNoProperties(t *testing.T) {
	out, err := stripSessionArg(map[string]any{"type": "object"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "object" {
		t.Errorf("schema mangled: %v", m)
	}
}

func TestErrorCode(t *testing.T) {
	cases := []struct {
		name string
		res  *mcp.CallToolResult
		want string
	}{
		{"nil result", nil, ""},
		{"not an error", &mcp.CallToolResult{}, ""},
		{"no structured content", &mcp.CallToolResult{IsError: true}, ""},
		{"envelope", &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{
			"error": map[string]any{"code": "unauthorized"},
		}}, "unauthorized"},
		{"foreign structured content", &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{
			"rows": 3,
		}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := errorCode(c.res); got != c.want {
				t.Errorf("errorCode = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAllTextJoins(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "a"},
		&mcp.TextContent{Text: "b"},
	}}
	if got := allText(res); got != "a\nb" {
		t.Errorf("allText = %q", got)
	}
	if got := allText(nil); got != "" {
		t.Errorf("allText(nil) = %q", got)
	}
}
