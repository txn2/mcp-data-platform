package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// erroringAuthenticator fails authentication, exercising the dispatch-layer
// categorized error end to end.
type erroringAuthenticator struct{}

func (erroringAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return nil, errors.New("token expired")
}

// errorContractServer wires a real mcp.Server with the production-ordered chain
// (auth/authz outermost, audit, error-contract inner, handler) and the given
// leaf tool, then returns a connected in-memory client session plus the audit
// store. This is the real assembled system the contract must hold across.
func errorContractServer(t *testing.T, auth middleware.Authenticator, tool *mcp.Tool,
	handler func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error),
) (*mcp.ClientSession, *testAuditStore) {
	t.Helper()
	auditStore := &testAuditStore{}
	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	server.AddTool(tool, handler)

	// Inner-first: error-contract normalizes the handler result, audit observes
	// the normalized category, auth/authz (outermost) sets PlatformContext.
	server.AddReceivingMiddleware(middleware.MCPErrorContractMiddleware())
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditStore))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		auth, &testAuthorizer{persona: "analyst"},
		&testToolkitLookup{tools: map[string]struct{ kind, name, conn string }{
			tool.Name: {kind: "test", name: "test", conn: ""},
		}},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

	session, err := connectClientServer(context.Background(), server)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, auditStore
}

// clientErrorEnvelope extracts the structured error object as it arrives at the
// client over the wire (JSON-decoded into a generic map), proving the contract
// is serialized to the agent, not just held in process.
func clientErrorEnvelope(t *testing.T, r *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.True(t, r.IsError, "expected an error result")
	sc, ok := r.StructuredContent.(map[string]any)
	require.True(t, ok, "structuredContent must round-trip as a map over the wire")
	e, ok := sc["error"].(map[string]any)
	require.True(t, ok, "structuredContent.error must be present")
	return e
}

func okAuth() middleware.Authenticator {
	return &testAuthenticator{userInfo: &middleware.UserInfo{UserID: "u1", Email: "u@example.com", Roles: []string{"analyst"}}}
}

// TestIntegration_ErrorContract_BareToolkitError proves that a bare IsError
// result from an un-upgraded handler is normalized into the structured contract
// that reaches the client, and that the audit store observes the category end to
// end through the real middleware chain.
func TestIntegration_ErrorContract_BareToolkitError(t *testing.T) {
	tool := &mcp.Tool{Name: "manage_artifact", InputSchema: json.RawMessage(`{"type":"object"}`)}
	session, auditStore := errorContractServer(t, okAuth(), tool,
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "asset not found"}}}, nil
		})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "manage_artifact"})
	require.NoError(t, err)

	e := clientErrorEnvelope(t, res)
	assert.Equal(t, "tool_error", e["category"], "uncategorized error is uniformly self-describing, not opaque")
	assert.NotEmpty(t, e["code"])
	assert.Equal(t, "asset not found", e["message"], "original message preserved for the agent")
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "asset not found")

	events := waitForAuditEvents(t, auditStore)
	assert.Equal(t, "tool_error", events[0].ErrorCategory, "audit observes the normalized category")
	assert.False(t, events[0].Success)
}

// TestIntegration_ErrorContract_PanicBecomesInternal proves a panicking handler
// surfaces as a categorized internal error to the client rather than a dropped
// connection.
func TestIntegration_ErrorContract_PanicBecomesInternal(t *testing.T) {
	tool := &mcp.Tool{Name: "trino_query", InputSchema: json.RawMessage(`{"type":"object"}`)}
	session, _ := errorContractServer(t, okAuth(), tool,
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			panic("handler bug")
		})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "trino_query"})
	require.NoError(t, err, "a handler panic must not surface as a transport error")
	e := clientErrorEnvelope(t, res)
	assert.Equal(t, "internal", e["category"])
	assert.Equal(t, "internal_error", e["code"])
}

// TestIntegration_ErrorContract_AuthFailureCategorized proves a dispatch-layer
// authentication failure reaches the client with a precise code and category.
func TestIntegration_ErrorContract_AuthFailureCategorized(t *testing.T) {
	tool := &mcp.Tool{Name: "trino_query", InputSchema: json.RawMessage(`{"type":"object"}`)}
	session, _ := errorContractServer(t, erroringAuthenticator{}, tool,
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Error("handler must not run when authentication fails")
			return nil, errors.New("handler should not run")
		})

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "trino_query"})
	require.NoError(t, err)
	e := clientErrorEnvelope(t, res)
	assert.Equal(t, "unauthenticated", e["code"])
	assert.Equal(t, "authentication_failed", e["category"])
	assert.NotEmpty(t, e["hint"], "auth failure must carry a corrective hint")
}
