package platform

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// TestToolsSource_LoadItems wires a real in-memory MCP server with two
// tools and proves the Source enumerates them via tools/list, builds
// embed text, and excludes the discovery tool itself.
func TestToolsSource_LoadItems(t *testing.T) {
	t.Parallel()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	noop := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "alpha", Description: "do the alpha thing"}, noop)
	mcp.AddTool(srv, &mcp.Tool{Name: platformFindToolsName, Description: "discovery"}, noop)

	p := &Platform{mcpServer: srv}
	s := &toolsSource{p: p}

	if s.Kind() != toolsindex.SourceKind {
		t.Errorf("Kind() = %q; want %q", s.Kind(), toolsindex.SourceKind)
	}

	items, err := s.LoadItems(context.Background(), toolsindex.SourceID)
	if err != nil {
		t.Fatalf("LoadItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d; want 1 (discovery tool excluded): %+v", len(items), items)
	}
	if items[0].ItemID != "alpha" {
		t.Errorf("item id = %q; want alpha", items[0].ItemID)
	}
	if items[0].Text == "" {
		t.Error("embed text should not be empty")
	}

	s.OnSucceeded("x") // no-op; must not panic
}

func TestEnumerateGlobalTools_NilServer(t *testing.T) {
	t.Parallel()
	p := &Platform{} // no mcpServer
	if _, err := p.enumerateGlobalTools(context.Background()); err == nil {
		t.Error("expected error when mcp server is not initialized")
	}
}
