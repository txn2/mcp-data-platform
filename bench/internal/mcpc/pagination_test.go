package mcpc

// ListTools pagination is exercised against a real streamable-HTTP MCP
// server whose page size is smaller than its toolset, so a single-page
// tools/list read would truncate. The per-endpoint benchmark arm (#1027)
// registers catalogs past the SDK's 1000-tool default page, making
// truncation here a silent measurement error rather than a crash.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListToolsFollowsPagination(t *testing.T) {
	const nTools = 7
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "paged", Version: "0.0.1"},
		&mcp.ServerOptions{PageSize: 2},
	)
	for i := range nTools {
		name := fmt.Sprintf("tool_%02d", i)
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "paged tool " + name},
			func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
			})
	}
	// platform_info must be dropped from the returned defs regardless of
	// which page it lands on.
	mcp.AddTool(srv, &mcp.Tool{Name: infoToolName, Description: "measurement plumbing"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil, nil
		})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
	})

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "bench-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	})

	defs, err := ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(defs) != nTools {
		t.Fatalf("ListTools returned %d tools, want all %d (pagination not followed)", len(defs), nTools)
	}
	for _, d := range defs {
		if d.Name == infoToolName {
			t.Fatalf("ListTools returned %s; measurement plumbing must be dropped", infoToolName)
		}
	}
}

// A dead upstream must surface as an error from the pagination iterator,
// not as a silently empty toolset.
func TestListToolsIteratorError(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "gone", Version: "0.0.1"}, nil)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "bench-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	ts.CloseClientConnections()
	ts.Close()
	if _, err := ListTools(ctx, session); err == nil {
		t.Fatal("ListTools against a dead upstream returned nil error")
	}
}
