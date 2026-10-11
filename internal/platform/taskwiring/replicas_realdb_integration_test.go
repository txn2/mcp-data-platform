//go:build integration

package taskwiring

// The assembled Tasks extension across two replicas sharing one PostgreSQL
// (#2009): a real MCP server per replica with the audit layer outside the
// tasks layer, as the platform's chain has them, and a raw JSON-RPC client on
// each, so every assertion reads what a client reads. A call started on one
// replica is followed, finished and cancelled through the other.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/internal/platform/calltask"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// auditLog collects the audit rows a replica writes, handing each to a waiter.
type auditLog struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
	ch     chan middleware.AuditEvent
}

func newAuditLog() *auditLog { return &auditLog{ch: make(chan middleware.AuditEvent, 16)} }

func (a *auditLog) Log(_ context.Context, e middleware.AuditEvent) error {
	a.mu.Lock()
	a.events = append(a.events, e)
	a.mu.Unlock()
	a.ch <- e
	return nil
}

// exportTool stands in for trino_export: it blocks until released, or until
// its context ends.
type exportTool struct {
	started chan struct{}
	release chan struct{}
}

// replica is one platform process: its server, and a client per caller.
type replica struct {
	audit *auditLog
	h     *Handle
	srv   *mcp.Server
}

// identify stands in for MCPToolCallMiddleware: it puts the caller the
// client names in _meta on the tools/call's PlatformContext.
func identify(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/call" {
			pc := middleware.GetPlatformContext(ctx)
			if pc == nil {
				pc = middleware.NewPlatformContext("req")
				ctx = middleware.WithPlatformContext(ctx, pc)
			}
			pc.UserID = userOf(req)
			pc.ToolName = "trino_export"
		}
		return next(ctx, method, req)
	}
}

// resolve stands in for the platform's tasks/* caller resolution.
func resolve(ctx context.Context, req mcp.Request) context.Context {
	user := userOf(req)
	return middleware.WithPlatformContext(ctx, &middleware.PlatformContext{UserID: user, IsAdmin: user == "root"})
}

func userOf(req mcp.Request) string {
	if req == nil || req.GetParams() == nil {
		return ""
	}
	u, _ := req.GetParams().GetMeta()["test/user"].(string)
	return u
}

func buildReplica(t *testing.T, store calltask.Store, tool *exportTool) *replica {
	t.Helper()
	runner := calltask.New(calltask.Options{Store: store, Heartbeat: 10 * time.Millisecond})
	h := &Handle{sources: mcptask.NewSources(runner), runner: runner, resolve: resolve}
	srv := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	mcp.AddTool(srv, &mcp.Tool{Name: "trino_export"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		tool.started <- struct{}{}
		select {
		case <-tool.release:
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "exported 10 rows"}}}, nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	})
	require.NoError(t, h.Register(srv))
	audit := newAuditLog()
	// Registered innermost first: the tasks layer, then audit, then the
	// stand-in for authorization outermost, the order the platform's chain
	// declares.
	srv.AddReceivingMiddleware(h.Middleware())
	srv.AddReceivingMiddleware(middleware.MCPAuditMiddleware(audit))
	srv.AddReceivingMiddleware(identify)
	require.NoError(t, h.Start(context.Background()))
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	return &replica{audit: audit, h: h, srv: srv}
}

// client is a raw JSON-RPC client connected to a replica as one user.
type client struct {
	t    *testing.T
	conn mcp.Connection
	user string
	id   int64
	mu   sync.Mutex
}

func connect(t *testing.T, r *replica, user string) *client {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := r.srv.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	conn, err := ct.Connect(context.Background())
	require.NoError(t, err)
	c := &client{t: t, conn: conn, user: user}
	resp := c.raw("initialize", map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "c", "version": "1"},
	})
	require.Nil(t, resp.Error)
	require.NoError(t, conn.Write(context.Background(), &jsonrpc.Request{Method: "notifications/initialized", Params: json.RawMessage(`{}`)}))
	return c
}

func (c *client) raw(method string, params map[string]any) *jsonrpc.Response {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id++
	body, err := json.Marshal(params)
	require.NoError(c.t, err)
	id, err := jsonrpc.MakeID(float64(c.id))
	require.NoError(c.t, err)
	require.NoError(c.t, c.conn.Write(context.Background(), &jsonrpc.Request{ID: id, Method: method, Params: body}))
	msg, err := c.conn.Read(context.Background())
	require.NoError(c.t, err)
	resp, ok := msg.(*jsonrpc.Response)
	require.True(c.t, ok)
	return resp
}

// call sends method with the caller's identity and, when declaring, the
// extension in the request's own capabilities.
func (c *client) call(method string, params map[string]any, declaring bool) *jsonrpc.Response {
	meta := map[string]any{"test/user": c.user}
	if declaring {
		meta["io.modelcontextprotocol/clientCapabilities"] = map[string]any{"extensions": map[string]any{mcptask.ExtensionID: map[string]any{}}}
	}
	params["_meta"] = meta
	return c.raw(method, params)
}

func fields(t *testing.T, resp *jsonrpc.Response) map[string]any {
	t.Helper()
	require.Nil(t, resp.Error)
	var out map[string]any
	require.NoError(t, json.Unmarshal(resp.Result, &out))
	return out
}

func TestTasksAcrossReplicas_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := calltask.NewPostgresStore(db)
	toolA := &exportTool{started: make(chan struct{}, 4), release: make(chan struct{})}
	toolB := &exportTool{started: make(chan struct{}, 4), release: make(chan struct{})}
	a, b := buildReplica(t, store, toolA), buildReplica(t, store, toolB)
	janeA, janeB, bobB, rootB := connect(t, a, "jane"), connect(t, b, "jane"), connect(t, b, "bob"), connect(t, b, "root")
	exportCall := func() map[string]any { return map[string]any{"name": "trino_export", "arguments": map[string]any{}} }

	// A client without the extension is answered with the export's result,
	// as before.
	go func() { <-toolA.started; toolA.release <- struct{}{} }()
	direct := fields(t, janeA.call("tools/call", exportCall(), false))
	assert.NotEqual(t, "task", direct["resultType"])
	assert.Contains(t, direct["content"], map[string]any{"type": "text", "text": "exported 10 rows"})
	<-a.audit.ch

	// Declaring, the export is a task at once; the other replica follows it.
	handle := fields(t, janeA.call("tools/call", exportCall(), true))
	require.Equal(t, "task", handle["resultType"])
	taskID, _ := handle["taskId"].(string)
	require.Regexp(t, `^dpt_[0-9a-f]{32}$`, taskID)
	assert.Equal(t, "working", handle["status"])
	<-toolA.started
	assert.Empty(t, drain(a.audit.ch), "the call is recorded when its work ends, not when the handle goes back")

	got := fields(t, janeB.call(mcptask.MethodGet, map[string]any{"taskId": taskID}, true))
	assert.Equal(t, "complete", got["resultType"])
	assert.Equal(t, "working", got["status"])

	// Another person is told it does not exist; an administrator may read it.
	resp := bobB.call(mcptask.MethodGet, map[string]any{"taskId": taskID}, true)
	assertRPCError(t, resp, jsonrpc.CodeInvalidParams)
	assert.Equal(t, "working", fields(t, rootB.call(mcptask.MethodGet, map[string]any{"taskId": taskID}, true))["status"])

	// Without the extension declared, tasks/get is refused.
	assertRPCError(t, janeB.call(mcptask.MethodGet, map[string]any{"taskId": taskID}, false), mcptask.CodeMissingCapability)

	// The export finishes; its result is read on the other replica and the
	// audit row says what it did.
	toolA.release <- struct{}{}
	event := <-a.audit.ch
	assert.True(t, event.Success)
	assert.Equal(t, "jane", event.UserID)
	assert.Equal(t, len("exported 10 rows"), event.ResponseChars)
	done := fields(t, janeB.call(mcptask.MethodGet, map[string]any{"taskId": taskID}, true))
	assert.Equal(t, "completed", done["status"])
	result, ok := done["result"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, result["content"], map[string]any{"type": "text", "text": "exported 10 rows"})
	assert.NotNil(t, done["ttlMs"])

	// A second export is cancelled through the replica not running it: the
	// cancel reaches the running replica at its next heartbeat.
	handle = fields(t, janeA.call("tools/call", exportCall(), true))
	second, _ := handle["taskId"].(string)
	<-toolA.started
	ack := fields(t, janeB.call(mcptask.MethodCancel, map[string]any{"taskId": second}, true))
	assert.Equal(t, map[string]any{"resultType": "complete"}, ack)
	event = <-a.audit.ch
	assert.False(t, event.Success, "a cancelled export did not do its work")
	cancelled := fields(t, janeB.call(mcptask.MethodGet, map[string]any{"taskId": second}, true))
	assert.Equal(t, "cancelled", cancelled["status"])
	assert.NotContains(t, cancelled, "result")

	// tasks/update acknowledges a task the caller follows and refuses one
	// they do not.
	assert.Equal(t, map[string]any{"resultType": "complete"},
		fields(t, janeB.call(mcptask.MethodUpdate, map[string]any{"taskId": taskID, "inputResponses": map[string]any{}}, true)))
	assertRPCError(t, bobB.call(mcptask.MethodUpdate, map[string]any{"taskId": taskID, "inputResponses": map[string]any{}}, true), jsonrpc.CodeInvalidParams)
	assertRPCError(t, bobB.call(mcptask.MethodCancel, map[string]any{"taskId": taskID}, true), jsonrpc.CodeInvalidParams)
}

func assertRPCError(t *testing.T, resp *jsonrpc.Response, code int64) {
	t.Helper()
	require.NotNil(t, resp.Error)
	var wire *jsonrpc.Error
	require.True(t, errors.As(resp.Error, &wire))
	assert.Equal(t, code, wire.Code)
}

// drain returns what a channel holds now, without waiting.
func drain(ch chan middleware.AuditEvent) []middleware.AuditEvent {
	var out []middleware.AuditEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}
