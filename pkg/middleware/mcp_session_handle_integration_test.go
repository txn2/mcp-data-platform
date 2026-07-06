package middleware_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

const shInitTool = "platform_info"

type shSQLInput struct {
	SQL string `json:"sql,omitempty"`
}

// shHarness bundles the assembled server and the stores/loggers a test asserts
// against.
type shHarness struct {
	server     *mcp.Server
	audit      *recordingAuditLogger
	store      pkgsession.Store
	provenance *middleware.ProvenanceTracker
}

// sessionHandleServer wires the real assembled middleware chain (tool-call
// middleware with the session-handle resolver, the search-first workflow gate,
// and audit) plus the schema-injection list decorator, then registers a
// platform_info tool that mints handles into the shared store, a search tool,
// and a trino_query tool.
func sessionHandleServer(t *testing.T) shHarness {
	t.Helper()
	store := pkgsession.NewMemoryStore(time.Hour)
	sgStore := searchgate.NewMemoryStore(time.Hour)
	tracker := middleware.NewSessionWorkflowTracker(
		[]string{"search"}, []string{"trino_query"}, sgStore, time.Hour)
	auditLog := &recordingAuditLogger{}
	provenance := middleware.NewProvenanceTracker()

	server := mcp.NewServer(&mcp.Implementation{Name: "session-handle-test", Version: "v0"}, nil)

	// platform_info mints a handle owned by the caller and returns it, mirroring
	// the real info_tool mint.
	mcp.AddTool(server, &mcp.Tool{Name: shInitTool, Description: "init"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			handle, err := pkgsession.GenerateHandle()
			if err != nil {
				return nil, nil, fmt.Errorf("mint handle: %w", err)
			}
			uid := ""
			if pc := middleware.GetPlatformContext(ctx); pc != nil {
				uid = pc.UserID
			}
			now := time.Now()
			if cerr := store.Create(ctx, &pkgsession.Session{
				ID: handle, UserID: uid, CreatedAt: now, LastActiveAt: now,
				ExpiresAt: now.Add(time.Hour),
				State:     map[string]any{pkgsession.StateKeyMintedBy: pkgsession.MintedByPlatformInfo},
			}); cerr != nil {
				return nil, nil, fmt.Errorf("persist handle: %w", cerr)
			}
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: handle}},
				StructuredContent: map[string]any{"session_id": handle},
			}, nil, nil
		})

	okHandler := func(_ context.Context, _ *mcp.CallToolRequest, _ shSQLInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: "search", Description: "discover"}, okHandler)
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query"}, okHandler)

	// Chain, innermost added first (last-added runs first).
	server.AddReceivingMiddleware(middleware.MCPProvenanceMiddleware(provenance))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditLog))
	server.AddReceivingMiddleware(middleware.MCPWorkflowGateMiddleware(tracker))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "user-1", Email: "analyst@example.com", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "trino", name: "prod", conn: "primary"},
		middleware.ToolCallConfig{
			Transport:       "http",
			AdminPersona:    "admin",
			WorkflowTracker: tracker,
			SessionResolver: middleware.NewSessionResolver(store, middleware.SessionResolverConfig{
				Enabled:  true,
				Require:  true,
				TTL:      time.Hour,
				InitTool: shInitTool,
			}),
		},
	))
	server.AddReceivingMiddleware(middleware.MCPSessionHandleSchemaMiddleware(shInitTool))
	return shHarness{server: server, audit: auditLog, store: store, provenance: provenance}
}

// mintViaPlatformInfo calls platform_info and returns the minted handle.
func mintViaPlatformInfo(ctx context.Context, t *testing.T, sess *mcp.ClientSession) string {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: shInitTool})
	require.NoError(t, err)
	require.False(t, res.IsError, "platform_info must succeed")
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.True(t, pkgsession.IsHandle(tc.Text), "platform_info must return a dps_ handle")
	return tc.Text
}

// TestIntegration_SessionHandle_ThreadedFlow proves the assembled chain end to
// end: platform_info mints a handle, the handle threads through the resolver,
// the search-first gate keys on it, and the audit rows share it (acceptance
// criteria 4 and 7).
func TestIntegration_SessionHandle_ThreadedFlow(t *testing.T) {
	ctx := context.Background()
	h := sessionHandleServer(t)
	sess := mustConnect(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)

	// trino_query BEFORE search under the handle -> SEARCH_REQUIRED (init gate
	// passed, discovery gate not yet).
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1", "session_id": handle},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "query before search must be refused")
	assert.Equal(t, middleware.CodeSearchRequired, clientErrCode(t, res))

	// search under the handle opens the gate.
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"session_id": handle},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "search must succeed: %v", res)

	// trino_query AFTER search executes.
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1", "session_id": handle},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "query after search must execute: %v", res)

	// Criterion 7: audit rows for search and trino_query share the minted handle.
	searchEvent, ok := waitForAuditEvent(h.audit, "search", 2*time.Second)
	require.True(t, ok, "expected an audit row for search")
	assert.Equal(t, handle, searchEvent.SessionID)
	queryEvent, ok := waitForAuditEvent(h.audit, "trino_query", 2*time.Second)
	require.True(t, ok, "expected an audit row for trino_query")
	assert.Equal(t, handle, queryEvent.SessionID)

	// Criterion 6: provenance is keyed by the threaded handle, so the calls are
	// recorded under it (not dropped as they would be under an empty session on
	// a headerless transport).
	calls := h.provenance.Harvest(handle)
	tools := make([]string, 0, len(calls))
	for _, c := range calls {
		tools = append(tools, c.ToolName)
	}
	assert.Contains(t, tools, "search")
	assert.Contains(t, tools, "trino_query")
}

// TestIntegration_SessionHandle_NoHandleRefused is issue #800's acceptance
// criterion through the real wired chain: with handles required, a gated query
// carrying no session_id handle is refused with SESSION_REQUIRED and never
// reaches the handler. (The unit test TestSessionResolver_TransportSessionDoesNotSatisfyRequire
// covers the companion case where a non-empty transport session is present but
// still does not satisfy the requirement.)
func TestIntegration_SessionHandle_NoHandleRefused(t *testing.T) {
	ctx := context.Background()
	h := sessionHandleServer(t)
	sess := mustConnect(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "a handle-less gated call must be refused")
	assert.Equal(t, middleware.CodeSessionRequired, clientErrCode(t, res))

	// Refused before the handler ran, so no execution audit row for trino_query.
	if e, ok := waitForAuditEvent(h.audit, "trino_query", 300*time.Millisecond); ok {
		t.Fatalf("a refused call must not produce a handler audit row: %+v", e)
	}
}

// TestIntegration_SessionHandle_ExpiredRejectedBeforeHandler proves an unknown
// handle is refused with SESSION_EXPIRED before the handler or audit run
// (acceptance criterion 3's mechanism: refused before execution, no handler
// audit row).
func TestIntegration_SessionHandle_ExpiredRejectedBeforeHandler(t *testing.T) {
	ctx := context.Background()
	h := sessionHandleServer(t)
	sess := mustConnect(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1", "session_id": "dps_unknownhandle"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Equal(t, middleware.CodeSessionExpired, clientErrCode(t, res))

	// No audit row for a handler execution of trino_query.
	if e, ok := waitForAuditEvent(h.audit, "trino_query", 300*time.Millisecond); ok {
		t.Fatalf("a refused call must not produce a handler audit row: %+v", e)
	}
}

// TestIntegration_SessionHandle_IdentityMismatch is acceptance criterion 5: a
// handle minted under one identity, presented by another, is rejected.
func TestIntegration_SessionHandle_IdentityMismatch(t *testing.T) {
	ctx := context.Background()
	h := sessionHandleServer(t)
	sess := mustConnect(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	// A handle owned by a DIFFERENT identity than the fake authenticator's user-1.
	other, err := pkgsession.GenerateHandle()
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, h.store.Create(ctx, &pkgsession.Session{
		ID: other, UserID: "user-2", CreatedAt: now, LastActiveAt: now, ExpiresAt: now.Add(time.Hour),
	}))

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1", "session_id": other},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "cross-identity handle must be rejected")
	assert.Equal(t, middleware.CodeSessionExpired, clientErrCode(t, res))
}

// TestIntegration_SessionHandle_ListSchemaInjection is acceptance criterion 2:
// tools/list advertises session_id on every tool except platform_info.
func TestIntegration_SessionHandle_ListSchemaInjection(t *testing.T) {
	ctx := context.Background()
	h := sessionHandleServer(t)
	sess := mustConnect(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		hasSessionID := toolSchemaHasProperty(t, tool, "session_id")
		if tool.Name == shInitTool {
			assert.False(t, hasSessionID, "platform_info must NOT advertise session_id")
		} else {
			assert.True(t, hasSessionID, "%s must advertise session_id", tool.Name)
		}
	}
	require.True(t, seen[shInitTool] && seen["search"] && seen["trino_query"], "all tools listed")
}

// clientErrCode extracts the structured error code from a tool result as it
// arrives over the wire.
func clientErrCode(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	sc, ok := r.StructuredContent.(map[string]any)
	require.True(t, ok, "structuredContent must round-trip as a map")
	e, ok := sc["error"].(map[string]any)
	require.True(t, ok, "structuredContent.error must be present")
	code, _ := e["code"].(string)
	return code
}

// toolSchemaHasProperty reports whether the tool's input schema (as received by
// the client, a map[string]any) declares the named property.
func toolSchemaHasProperty(t *testing.T, tool *mcp.Tool, name string) bool {
	t.Helper()
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		return false
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, present := props[name]
	return present
}
