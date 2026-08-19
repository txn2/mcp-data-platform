package platform

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

const (
	rtBareErrorTool = "rt_bare_error"
	rtDeniedTool    = "rt_denied"
	rtPlainTool     = "rt_plain"
	rtUpstreamConn  = "mcptest"
)

// TestEveryToolResultCarriesResultTypeThroughTheAssembledChain is the wiring
// proof for #1382 and #1383. It assembles the real platform with the full
// receiving chain, connects a client on the revision that requires resultType,
// and drives every way a result reaches the client: a handler success the SDK
// types itself, a bare error the error contract rebuilds (the shape of a
// run_script refusal), a persona denial the authz layer short-circuits, a
// purpose-gate refusal, and a gateway-proxied upstream's success and error.
// Every one must reach the client typed complete, with its body intact.
func TestEveryToolResultCarriesResultTypeThroughTheAssembledChain(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Name: "test-platform"},
		Semantic: SemanticConfig{Provider: testProviderNoop},
		Query:    QueryConfig{Provider: testProviderNoop},
		Storage:  StorageConfig{Provider: testProviderNoop},
		Personas: PersonasConfig{Definitions: map[string]PersonaDef{"default": {
			DisplayName: "Default",
			Roles:       []string{auth.RoleAnonymous},
			Tools:       ToolRulesDef{Allow: []string{"*"}, Deny: []string{rtDeniedTool}},
			Connections: ConnectionRulesDef{Allow: []string{"*"}},
		}}},
	}
	p, err := New(WithConfig(cfg))
	require.NoError(t, err)
	ctx := context.Background()

	// An upstream MCP server proxied through a kind:mcp gateway connection,
	// with one tool that succeeds and one that answers with a tool error.
	upstream := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "v0"}, nil)
	mcp.AddTool(upstream, &mcp.Tool{Name: "whoami"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"name":"tester"}`}}}, nil, nil
	})
	mcp.AddTool(upstream, &mcp.Tool{Name: "boom"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "upstream said no"}}}, nil, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, nil))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	gw := gateway.New(rtUpstreamConn)
	require.NoError(t, p.ToolkitRegistry().Register(gw))

	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()
	require.NoError(t, gw.AddConnection(rtUpstreamConn, map[string]any{"endpoint": ts.URL, "connection_name": rtUpstreamConn}))

	// The platform-side handler shapes: a bare IsError result (what the
	// managed-script tool returns for a refusal) and a plain success.
	p.MCPServer().AddTool(&mcp.Tool{Name: rtBareErrorTool, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "script has no approved version"}}}, nil
		})
	for _, name := range []string{rtPlainTool, rtDeniedTool} {
		p.MCPServer().AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ran"}}}, nil
			})
	}

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := p.MCPServer().Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()
	require.GreaterOrEqual(t, cs.InitializeResult().ProtocolVersion, "2026-07-28",
		"the client negotiates the revision that requires resultType")

	info, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: map[string]any{}})
	require.NoError(t, err)
	sid := sessionIDFromSC(t, info.StructuredContent)
	require.NotEmpty(t, sid)
	withSession := func(extra map[string]any) map[string]any {
		args := map[string]any{"session_id": sid, "purpose": "proving the wire envelope of every result"}
		maps.Copy(args, extra)
		return args
	}

	calls := []struct {
		name     string
		args     map[string]any
		wantErr  bool
		wantText string
	}{
		{rtPlainTool, withSession(nil), false, "ran"},
		{rtBareErrorTool, withSession(nil), true, "script has no approved version"},
		{rtDeniedTool, withSession(nil), true, "not permitted"},
		{rtUpstreamConn + gateway.NamespaceSeparator + "whoami", withSession(nil), false, "tester"},
		// No purpose on a data call: the purpose gate refuses before the
		// proxy runs.
		{rtUpstreamConn + gateway.NamespaceSeparator + "whoami", map[string]any{"session_id": sid}, true, "PURPOSE_REQUIRED"},
		{rtUpstreamConn + gateway.NamespaceSeparator + "boom", withSession(nil), true, "upstream said no"},
	}
	for _, c := range calls {
		label := c.name
		if c.wantText == "PURPOSE_REQUIRED" {
			label += " (purpose gate)"
		}
		t.Run(label, func(t *testing.T) {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			require.NoError(t, err, "the call is answered with a result, not a protocol error")
			assert.Equal(t, c.wantErr, res.IsError, "isError: %v", res.Content)
			require.NotEmpty(t, res.Content)
			text, ok := res.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Contains(t, text.Text, c.wantText, "the body the platform composed reaches the client")

			wire, err := json.Marshal(res)
			require.NoError(t, err)
			var m map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(wire, &m))
			raw, has := m["resultType"]
			require.True(t, has, "the result carries resultType on the wire: %s", wire)
			assert.JSONEq(t, `"complete"`, string(raw))
		})
	}
}
