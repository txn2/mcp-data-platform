package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestListToolsFollowsPagination verifies discovery follows tools/list
// cursors across pages instead of registering only the first page. The
// upstream serves 7 tools with a page size of 2, so a single-page
// ListTools would see 2 tools and silently drop the other 5 (the bug:
// against the SDK's default page size of 1000, any upstream with more
// than 1000 tools was truncated).
func TestListToolsFollowsPagination(t *testing.T) {
	const nTools = 7
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "paged-upstream", Version: "0.0.1"},
		&mcp.ServerOptions{PageSize: 2},
	)
	for i := range nTools {
		name := fmt.Sprintf("tool_%02d", i)
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "paged tool " + name},
			func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
				}, nil, nil
			})
	}
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})

	tk := New("paged")
	t.Cleanup(func() {
		if err := tk.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	if err := tk.AddConnection("paged", connectionConfig(ts.URL, "paged")); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if got := len(tk.Tools()); got != nTools {
		t.Fatalf("gateway discovered %d tools, want all %d (pagination not followed)", got, nTools)
	}
}
