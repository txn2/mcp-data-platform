package middleware_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/scriptcallsite"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// auditCallSite makes one tool call carrying a call site in its _meta over a
// real server with the tool-call and audit middleware, connected under ctx,
// and returns the call site the audit event recorded.
func auditCallSite(ctx context.Context, t *testing.T, meta mcp.Meta) []string {
	t.Helper()
	store := &testAuditStore{}
	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	server.AddTool(&mcp.Tool{
		Name: chainTestTrinoQuery, InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(store))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&testAuthenticator{userInfo: &middleware.UserInfo{UserID: "script:nightly", Roles: []string{chainTestAnalyst}}},
		&testAuthorizer{persona: "data-analyst"},
		&testToolkitLookup{tools: map[string]struct{ kind, name, conn string }{
			chainTestTrinoQuery: {kind: chainTestTrino, name: chainTestProduction, conn: chainTestProdTrino},
		}},
		middleware.ToolCallConfig{Transport: chainTestStdio, AdminPersona: "admin"},
	))
	session, err := connectClientServer(ctx, server)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	params := &mcp.CallToolParams{Name: chainTestTrinoQuery, Arguments: map[string]any{}}
	params.Meta = meta
	_, err = session.CallTool(context.Background(), params)
	require.NoError(t, err)
	return waitForAuditEvents(t, store)[0].CallSite
}

// A script's call records where in the script it was made (#1907), sent in the
// request's _meta, so a run's calls are drawn on the Flow card that made them.
func TestMiddlewareChain_AScriptCallRecordsItsCallSite(t *testing.T) {
	ctx := middleware.WithSource(context.Background(), middleware.SourceScript)
	site := []string{"40:5", "31:20"}
	assert.Equal(t, site, auditCallSite(ctx, t, mcp.Meta{scriptcallsite.MetaKey: site}))
	assert.Nil(t, auditCallSite(ctx, t, nil), "a call that sends none records none")
	assert.Nil(t, auditCallSite(ctx, t, mcp.Meta{scriptcallsite.MetaKey: []string{"not a site"}}),
		"a malformed site is not recorded")
}

// Only a script's own calls carry one: any other caller's _meta says nothing
// the platform recorded.
func TestMiddlewareChain_AnotherCallersCallSiteIsIgnored(t *testing.T) {
	assert.Nil(t, auditCallSite(context.Background(), t, mcp.Meta{scriptcallsite.MetaKey: []string{"1:1"}}))
}
