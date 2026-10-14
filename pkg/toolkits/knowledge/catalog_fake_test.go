package knowledge

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
)

// newFakeCatalog returns an empty in-memory catalog.
func newFakeCatalog() *catalogfake.Store { return catalogfake.New() }

// resultText returns the text of a tool result's first content block.
func resultText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
