package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// TestBuildRootHandler_ServesEveryRevisionWithTheMemoryStore is #2008: a
// deployment with no database session store -- the memory store, the default
// -- serves a client asking for MCP 2026-07-28 that revision instead of
// negotiating it down, still serves 2025-11-25, and platform_info reports the
// revision each session speaks.
func TestBuildRootHandler_ServesEveryRevisionWithTheMemoryStore(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test", Streamable: platform.StreamableConfig{SessionTimeout: testSessionTimeout}},
		Personas: platform.PersonasConfig{Definitions: map[string]platform.PersonaDef{"default": {
			DisplayName: "Default", Roles: []string{auth.RoleAnonymous}, Tools: platform.ToolRulesDef{Allow: []string{"*"}},
		}}},
	})
	defer func() { _ = p.Close() }()
	ctx := t.Context()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	srv := httptest.NewServer(buildRootHandler(ctx, p.MCPServer(), p, extractHTTPConfig(p)))
	defer srv.Close()

	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil)
		cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, &mcp.ClientSessionOptions{ProtocolVersion: version})
		if err != nil {
			t.Fatalf("%s: connect: %v", version, err)
		}
		if got := cs.InitializeResult().ProtocolVersion; got != version {
			t.Errorf("a client asking for %s was negotiated to %s", version, got)
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: map[string]any{}})
		if err != nil || res.IsError {
			t.Fatalf("%s: platform_info: %v %v", version, err, res)
		}
		var info struct {
			ProtocolVersion string `json:"protocol_version"`
		}
		text, _ := res.Content[0].(*mcp.TextContent)
		if text == nil || json.Unmarshal([]byte(text.Text), &info) != nil {
			t.Fatalf("%s: platform_info answered %v", version, res.Content)
		}
		if info.ProtocolVersion != version {
			t.Errorf("platform_info reports protocol_version %q to a %s session", info.ProtocolVersion, version)
		}
		_ = cs.Close()
	}
}
