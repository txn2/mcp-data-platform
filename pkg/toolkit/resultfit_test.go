package toolkit

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSetFittedResult(t *testing.T) {
	image := &mcp.ImageContent{Data: []byte("png"), MIMEType: "image/png"}
	res := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: "old one"}, image, &mcp.TextContent{Text: "old two"}},
		StructuredContent: json.RawMessage(`{"old":true}`),
		Meta:              mcp.Meta{"kept": true},
	}
	if err := SetFittedResult(res, []byte("fitted"), map[string]int{"rows": 3}); err != nil {
		t.Fatalf("SetFittedResult: %v", err)
	}
	if len(res.Content) != 2 || textOf(t, res.Content[0]) != "fitted" || res.Content[1] != image {
		t.Errorf("content = %v; want the fitted text in place of every text block, the image kept", res.Content)
	}
	if raw, ok := res.StructuredContent.(json.RawMessage); !ok || string(raw) != `{"rows":3}` {
		t.Errorf("structured = %v; want the fitted value, compact", res.StructuredContent)
	}
	if kept, _ := res.Meta["kept"].(bool); !kept {
		t.Error("the result's _meta was dropped")
	}
}

func TestSetFittedResult_NoTextBlock(t *testing.T) {
	image := &mcp.ImageContent{Data: []byte("png"), MIMEType: "image/png"}
	res := &mcp.CallToolResult{Content: []mcp.Content{image}}
	if err := SetFittedResult(res, []byte("fitted"), 1); err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 2 || textOf(t, res.Content[0]) != "fitted" {
		t.Errorf("content = %v; want the fitted text first", res.Content)
	}
}

func TestSetFittedResult_UnencodableValue(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "old"}}}
	if err := SetFittedResult(res, []byte("fitted"), math.Inf(1)); err == nil {
		t.Fatal("an unencodable value was accepted")
	}
	if textOf(t, res.Content[0]) != "old" {
		t.Error("a failed fit changed the result")
	}
}

// textOf is a content block's text, failing the test when it is not text.
func textOf(t *testing.T, c mcp.Content) string {
	t.Helper()
	tc, ok := c.(*mcp.TextContent)
	if !ok {
		t.Fatalf("content %v is not text", c)
	}
	return tc.Text
}

func TestDecodeStructured(t *testing.T) {
	var out struct {
		Rows int `json:"rows"`
	}
	if !DecodeStructured(json.RawMessage(`{"rows":3}`), &out) || out.Rows != 3 {
		t.Errorf("raw JSON: rows = %d", out.Rows)
	}
	if !DecodeStructured(map[string]int{"rows": 4}, &out) || out.Rows != 4 {
		t.Errorf("Go value: rows = %d", out.Rows)
	}
	if DecodeStructured(nil, &out) {
		t.Error("nil decoded")
	}
	if DecodeStructured(math.Inf(1), &out) {
		t.Error("an unencodable value decoded")
	}
	if DecodeStructured(json.RawMessage(`[1]`), &out) {
		t.Error("a value of another shape decoded")
	}
}
