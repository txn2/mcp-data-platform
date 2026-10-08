package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// rolesMapper maps the "analyst" role to the analyst persona and anything
// else to none.
type rolesMapper struct{ reg *persona.Registry }

func (rolesMapper) MapToRoles(map[string]any) ([]string, error) { return nil, nil }

func (m rolesMapper) MapToPersona(_ context.Context, roles []string) (*persona.Persona, error) {
	for _, r := range roles {
		if p, ok := m.reg.Get(r); ok {
			return p, nil
		}
	}
	return nil, errNoPersona
}

// errNoPersona is the mapper's answer for roles no persona carries; the
// authorizer refuses with no persona either way.
var errNoPersona = errors.New("no persona for these roles")

// TestToolCallDenials_CountedByReason drives refused calls through the real
// persona authorizer and the assembled chain, so the class read from the
// authorizer's reason is the one that authorizer gives (#1898).
func TestToolCallDenials_CountedByReason(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Shutdown(context.Background()) }()

	reg := persona.NewRegistry()
	if err := reg.Register(&persona.Persona{
		Name: "analyst", Roles: []string{"analyst"},
		Tools:       persona.ToolRules{Allow: []string{"trino_query"}},
		Connections: persona.ConnectionRules{Allow: []string{"warehouse"}},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := persona.NewAuthorizer(reg, rolesMapper{reg: reg})

	tests := []struct {
		name  string
		roles []string
		tool  string
		conn  string
		want  string
	}{
		{
			"tool the persona does not allow",
			[]string{"analyst"},
			"s3_list", "warehouse",
			`mcp_tool_call_denials_total{persona="analyst",reason="tool_denied"} 1`,
		},
		{
			"connection the persona does not allow",
			[]string{"analyst"},
			"trino_query", "elsewhere",
			`mcp_tool_call_denials_total{persona="analyst",reason="connection_denied"} 1`,
		},
		{
			"no persona",
			[]string{"guest"},
			"trino_query", "warehouse",
			`mcp_tool_call_denials_total{persona="unknown",reason="no_persona"} 1`,
		},
	}
	for _, tt := range tests {
		server := mcp.NewServer(&mcp.Implementation{Name: "denials", Version: "v0"}, nil)
		server.AddTool(&mcp.Tool{Name: tt.tool, InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ran"}}}, nil
			})
		server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
			&fakeAuthn{user: &middleware.UserInfo{UserID: "u1", Roles: tt.roles}}, authorizer,
			&fakeLookup{kind: "trino", name: "prod", conn: tt.conn},
			middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
		))
		// Outermost, as the platform registers it, so a refusal is counted.
		server.AddReceivingMiddleware(middleware.MCPMetricsMiddleware(m))

		ctx := context.Background()
		sess := mustConnect(ctx, t, server)
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tt.tool, Arguments: map[string]any{}})
		_ = sess.Close()
		if err != nil {
			t.Fatalf("%s: CallTool: %v", tt.name, err)
		}
		if !res.IsError {
			t.Fatalf("%s: the call was not refused", tt.name)
		}
		if body := scrape(t, m.Handler()); !strings.Contains(body, tt.want) {
			t.Errorf("%s: scrape missing %q", tt.name, tt.want)
		}
	}
}
