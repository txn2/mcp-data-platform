package mcpcontext

import (
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProtocolVersion(t *testing.T) {
	if got := ProtocolVersion(nil); got != "" {
		t.Errorf("nil request: %q", got)
	}
	if got := ProtocolVersion(&mcp.CallToolRequest{}); got != "" {
		t.Errorf("no header and no session: %q", got)
	}
	h := http.Header{}
	h.Set("Mcp-Protocol-Version", "2026-07-28")
	if got := ProtocolVersion(&mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: h}}); got != "2026-07-28" {
		t.Errorf("the header's revision: %q", got)
	}
	if got := ProtocolVersion(&mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: http.Header{}}}); got != "" {
		t.Errorf("an empty header: %q", got)
	}
}
