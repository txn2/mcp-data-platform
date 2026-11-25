package platform

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolSearchTestServer returns an in-memory MCP server with the given tools,
// each with a description naming it, plus search itself.
func toolSearchTestServer(names ...string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	noop := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	}
	for _, n := range names {
		mcp.AddTool(srv, &mcp.Tool{Name: n, Description: "desc of " + n}, noop)
	}
	mcp.AddTool(srv, &mcp.Tool{Name: searchToolName, Description: "search desc"}, noop)
	return srv
}

// TestToolRanker_ReadsTheLiveServer is #2809: search's tools source ranks the
// server's tools as tools/list lists them, never search itself, and with no
// authorizer every tool is allowed (as the visibility middleware does).
func TestToolRanker_ReadsTheLiveServer(t *testing.T) {
	t.Parallel()
	p := &Platform{mcpServer: toolSearchTestServer("alpha", "beta")}
	got := p.toolRanker().RankTools(context.Background(), "desc alpha", nil, 10)
	if len(got) == 0 || got[0].Name != "alpha" {
		t.Fatalf("ranked = %+v, want alpha first", got)
	}
	for _, m := range got {
		if m.Name == searchToolName {
			t.Error("search ranked itself")
		}
	}
	if !p.toolRanker().Allowed(context.Background(), []string{"analyst"}, "alpha") {
		t.Error("with no authorizer a tool was refused")
	}
}

// resultText is a tool result's first text block, "" when it has none.
func resultText(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

// TestConnectionRoutes_ReadsTheRegistry: the routing layer's view of the
// deployment comes from the live toolkit registry.
func TestConnectionRoutes_ReadsTheRegistry(t *testing.T) {
	t.Parallel()
	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()
	d := p.connectionRoutes()
	_ = d.Connections()
	if got := d.ToolKind("no_such_tool"); got != "" {
		t.Errorf("an unregistered tool has kind %q", got)
	}
	if got := d.KindTools("no_such_kind"); len(got) != 0 {
		t.Errorf("an unknown kind has tools %v", got)
	}
}

// TestConnectionKinds_FollowsTheRegistry: a deployment with no connections
// lists no kinds, so platform_info adds no connection line.
func TestConnectionKinds_FollowsTheRegistry(t *testing.T) {
	t.Parallel()
	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()
	if got := p.connectionKinds([]string{"trino_query"}); len(got) != 0 {
		t.Errorf("kinds = %+v, want none", got)
	}
}
