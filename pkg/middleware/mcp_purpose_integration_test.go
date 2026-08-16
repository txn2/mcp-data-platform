package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// Integration coverage for the purpose argument (#1317) on the REAL assembled
// chain: the tools/list schema decorator, the tool-call middleware carrying both
// the session-handle and purpose resolvers, and the audit middleware that
// records what the purpose resolver stripped. Every assertion here is an
// end-to-end fact — what the client sees on tools/list, what the handler
// receives, and what reaches the audit store — not a hand-built input to one
// function.

// pQueryInput is the trino_query handler's argument shape. The purpose argument
// is deliberately NOT a field: proving the handler still decodes its arguments
// is how this test shows the platform argument was stripped before the SDK
// validated them.
type pQueryInput struct {
	SQL string `json:"sql,omitempty"`
}

// pKindLookup resolves each tool's toolkit kind, so kind:mcp gating of a
// gateway-proxied tool is exercised through the real registry contract.
type pKindLookup map[string]string

func (l pKindLookup) GetToolkitForTool(toolName string) registry.ToolkitMatch {
	kind, ok := l[toolName]
	if !ok {
		return registry.ToolkitMatch{}
	}
	return registry.ToolkitMatch{Kind: kind, Name: "prod", Connection: "primary", Found: true}
}

// pHarness bundles the assembled server with what a test asserts against.
type pHarness struct {
	server *mcp.Server
	audit  *recordingAuditLogger
	// sawArgs records the arguments each handler actually received, so a test
	// can prove the purpose never reached the tool.
	sawArgs chan map[string]any
}

type purposeServerOpts struct {
	require bool
}

// purposeServer wires platform_info (which mints a handle), search, trino_query,
// and a gateway-proxied tool behind the real middleware chain: the purpose
// schema decorator outermost, then tool-call (session-handle resolver + purpose
// resolver), then audit.
func purposeServer(t *testing.T, opts purposeServerOpts) pHarness {
	t.Helper()
	store := pkgsession.NewMemoryStore(time.Hour)
	sgStore := searchgate.NewMemoryStore(time.Hour)
	tracker := middleware.NewSessionWorkflowTracker(
		[]string{"search"}, []string{"trino_query"}, sgStore, time.Hour)
	auditLog := &recordingAuditLogger{}
	sawArgs := make(chan map[string]any, 8)

	server := mcp.NewServer(&mcp.Implementation{Name: "purpose-test", Version: "v0"}, nil)

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
				Content: []mcp.Content{&mcp.TextContent{Text: handle}},
			}, nil, nil
		})

	record := func(req *mcp.CallToolRequest) {
		args := map[string]any{}
		if req != nil && req.Params != nil {
			_ = json.Unmarshal(req.Params.Arguments, &args)
		}
		select {
		case sawArgs <- args:
		default:
		}
	}
	okHandler := func(_ context.Context, req *mcp.CallToolRequest, _ pQueryInput) (*mcp.CallToolResult, any, error) {
		record(req)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: "search", Description: "discover"}, okHandler)
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query"}, okHandler)
	mcp.AddTool(server, &mcp.Tool{Name: "vendor__list_contacts", Description: "proxied"}, okHandler)

	lookup := pKindLookup{
		shInitTool:              "platform",
		"search":                "search",
		"trino_query":           "trino",
		"vendor__list_contacts": "mcp",
	}
	purpose := middleware.NewPurposeResolver(middleware.PurposeConfig{
		Enabled: true,
		Require: opts.require,
		Lookup:  lookup,
	})

	// Chain, innermost added first (last-added runs first).
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(auditLog))
	server.AddReceivingMiddleware(middleware.MCPWorkflowGateMiddleware(tracker))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "user-1", Email: "analyst@example.com", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		lookup,
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
			PurposeResolver: purpose,
		},
	))
	server.AddReceivingMiddleware(middleware.MCPPurposeSchemaMiddleware(purpose))
	return pHarness{server: server, audit: auditLog, sawArgs: sawArgs}
}

// connectPurpose opens a client session, tagging the server connection context
// with a caller source when the harness options ask for one.
func connectPurpose(ctx context.Context, t *testing.T, h pHarness, source string) *mcp.ClientSession {
	t.Helper()
	if source != "" {
		ctx = middleware.WithSource(ctx, source)
	}
	return mustConnect(ctx, t, h.server)
}

// TestIntegration_Purpose_AdvertisedOnGatedToolsOnly proves the tools/list
// decorator through the real chain: a client sees the purpose property on the
// gated tools' input schemas and not on the orientation tool.
func TestIntegration_Purpose_AdvertisedOnGatedToolsOnly(t *testing.T) {
	ctx := context.Background()
	h := purposeServer(t, purposeServerOpts{require: true})
	sess := connectPurpose(ctx, t, h, "")
	defer func() { _ = sess.Close() }()

	tools, err := sess.ListTools(ctx, nil)
	require.NoError(t, err)

	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	require.Contains(t, byName, "trino_query")
	require.Contains(t, byName, shInitTool)
	require.Contains(t, byName, "vendor__list_contacts")

	assert.True(t, toolSchemaHasProperty(t, byName["trino_query"], "purpose"),
		"a data-access tool advertises purpose")
	assert.True(t, toolSchemaHasProperty(t, byName["vendor__list_contacts"], "purpose"),
		"a gateway-proxied tool is gated by kind:mcp")
	assert.False(t, toolSchemaHasProperty(t, byName[shInitTool], "purpose"),
		"platform_info is orientation, not data access")
}

// TestIntegration_Purpose_RefusesThreadedCallWithout proves the enforcement path
// end to end: a handle-threading agent that omits purpose on a gated tool is
// refused with PURPOSE_REQUIRED before the handler runs.
func TestIntegration_Purpose_RefusesThreadedCallWithout(t *testing.T) {
	ctx := context.Background()
	h := purposeServer(t, purposeServerOpts{require: true})
	sess := connectPurpose(ctx, t, h, "")
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"session_id": handle},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "a gated call with a handle and no purpose must be refused")
	assert.Equal(t, middleware.CodePurposeRequired, clientErrCode(t, res))

	select {
	case args := <-h.sawArgs:
		t.Fatalf("the handler ran despite the refusal, with %v", args)
	default:
	}
}

// TestIntegration_Purpose_StrippedAndAudited proves the whole point of the
// feature: a stated purpose reaches the audit row and never reaches the tool.
func TestIntegration_Purpose_StrippedAndAudited(t *testing.T) {
	ctx := context.Background()
	h := purposeServer(t, purposeServerOpts{require: true})
	sess := connectPurpose(ctx, t, h, "")
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)
	const stated = "Checking whether order volume fell in the western region for the board deck."

	// search first, so the search-first gate lets the query through.
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"session_id": handle, "purpose": "Finding the orders table."},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "search with a purpose must succeed: %v", res)

	res, err = sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "trino_query",
		Arguments: map[string]any{
			"sql": "SELECT 1", "session_id": handle, "purpose": stated,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "query with a purpose must succeed: %v", res)

	// The handler saw its own argument and neither platform argument.
	var queryArgs map[string]any
	for {
		args := <-h.sawArgs
		if _, ok := args["sql"]; ok {
			queryArgs = args
			break
		}
	}
	assert.Equal(t, "SELECT 1", queryArgs["sql"])
	assert.NotContains(t, queryArgs, "purpose", "the tool must never receive the purpose")
	assert.NotContains(t, queryArgs, "session_id", "the tool must never receive the handle")

	event, ok := waitForAuditEvent(h.audit, "trino_query", 2*time.Second)
	require.True(t, ok, "expected an audit row for the query")
	assert.Equal(t, stated, event.Purpose, "the stated purpose is recorded on the audit row")
	assert.NotContains(t, event.Parameters, "purpose",
		"purpose lives in its own field, not among the recorded arguments")
	assert.Equal(t, handle, event.SessionID)
}

// TestIntegration_Purpose_SessionAdoptedCallExempt proves the #1040 shape stays
// working: an MCP App's sandboxed call threads no handle, is adopted onto the
// caller's established session, and is NOT refused for stating no purpose.
func TestIntegration_Purpose_SessionAdoptedCallExempt(t *testing.T) {
	ctx := context.Background()
	h := purposeServer(t, purposeServerOpts{require: true})
	sess := connectPurpose(ctx, t, h, "")
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "search"})
	require.NoError(t, err)
	require.False(t, res.IsError, "an adopted, handle-less call must not be refused for purpose: %v", res)

	event, ok := waitForAuditEvent(h.audit, "search", 2*time.Second)
	require.True(t, ok)
	assert.Empty(t, event.Purpose, "a caller that cannot state a purpose records none")
	assert.Equal(t, handle, event.SessionID, "it is still scoped to the caller's own session")
}

// TestIntegration_Purpose_IsolatedPortalRunExempt proves an admin-source run —
// the portal tool runner, whose session is a server-minted dpp_ id it could
// never have threaded — executes a gated tool without stating a purpose.
func TestIntegration_Purpose_IsolatedPortalRunExempt(t *testing.T) {
	ctx := context.Background()
	h := purposeServer(t, purposeServerOpts{require: true})
	sess := connectPurpose(ctx, t, h, middleware.SourceAdmin)
	defer func() { _ = sess.Close() }()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT 1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "a portal run must execute without a purpose: %v", res)

	event, ok := waitForAuditEvent(h.audit, "trino_query", 2*time.Second)
	require.True(t, ok)
	assert.Empty(t, event.Purpose)
	assert.True(t, pkgsession.IsRunID(event.SessionID),
		"the run carries a server-minted dpp_ id, which is why it could not thread one")
}

// TestIntegration_Purpose_RequireOffRecordsWithoutRefusing proves the
// record-only deployment: with require off, a gated call missing a purpose runs,
// and one carrying a purpose still has it stripped and recorded.
func TestIntegration_Purpose_RequireOffRecordsWithoutRefusing(t *testing.T) {
	ctx := context.Background()
	h := purposeServer(t, purposeServerOpts{require: false})
	sess := connectPurpose(ctx, t, h, "")
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"session_id": handle},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "require off must never refuse: %v", res)

	const stated = "Sizing the backfill after Tuesday's ingestion gap."
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "trino_query",
		Arguments: map[string]any{
			"sql": "SELECT 1", "session_id": handle, "purpose": stated,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res)

	event, ok := waitForAuditEvent(h.audit, "trino_query", 2*time.Second)
	require.True(t, ok)
	assert.Equal(t, stated, event.Purpose, "a stated purpose is recorded even when it is optional")

	searchEvent, ok := waitForAuditEvent(h.audit, "search", 2*time.Second)
	require.True(t, ok)
	assert.Empty(t, searchEvent.Purpose)
}
