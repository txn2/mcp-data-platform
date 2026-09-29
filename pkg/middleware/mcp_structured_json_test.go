package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tool that sets its structured content as a Go value holding an empty
// list reaches the client with [] once the outermost layer has encoded it,
// and with null without it (#1832).
func TestMCPResultTypeMiddleware_EncodesStructuredContentForTheWire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outer   bool
		wantRaw string
	}{
		{"through the outermost layer", true, `{"sections":[{"items":[]}]}`},
		{"without it", false, `{"sections":[{"items":null}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "sc", Version: "v0"}, nil)
			server.AddTool(&mcp.Tool{Name: "collection", InputSchema: map[string]any{"type": "object"}},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					var none []string
					return &mcp.CallToolResult{
						Content:           []mcp.Content{&mcp.TextContent{Text: "{}"}},
						StructuredContent: map[string]any{"sections": []map[string]any{{"items": none}}},
					}, nil
				})
			if tc.outer {
				server.AddReceivingMiddleware(MCPResultTypeMiddleware())
			}
			st, ct := mcp.NewInMemoryTransports()
			ss, err := server.Connect(context.Background(), st, nil)
			require.NoError(t, err)
			defer func() { _ = ss.Close() }()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(context.Background(), ct, nil)
			require.NoError(t, err)
			defer func() { _ = cs.Close() }()

			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "collection", Arguments: map[string]any{}})
			require.NoError(t, err)
			got, err := json.Marshal(res.StructuredContent)
			require.NoError(t, err)
			assert.JSONEq(t, tc.wantRaw, string(got))
		})
	}
}

// Content already encoded, and a value the encoder cannot encode, are left as
// they are.
func TestEncodeStructured_LeavesWhatItCannotImprove(t *testing.T) {
	raw := json.RawMessage(`{"items":null}`)
	res := &mcp.CallToolResult{StructuredContent: raw}
	encodeStructured(res)
	assert.Equal(t, raw, res.StructuredContent)

	bad := map[string]any{"f": func() {}}
	res = &mcp.CallToolResult{StructuredContent: bad}
	encodeStructured(res)
	assert.IsType(t, map[string]any{}, res.StructuredContent)

	encodeStructured(&mcp.CallToolResult{})
	encodeStructured(&mcp.GetPromptResult{})
	encodeStructured((*mcp.CallToolResult)(nil))
}
