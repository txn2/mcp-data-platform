package middleware_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// portalHarness bundles the assembled server plus the stores/loggers a portal
// isolation test asserts against (issue #859).
type portalHarness struct {
	server     *mcp.Server
	audit      *recordingAuditLogger
	sgStore    searchgate.Store
	provenance *middleware.ProvenanceTracker
	adminID    string
}

// portalServer wires the real assembled middleware chain the way the platform
// does — provenance, audit, the search-first workflow gate, and the tool-call
// middleware with a REQUIRED session-handle resolver and the workflow tracker —
// then registers a search tool and a trino_query (gated) tool. The authenticator
// stands in for an authenticated admin, matching the portal tool runner, which
// authenticates as the operating admin.
func portalServer(t *testing.T) portalHarness {
	t.Helper()
	const adminID = "admin-1"

	handleStore := pkgsession.NewMemoryStore(time.Hour)
	sgStore := searchgate.NewMemoryStore(time.Hour)
	tracker := middleware.NewSessionWorkflowTracker(
		[]string{"search"}, []string{"trino_query"}, sgStore, time.Hour)
	auditLog := &recordingAuditLogger{}
	provenance := middleware.NewProvenanceTracker()

	server := mcp.NewServer(&mcp.Implementation{Name: "portal-session-test", Version: "v0"}, nil)

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
		&fakeAuthn{user: &middleware.UserInfo{UserID: adminID, Email: "admin@example.com", Roles: []string{"admin"}}},
		&fakeAuthz{persona: "admin"},
		&fakeLookup{kind: "trino", name: "prod", conn: "primary"},
		middleware.ToolCallConfig{
			Transport:       "http",
			AdminPersona:    "admin",
			WorkflowTracker: tracker,
			SessionResolver: middleware.NewSessionResolver(handleStore, middleware.SessionResolverConfig{
				Enabled:  true,
				Require:  true,
				TTL:      time.Hour,
				InitTool: "platform_info",
			}),
		},
	))
	return portalHarness{server: server, audit: auditLog, sgStore: sgStore, provenance: provenance, adminID: adminID}
}

// connectPortal connects to the assembled server with a connection context that
// carries the admin source, exactly as pkg/admin's connectInternalSession does
// for a portal-initiated tool run. MCPToolCallMiddleware mints the portal
// session id itself (issue #859), so the test discovers the minted id from the
// audit row rather than choosing it.
func connectPortal(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	pctx := middleware.WithSource(ctx, middleware.SourceAdmin)
	return mustConnect(pctx, t, server)
}

// TestIntegration_PortalRun_QueryExecutesWithoutSearchAndIsAttributed proves the
// two portal-facing acceptance criteria of issue #859 through the real wired
// chain: a portal replay of a gated query tool (1) executes even though no
// search was performed and no session handle was minted (the search-first and
// SESSION_REQUIRED gates must not block a portal run), and (2) is attributed to
// a distinct, recognizable portal session id in the audit row, never empty.
func TestIntegration_PortalRun_QueryExecutesWithoutSearchAndIsAttributed(t *testing.T) {
	ctx := context.Background()
	h := portalServer(t)
	sess := connectPortal(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	// A gated query tool, no prior search, no session_id handle. A real MCP
	// agent would be refused (SESSION_REQUIRED, then SEARCH_REQUIRED); a portal
	// run must execute.
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "portal query replay must execute without a prior search or handle: %v", res)

	ev, ok := waitForAuditEvent(h.audit, "trino_query", 2*time.Second)
	require.True(t, ok, "expected an audit row for the portal trino_query run")
	assert.NotEmpty(t, ev.SessionID, "portal run must record a non-empty session id")
	assert.True(t, strings.HasPrefix(ev.SessionID, pkgsession.PortalSessionPrefix),
		"portal run's audit session id must carry the portal prefix, got %q", ev.SessionID)

	// The per-request portal id must NOT accumulate in the provenance tracker:
	// it is never harvested, and the tracker's only pruner is Harvest, so
	// recording it would leak one map entry per portal run. Harvest returns
	// empty because the portal run was skipped at record time (issue #859).
	assert.Empty(t, h.provenance.Harvest(ev.SessionID),
		"portal run must not record provenance (unbounded-growth leak guard)")
}

// TestIntegration_PortalRun_DoesNotAdvanceOperatorGateState proves the isolation
// criterion of issue #859: a portal discovery call (search) must record its
// discovery under the isolated portal session scope, NOT the operating admin's
// user scope. Otherwise a portal run would advance the operator's OWN
// agent-session search-first gate, and a query the operator then issues from
// their agent session would be falsely allowed through.
func TestIntegration_PortalRun_DoesNotAdvanceOperatorGateState(t *testing.T) {
	ctx := context.Background()
	h := portalServer(t)
	sess := connectPortal(ctx, t, h.server)
	defer func() { _ = sess.Close() }()

	// A portal search run: it is a discovery tool, so it records discovery, but
	// it must land in the portal scope, not the operator's user scope.
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "search"})
	require.NoError(t, err)
	require.False(t, res.IsError, "portal search must execute: %v", res)

	// Recover the minted portal id from the audit row so we can assert the scope.
	ev, ok := waitForAuditEvent(h.audit, "search", 2*time.Second)
	require.True(t, ok, "expected an audit row for the portal search run")
	require.True(t, strings.HasPrefix(ev.SessionID, pkgsession.PortalSessionPrefix),
		"portal search must carry a portal session id, got %q", ev.SessionID)

	// The portal run recorded discovery under session:<portalID> ...
	discoveredPortal, err := h.sgStore.HasDiscovered(ctx, "session:"+ev.SessionID)
	require.NoError(t, err)
	assert.True(t, discoveredPortal,
		"portal search must record discovery under its own portal session scope")

	// ... and NOT under the operator's user scope, so it cannot open the
	// operator's own agent-session search-first gate.
	discoveredUser, err := h.sgStore.HasDiscovered(ctx, "user:"+h.adminID)
	require.NoError(t, err)
	assert.False(t, discoveredUser,
		"a portal run must not advance the operator's user-scoped agent-session gate state")
}
