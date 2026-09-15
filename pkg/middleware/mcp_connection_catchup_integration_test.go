package middleware_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Integration coverage for the connection catch-up hook (#1757) on the REAL
// assembled chain: a tool call crosses the tool-call middleware, which resolves
// the toolkit kind and the connection the call names and hands both to the
// catch-up before the handler runs. Every assertion is what the catch-up
// actually received from a client's tools/call, not a hand-built input.
//
// Why it has to be here rather than in the catch-up's own tests: the catch-up
// is correct in isolation and was still unreachable if the chain never called
// it, or called it with the toolkit's default connection instead of the one the
// caller named. That is the failure the #1746 acceptance run found two packages
// away from the code that was right.

// catchUpRecorder records what the middleware asked to be taken on.
type catchUpRecorder struct {
	mu    sync.Mutex
	seen  []string
	calls int
}

func (r *catchUpRecorder) TakeOn(_ context.Context, kind, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.seen = append(r.seen, kind+"/"+name)
}

func (r *catchUpRecorder) taken() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

// ccQueryInput is the handler's argument shape: a connection-routed tool takes
// the connection by name, which is what the catch-up must be handed.
type ccQueryInput struct {
	Connection string `json:"connection,omitempty"`
	SQL        string `json:"sql,omitempty"`
}

// ccLookup resolves each tool's toolkit, reporting the toolkit's DEFAULT
// connection the way the real registry does for a multi-connection kind.
type ccLookup map[string]registry.ToolkitMatch

func (l ccLookup) GetToolkitForTool(toolName string) registry.ToolkitMatch {
	return l[toolName]
}

// ccServer wires two tools behind the real tool-call middleware: one routed by
// a connection argument (trino) and one platform tool that names no connection.
func ccServer(t *testing.T, catchUp *catchUpRecorder, authorized bool) *mcp.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "catchup-test", Version: "v0"}, nil)

	okHandler := func(_ context.Context, _ *mcp.CallToolRequest, _ ccQueryInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query"}, okHandler)
	mcp.AddTool(server, &mcp.Tool{Name: "platform_info", Description: "info"}, okHandler)

	lookup := ccLookup{
		"trino_query":   {Kind: "trino", Name: "prod", Connection: "declared", Found: true},
		"platform_info": {},
	}
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "user-1", Roles: []string{"analyst"}}},
		&ccAuthz{allow: authorized},
		lookup,
		middleware.ToolCallConfig{
			Transport:         "http",
			AdminPersona:      "admin",
			ConnectionCatchUp: catchUp,
		},
	))
	return server
}

// ccAuthz allows or denies every call, to hold the ordering contract.
type ccAuthz struct{ allow bool }

func (a *ccAuthz) IsAuthorized(_ context.Context, _ string, _ []string, _, _ string) (allowed bool, persona, reason string) {
	if !a.allow {
		return false, "analyst", "the analyst persona does not reach this connection"
	}
	return true, "analyst", ""
}

// ccCall opens a session against server and calls one tool.
func ccCall(t *testing.T, server *mcp.Server, tool string, args map[string]any) {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close() //nolint:errcheck // test cleanup

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close() //nolint:errcheck // test cleanup

	raw, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: json.RawMessage(raw)})
	require.NoError(t, err)
}

// TestCatchUp_TakesOnTheConnectionTheCallerNamed is the defect: the caller names
// a connection saved through another replica, and the chain must hand THAT name
// to the catch-up — not the toolkit's default, which is what the registry
// reports for a multi-connection kind.
func TestCatchUp_TakesOnTheConnectionTheCallerNamed(t *testing.T) {
	catchUp := &catchUpRecorder{}

	ccCall(t, ccServer(t, catchUp, true), "trino_query",
		map[string]any{"connection": "saved-elsewhere", "sql": "SELECT 1"})

	assert.Equal(t, []string{"trino/saved-elsewhere"}, catchUp.taken(),
		"the connection the caller named, under the kind that serves it")
}

// TestCatchUp_ACallNamingNoConnectionTakesOnTheDefault: a connection-routed tool
// called without one is answered by the toolkit's default, which is equally a
// connection this replica may not serve yet.
func TestCatchUp_ACallNamingNoConnectionTakesOnTheDefault(t *testing.T) {
	catchUp := &catchUpRecorder{}

	ccCall(t, ccServer(t, catchUp, true), "trino_query", map[string]any{"sql": "SELECT 1"})

	assert.Equal(t, []string{"trino/declared"}, catchUp.taken())
}

// TestCatchUp_APlatformToolNamesNoConnection: the hook runs on every tool call,
// so a tool belonging to no connection kind must reach it as a no-op rather than
// as a lookup for the empty name.
func TestCatchUp_APlatformToolNamesNoConnection(t *testing.T) {
	catchUp := &catchUpRecorder{}

	ccCall(t, ccServer(t, catchUp, true), "platform_info", map[string]any{})

	assert.Equal(t, []string{"/"}, catchUp.taken(),
		"the hook is handed the empty kind and name, which it declines")
}

// TestCatchUp_AnUnauthorizedCallNeverReachesTheStore holds the ordering
// contract: a caller who may not reach a connection must not be able to make
// this process read the store for it.
func TestCatchUp_AnUnauthorizedCallNeverReachesTheStore(t *testing.T) {
	catchUp := &catchUpRecorder{}

	ccCall(t, ccServer(t, catchUp, false), "trino_query",
		map[string]any{"connection": "saved-elsewhere", "sql": "SELECT 1"})

	assert.Empty(t, catchUp.taken(), "authorization runs first")
}

// TestCatchUp_NoHookLeavesTheChainUnchanged covers the deployment that wires
// none: a call is answered from the connections this process holds.
func TestCatchUp_NoHookLeavesTheChainUnchanged(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "catchup-test", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ ccQueryInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "user-1"}},
		&ccAuthz{allow: true},
		ccLookup{"trino_query": {Kind: "trino", Name: "prod", Connection: "declared", Found: true}},
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin"},
	))

	ccCall(t, server, "trino_query", map[string]any{"sql": "SELECT 1"})
}
