package mcptask

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
)

// fakeSource holds tasks in memory under one prefix.
type fakeSource struct {
	mu       sync.Mutex
	prefix   string
	tasks    map[string]*Snapshot
	canceled []string
	err      error
}

func newFakeSource(prefix string) *fakeSource {
	return &fakeSource{prefix: prefix, tasks: map[string]*Snapshot{}}
}

func (f *fakeSource) Prefix() string { return f.prefix }

func (f *fakeSource) Get(_ context.Context, id string) (*Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.tasks[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSource) Cancel(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.tasks[id]; !ok {
		return ErrNotFound
	}
	f.canceled = append(f.canceled, id)
	return nil
}

func (f *fakeSource) put(s *Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[s.ID] = s
}

// fakeDetacher records the calls handed to it and runs them on demand.
type fakeDetacher struct {
	src  *fakeSource
	runs chan func(context.Context) (mcp.Result, error)
	err  error
}

func (d *fakeDetacher) Start(ctx context.Context, _ string, run func(context.Context) (mcp.Result, error)) (*Snapshot, error) {
	if d.err != nil {
		return nil, d.err
	}
	snap := &Snapshot{ID: "det_1", Status: StatusWorking, CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), PollInterval: time.Second}
	d.src.put(snap)
	d.runs <- func(context.Context) (mcp.Result, error) { return run(context.WithoutCancel(ctx)) }
	return snap, nil
}

// wire is a raw JSON-RPC client over an in-memory transport, so the test
// reads exactly what a client reads.
type wire struct {
	t    *testing.T
	conn mcp.Connection
	id   int64
}

func (w *wire) call(method string, params any) *jsonrpc.Response {
	w.t.Helper()
	w.id++
	raw, err := json.Marshal(params)
	require.NoError(w.t, err)
	id, err := jsonrpc.MakeID(float64(w.id))
	require.NoError(w.t, err)
	require.NoError(w.t, w.conn.Write(context.Background(), &jsonrpc.Request{ID: id, Method: method, Params: raw}))
	msg, err := w.conn.Read(context.Background())
	require.NoError(w.t, err)
	resp, ok := msg.(*jsonrpc.Response)
	require.True(w.t, ok, "a response, got %T", msg)
	return resp
}

func (w *wire) notify(method string) {
	require.NoError(w.t, w.conn.Write(context.Background(), &jsonrpc.Request{Method: method, Params: json.RawMessage(`{}`)}))
}

// declaring is the per-request _meta a client declaring the extension sends.
var declaring = map[string]any{
	metaKeyClientCapabilities: map[string]any{"extensions": map[string]any{ExtensionID: map[string]any{}}},
}

// harness starts a server whose "native" tool accepts its claim with
// "nat_1" and whose "export" tool returns a fixed result, with the Tasks
// middleware over them.
func harness(t *testing.T, cfg Config) *wire {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "1"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Extensions: map[string]any{ExtensionID: map[string]any{}}},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "native"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if c := Requested(ctx); c != nil {
			c.Accept("nat_1")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "queued"}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ran to the end"}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "export"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "exported"}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "plain"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "plain"}}}, nil, nil
	})
	require.NoError(t, Register(server, cfg.Sources))
	server.AddReceivingMiddleware(Middleware(cfg))

	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	conn, err := ct.Connect(context.Background())
	require.NoError(t, err)
	w := &wire{t: t, conn: conn}
	init := w.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "c", "version": "1"},
	})
	require.Nil(t, init.Error)
	w.notify("notifications/initialized")
	return w
}

func toolCall(name string, meta map[string]any) map[string]any {
	p := map[string]any{"name": name, "arguments": map[string]any{}}
	if meta != nil {
		p["_meta"] = meta
	}
	return p
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// A client that declares the extension and calls a Native tool is handed the
// task the tool accepted, in its seed state, at once.
func TestNativeCallIsAnsweredWithItsTask(t *testing.T) {
	src := newFakeSource("nat_")
	src.put(&Snapshot{
		ID: "nat_1", Status: StatusWorking, StatusMessage: "queued", CreatedAt: time.Unix(10, 0), UpdatedAt: time.Unix(11, 0),
		TTL: time.Hour, PollInterval: 2 * time.Second,
	})
	w := harness(t, Config{Tools: map[string]Mode{"native": Native}, Sources: NewSources(src)})

	resp := w.call("tools/call", toolCall("native", declaring))
	require.Nil(t, resp.Error)
	got := decode(t, resp.Result)
	assert.Equal(t, "task", got["resultType"])
	assert.Equal(t, "nat_1", got["taskId"])
	assert.Equal(t, "working", got["status"])
	assert.Equal(t, "queued", got["statusMessage"])
	assert.EqualValues(t, 3600000, got["ttlMs"])
	assert.EqualValues(t, 2000, got["pollIntervalMs"])
	assert.Equal(t, "1970-01-01T00:00:10Z", got["createdAt"])
	assert.NotContains(t, got, "content", "a task handle is not a tool result")
}

// A client that does not declare the extension gets the tool's own answer,
// exactly as before: the tool sees no claim.
func TestUndeclaredCallIsAnsweredDirectly(t *testing.T) {
	src := newFakeSource("nat_")
	w := harness(t, Config{Tools: map[string]Mode{"native": Native}, Sources: NewSources(src)})

	resp := w.call("tools/call", toolCall("native", nil))
	require.Nil(t, resp.Error)
	got := decode(t, resp.Result)
	assert.NotEqual(t, "task", got["resultType"])
	assert.Contains(t, string(resp.Result), "ran to the end")
}

// A tool the configuration does not name is answered directly even for a
// declaring client.
func TestUnlistedToolIsAnsweredDirectly(t *testing.T) {
	w := harness(t, Config{Tools: map[string]Mode{"native": Native}, Sources: NewSources()})
	resp := w.call("tools/call", toolCall("plain", declaring))
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), `"plain"`)
	assert.NotContains(t, string(resp.Result), `"task"`)
}

// A Native task whose seed state cannot be read is answered with the tool's
// own result, which names the work, rather than with a handle no tasks/get
// would resolve.
func TestNativeCallWithAnUnreadableTaskFallsBackToTheResult(t *testing.T) {
	src := newFakeSource("nat_")
	w := harness(t, Config{Tools: map[string]Mode{"native": Native}, Sources: NewSources(src)})
	resp := w.call("tools/call", toolCall("native", declaring))
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), "queued")
	assert.NotContains(t, string(resp.Result), `"resultType":"task"`)

	// No Source holds the accepted id at all.
	w = harness(t, Config{Tools: map[string]Mode{"native": Native}, Sources: NewSources()})
	resp = w.call("tools/call", toolCall("native", declaring))
	require.Nil(t, resp.Error)
	assert.NotContains(t, string(resp.Result), `"resultType":"task"`)
}

// A Detached tool's call is handed to the Detacher and answered with its task;
// the call runs when the Detacher runs it, through the rest of the chain.
func TestDetachedCallRunsApartFromTheRequest(t *testing.T) {
	src := newFakeSource("det_")
	d := &fakeDetacher{src: src, runs: make(chan func(context.Context) (mcp.Result, error), 1)}
	w := harness(t, Config{Tools: map[string]Mode{"export": Detached}, Sources: NewSources(src), Detacher: d})

	resp := w.call("tools/call", toolCall("export", declaring))
	require.Nil(t, resp.Error)
	got := decode(t, resp.Result)
	assert.Equal(t, "task", got["resultType"])
	assert.Equal(t, "det_1", got["taskId"])

	run := <-d.runs
	res, err := run(context.Background())
	require.NoError(t, err)
	ctr, ok := res.(*mcp.CallToolResult)
	require.True(t, ok)
	text, ok := ctr.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "exported", text.Text)
}

// A Detacher that declines or fails leaves the call answered directly.
func TestDetachedCallTheDetacherDeclinesIsAnsweredDirectly(t *testing.T) {
	for name, derr := range map[string]error{"declined": ErrNotDetached, "failed": errors.New("store down")} {
		t.Run(name, func(t *testing.T) {
			d := &fakeDetacher{err: derr}
			w := harness(t, Config{Tools: map[string]Mode{"export": Detached}, Sources: NewSources(), Detacher: d})
			resp := w.call("tools/call", toolCall("export", declaring))
			require.Nil(t, resp.Error)
			assert.Contains(t, string(resp.Result), "exported")
		})
	}
	w := harness(t, Config{Tools: map[string]Mode{"export": Detached}, Sources: NewSources()})
	resp := w.call("tools/call", toolCall("export", declaring))
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), "exported", "no Detacher, the call is answered directly")
}

// tasks/get inlines the outcome of an ended task: a completed one its result,
// a failed one its JSON-RPC error.
func TestGetInlinesTheOutcome(t *testing.T) {
	src := newFakeSource("t_")
	result, err := EncodeResult(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}, StructuredContent: map[string]any{"rows": []any{}}})
	require.NoError(t, err)
	src.put(&Snapshot{ID: "t_done", Status: StatusCompleted, Result: result, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0)})
	src.put(&Snapshot{ID: "t_bad", Status: StatusFailed, StatusMessage: "upstream gone", Error: &jsonrpc.Error{Code: -32603, Message: "upstream gone"}})
	src.put(&Snapshot{ID: "t_bare", Status: StatusFailed, StatusMessage: "no error recorded"})
	src.put(&Snapshot{ID: "t_stop", Status: StatusCancelled})
	w := harness(t, Config{Sources: NewSources(src)})

	done := decode(t, w.call(MethodGet, map[string]any{"taskId": "t_done", "_meta": declaring}).Result)
	assert.Equal(t, "complete", done["resultType"])
	assert.Equal(t, "completed", done["status"])
	assert.Nil(t, done["ttlMs"], "no TTL is an unlimited task: null")
	assert.Contains(t, done, "ttlMs")
	inlined, ok := done["result"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"rows": []any{}}, inlined["structuredContent"], "an empty list reads []")

	bad := decode(t, w.call(MethodGet, map[string]any{"taskId": "t_bad", "_meta": declaring}).Result)
	assert.Equal(t, "failed", bad["status"])
	assert.Equal(t, map[string]any{"code": float64(-32603), "message": "upstream gone"}, bad["error"])
	assert.NotContains(t, bad, "result")

	bare := decode(t, w.call(MethodGet, map[string]any{"taskId": "t_bare", "_meta": declaring}).Result)
	assert.Equal(t, map[string]any{"code": float64(-32603), "message": "no error recorded"}, bare["error"],
		"a failed task always carries an error")

	stopped := decode(t, w.call(MethodGet, map[string]any{"taskId": "t_stop", "_meta": declaring}).Result)
	assert.Equal(t, StatusCancelled, stopped["status"])
	assert.NotContains(t, stopped, "result")
	assert.NotContains(t, stopped, "error")
}

// An id no Source holds, or one its Source will not show the caller, is
// -32602 on get, update and cancel alike; a Source that fails is -32603.
func TestUnknownTasksAreInvalidParams(t *testing.T) {
	src := newFakeSource("t_")
	w := harness(t, Config{Sources: NewSources(src)})
	for _, method := range []string{MethodGet, MethodUpdate, MethodCancel} {
		for _, id := range []string{"t_missing", "other_1"} {
			resp := w.call(method, map[string]any{"taskId": id, "_meta": declaring})
			require.NotNil(t, resp.Error, "%s %s", method, id)
			var wireErr *jsonrpc.Error
			require.True(t, errors.As(resp.Error, &wireErr))
			assert.EqualValues(t, jsonrpc.CodeInvalidParams, wireErr.Code, "%s %s", method, id)
		}
	}
	src.err = errors.New("db down")
	resp := w.call(MethodGet, map[string]any{"taskId": "t_x", "_meta": declaring})
	var wireErr *jsonrpc.Error
	require.True(t, errors.As(resp.Error, &wireErr))
	assert.EqualValues(t, jsonrpc.CodeInternalError, wireErr.Code)
}

// A tasks/* request from a client that did not declare the extension is
// refused with -32021, naming the capability.
func TestUndeclaredTasksRequestIsRefused(t *testing.T) {
	src := newFakeSource("t_")
	src.put(&Snapshot{ID: "t_1", Status: StatusWorking})
	w := harness(t, Config{Sources: NewSources(src)})
	for _, method := range []string{MethodGet, MethodUpdate, MethodCancel} {
		resp := w.call(method, map[string]any{"taskId": "t_1"})
		var wireErr *jsonrpc.Error
		require.True(t, errors.As(resp.Error, &wireErr), method)
		assert.EqualValues(t, CodeMissingCapability, wireErr.Code, method)
		assert.JSONEq(t, `{"requiredCapabilities":{"extensions":{"io.modelcontextprotocol/tasks":{}}}}`, string(wireErr.Data), method)
	}
	// A request with no params at all declares nothing either.
	resp := w.call(MethodGet, nil)
	var wireErr *jsonrpc.Error
	require.True(t, errors.As(resp.Error, &wireErr))
	assert.EqualValues(t, CodeMissingCapability, wireErr.Code)
	assert.Empty(t, taskID(nil))
}

// tasks/cancel asks the Source to stop the task and acknowledges; tasks/update
// acknowledges a task the caller may follow.
func TestCancelAndUpdateAcknowledge(t *testing.T) {
	src := newFakeSource("t_")
	src.put(&Snapshot{ID: "t_1", Status: StatusWorking})
	w := harness(t, Config{Sources: NewSources(src)})

	resp := w.call(MethodCancel, map[string]any{"taskId": "t_1", "_meta": declaring})
	require.Nil(t, resp.Error)
	assert.JSONEq(t, `{"resultType":"complete"}`, string(resp.Result))
	assert.Equal(t, []string{"t_1"}, src.canceled)

	resp = w.call(MethodUpdate, map[string]any{"taskId": "t_1", "inputResponses": map[string]any{"k": map[string]any{"action": "accept"}}, "_meta": declaring})
	require.Nil(t, resp.Error)
	assert.JSONEq(t, `{"resultType":"complete"}`, string(resp.Result))
}

// The Resolver identifies the caller of each tasks/* request before its
// handler reads the task.
func TestTasksRequestsAreResolved(t *testing.T) {
	type callerKey struct{}
	var seen []any
	src := &recordingSource{fakeSource: newFakeSource("t_"), seen: &seen, key: callerKey{}}
	src.put(&Snapshot{ID: "t_1", Status: StatusWorking})
	w := harness(t, Config{Sources: NewSources(src), Resolve: func(ctx context.Context, _ mcp.Request) context.Context {
		return context.WithValue(ctx, callerKey{}, "jane")
	}})
	require.Nil(t, w.call(MethodGet, map[string]any{"taskId": "t_1", "_meta": declaring}).Error)
	assert.Equal(t, []any{"jane"}, seen)
}

type recordingSource struct {
	*fakeSource
	seen *[]any
	key  any
}

func (r *recordingSource) Get(ctx context.Context, id string) (*Snapshot, error) {
	*r.seen = append(*r.seen, ctx.Value(r.key))
	return r.fakeSource.Get(ctx, id)
}

// Only the request's own declaration counts.
func TestDeclared(t *testing.T) {
	req := func(meta map[string]any) mcp.Request {
		return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}}
	}
	assert.True(t, Declared(req(declaring)))
	assert.False(t, Declared(req(nil)))
	assert.False(t, Declared(nil))
	assert.False(t, Declared(&mcp.CallToolRequest{}))
	assert.False(t, Declared(req(map[string]any{metaKeyClientCapabilities: map[string]any{"extensions": map[string]any{"other": map[string]any{}}}})))
	assert.False(t, Declared(req(map[string]any{metaKeyClientCapabilities: "not an object"})))
	assert.False(t, Declared(req(map[string]any{metaKeyClientCapabilities: map[string]any{"bad": func() {}}})))
}

// The completion receiver installed on a context gets the outcome handed to
// Complete; without one, Complete does nothing.
func TestCompletion(t *testing.T) {
	Complete(context.Background(), nil, nil)
	var got error
	ctx := WithCompletion(context.Background(), func(_ context.Context, _ mcp.Result, err error) { got = err })
	Complete(ctx, nil, errors.New("boom"))
	assert.EqualError(t, got, "boom")
}

// A completed task with no result cannot be encoded, and Sources routes by
// prefix and skips a nil Source.
func TestEncodeResultAndSources(t *testing.T) {
	_, err := EncodeResult(nil)
	require.Error(t, err)
	raw, err := EncodeResult(&mcp.CallToolResult{})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"content":[]`)

	a, b := newFakeSource("a_"), newFakeSource("b_")
	s := NewSources(a, nil)
	s.Add(nil)
	s.Add(b)
	assert.Same(t, a, s.For("a_1"))
	assert.Same(t, b, s.For("b_1"))
	assert.Nil(t, s.For("c_1"))
	var none *Sources
	assert.Nil(t, none.For("a_1"))
	assert.True(t, (&Snapshot{Status: StatusCancelled}).Terminal())
	assert.False(t, (&Snapshot{Status: StatusWorking}).Terminal())
	assert.True(t, IsTask(NewCreateResult(&Snapshot{ID: "x"})))
	assert.False(t, IsTask(&mcp.CallToolResult{}))
}
