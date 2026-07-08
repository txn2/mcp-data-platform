package platform

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEnumerateGlobalTools_NilServer proves the guard: with no MCP server wired
// the enumeration errors rather than panicking.
func TestEnumerateGlobalTools_NilServer(t *testing.T) {
	t.Parallel()
	p := &Platform{} // no mcpServer
	if _, err := p.enumerateGlobalTools(context.Background()); err == nil {
		t.Error("expected error when mcp server is not initialized")
	}
}

// TestPlatformToolEnumerator_DelegatesToEnumerate proves the adapter forwards to
// the platform's enumerateGlobalTools (here exercising the nil-server error
// path, since that is what the injected interface must surface to the queue).
func TestPlatformToolEnumerator_DelegatesToEnumerate(t *testing.T) {
	t.Parallel()
	e := platformToolEnumerator{p: &Platform{}}
	if _, err := e.EnumerateGlobalTools(context.Background()); err == nil {
		t.Error("adapter should surface the enumeration error from the platform")
	}
}

// TestEnumerateGlobalTools_ListsTools wires a real in-memory MCP server with two
// tools and proves enumeration returns them via tools/list.
func TestEnumerateGlobalTools_ListsTools(t *testing.T) {
	t.Parallel()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	noop := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "alpha", Description: "do the alpha thing"}, noop)
	mcp.AddTool(srv, &mcp.Tool{Name: platformFindToolsName, Description: "discovery"}, noop)

	p := &Platform{mcpServer: srv}
	tools, err := p.enumerateGlobalTools(context.Background())
	if err != nil {
		t.Fatalf("enumerateGlobalTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d; want 2 (alpha + discovery)", len(tools))
	}
}
