package callrun

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// fixture is a server whose one tool answers status and body, behind the
// handlers' layer, with the caller set as the tool-call middleware would.
type fixture struct {
	e      *Evaluator
	called atomic.Int32
	status int
	body   map[string]any
}

type echoIn struct {
	Connection string         `json:"connection"`
	SQL        string         `json:"sql,omitempty"`
	Body       any            `json:"body,omitempty"`
	Method     string         `json:"method,omitempty"`
	Path       string         `json:"path,omitempty"`
	Purpose    string         `json:"purpose,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

type echoOut struct {
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
}

func (f *fixture) connect(t *testing.T, opts *mcp.ClientOptions, version string) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "api_invoke_endpoint"}, func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, echoOut, error) {
		f.called.Add(1)
		return nil, echoOut{Status: f.status, Body: f.body}, nil
	})
	server.AddReceivingMiddleware(f.e.Middleware())
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			pc := middleware.NewPlatformContext("r1")
			pc.Connection, pc.UserEmail, pc.PersonaName, pc.ToolkitKind = "billing-api", "jo@example.com", "marketing", apiKind
			pc.EventID = "evt-1"
			return next(middleware.WithPlatformContext(ctx, pc), method, req)
		}
	})
	ct, st := mcp.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "1"}, opts)
	var copts *mcp.ClientSessionOptions
	if version != "" {
		copts = &mcp.ClientSessionOptions{ProtocolVersion: version}
	}
	cs, err := client.Connect(context.Background(), ct, copts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func newFixture(t *testing.T, w *world) *fixture {
	t.Helper()
	e := w.evaluator(t)
	e.Shapes = func(string) Shapes { return shapes{} }
	return &fixture{e: e, status: 200, body: map[string]any{"card": map[string]any{"number": "4111 1111 1111 1111"}}}
}

func call(t *testing.T, cs *mcp.ClientSession, args map[string]any) (res *mcp.CallToolResult, text string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "api_invoke_endpoint", Arguments: args})
	require.NoError(t, err)
	text = ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	return res, text
}

var echoArgs = map[string]any{"connection": "billing-api", "method": "POST", "path": "/lists/7"}

// TestMiddleware_AnUnmatchedCallRuns: no handler on the connection, a call
// from a handler's own run, and a call before the evaluator exists.
func TestMiddleware_AnUnmatchedCallRuns(t *testing.T) {
	w := newWorld()
	w.add("d", callhandler.KindPolicy, denySrc, func(h *callhandler.Handler) { h.Connection = "crm" })
	f := newFixture(t, w)
	res, _ := call(t, f.connect(t, nil, ""), echoArgs)
	assert.False(t, res.IsError)
	assert.EqualValues(t, 1, f.called.Load())

	next := func(context.Context, string, mcp.Request) (mcp.Result, error) { return &mcp.CallToolResult{}, nil }
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "api_invoke_endpoint"}}
	_, err := LateMiddleware(func() *Evaluator { return nil })(next)(context.Background(), methodToolsCall, req)
	require.NoError(t, err, "no evaluator lets the call through")
	_, err = f.e.Middleware()(next)(callhandler.WithinHandler(context.Background()), methodToolsCall, req)
	require.NoError(t, err, "a handler's own call is not evaluated")
	_, err = f.e.Middleware()(next)(context.Background(), "tools/list", nil)
	require.NoError(t, err)
}

// TestMiddleware_ARefusedCallIsNotMade answers policy_denied and never calls
// the tool.
func TestMiddleware_ARefusedCallIsNotMade(t *testing.T) {
	w := newWorld()
	w.add("d", callhandler.KindPolicy, denySrc, nil)
	f := newFixture(t, w)
	res, text := call(t, f.connect(t, nil, ""), echoArgs)
	assert.True(t, res.IsError)
	assert.Contains(t, text, "POLICY_DENIED: billing-api addSubscribers is refused by script-d: not on Fridays")
	assert.EqualValues(t, 0, f.called.Load())
}

// TestMiddleware_AHeldCall answers approval_required where the client cannot
// be asked, and asks a 2026-07-28 client: accepting files the request and
// never makes the call, declining files nothing, no answer files nothing.
func TestMiddleware_AHeldCall(t *testing.T) {
	w := newWorld()
	w.add("h", callhandler.KindPolicy, holdSrc, nil)
	f := newFixture(t, w)
	res, text := call(t, f.connect(t, nil, ""), echoArgs)
	assert.True(t, res.IsError)
	assert.Contains(t, text, "APPROVAL_REQUIRED: billing-api addSubscribers is held by script-h: waits. ops-leads or an administrator can approve it.")
	assert.Contains(t, text, "manage_approval")

	answer := "accept"
	asked := 0
	opts := &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked++
		assert.Contains(t, req.Params.Message, "Ask for approval?")
		return &mcp.ElicitResult{Action: answer, Content: map[string]any{"calls": 3.0, "reason": "weekly import"}}, nil
	}}
	cs := f.connect(t, opts, multiRoundTripVersion)
	res, text = call(t, cs, echoArgs)
	assert.Equal(t, 1, asked)
	assert.True(t, res.IsError, "accepting never makes the call")
	assert.Contains(t, text, "Approval request apr_1 is filed")
	require.Len(t, w.filed, 1)
	assert.Equal(t, 3, w.filed[0].Calls)
	assert.Equal(t, "weekly import", w.filed[0].Reason)
	assert.Equal(t, "billing-api", w.filed[0].Connection)
	assert.Contains(t, w.filed[0].Rendering, "POST /lists/7")
	assert.EqualValues(t, 0, f.called.Load())

	answer = "decline"
	_, text = call(t, cs, echoArgs)
	assert.Contains(t, text, "you declined to ask for approval")
	answer = "cancel"
	_, text = call(t, cs, echoArgs)
	assert.Contains(t, text, "not answered")
	assert.Len(t, w.filed, 1, "only accepting files")
}

// TestMiddleware_ATransformChangesTheResult masks the body the caller gets,
// in structured content and text, and a failing one fails the call.
func TestMiddleware_ATransformChangesTheResult(t *testing.T) {
	w := newWorld()
	w.add("m", callhandler.KindTransform, "def transform(_call, result):\n    result[\"body\"][\"card\"][\"number\"] = \"****\"\n    return result\n", nil)
	f := newFixture(t, w)
	res, text := call(t, f.connect(t, nil, ""), echoArgs)
	require.False(t, res.IsError, text)
	assert.Contains(t, text, "****")
	assert.NotContains(t, text, "4111")

	w = newWorld()
	w.add("r", callhandler.KindTransform, "def transform(_call, _result):\n    fail(\"boom\")\n", nil)
	f = newFixture(t, w)
	res, text = call(t, f.connect(t, nil, ""), echoArgs)
	assert.True(t, res.IsError)
	assert.Contains(t, text, "TRANSFORM_FAILED: the transform script-r failed")
}

// TestMiddleware_AProtectedColumn refuses a covered caller's SQL the check
// refuses, and any SQL when no check is installed.
func TestMiddleware_AProtectedColumn(t *testing.T) {
	w := newWorld()
	w.add("p", callhandler.KindTransform, "def transform(_call, result):\n    return result\n", func(h *callhandler.Handler) {
		h.Protected = []string{"hive.hr.employees.ssn", "hr.people.ssn", "body.ssn"}
	})
	f := newFixture(t, w)
	sql := map[string]any{"connection": "billing-api", "sql": "SELECT ssn FROM t"}
	res, text := call(t, f.connect(t, nil, ""), sql)
	assert.True(t, res.IsError)
	assert.Contains(t, text, "PROTECTED_COLUMN: this deployment cannot read SQL")
	f.e.Protect = func(_ context.Context, _, _ string, protected map[string]string) error {
		assert.Equal(t, map[string]string{"hive.hr.employees.ssn": "script-p", "hr.people.ssn": "script-p"}, protected,
			"a Trino column, an application database column; a JSON path with no table is no column")
		return errors.New("hive.hr.employees.ssn is protected by script-p")
	}
	_, text = call(t, f.connect(t, nil, ""), sql)
	assert.Contains(t, text, "PROTECTED_COLUMN: hive.hr.employees.ssn is protected by script-p")
	f.e.Protect = func(context.Context, string, string, map[string]string) error { return nil }
	res, _ = call(t, f.connect(t, nil, ""), sql)
	assert.False(t, res.IsError)
}

// TestMiddleware_AWatchQueuesARunAfterASuccess and none after a failure.
func TestMiddleware_AWatchQueuesARunAfterASuccess(t *testing.T) {
	w := newWorld()
	w.add("w", callhandler.KindWatch, "def main():\n    print(run.call)\n", nil)
	f := newFixture(t, w)
	f.e.Watches = &WatchQueue{MaxQueued: DefaultMaxQueued, ch: make(chan watchRun, 4)}
	cs := f.connect(t, nil, "")
	call(t, cs, echoArgs)
	require.Len(t, f.e.Watches.ch, 1)
	queued := <-f.e.Watches.ch
	assert.Equal(t, "h-w", queued.handler.ID)
	assert.Equal(t, "billing-api", queued.call["connection"])
	f.status = 500
	call(t, cs, echoArgs)
	assert.Empty(t, f.e.Watches.ch, "a failed call queues nothing")
}

// TestHeldRequestAndStatement: submit's question of a held call, and an
// application database statement through the handlers.
func TestHeldRequestAndStatement(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("h", callhandler.KindPolicy, holdSrc, nil)
	e := w.evaluator(t)
	req, err := e.HeldRequest(ctx, approval.HeldCall{User: "jo@example.com", Persona: "marketing", Tool: "api_invoke_endpoint", Arguments: map[string]any{"connection": "billing-api", "method": "POST", "path": "/x"}, Calls: 2, Reason: "why"})
	require.NoError(t, err)
	assert.Equal(t, 2, req.Calls)
	assert.Equal(t, []string{"ops-leads"}, req.Approvers)
	_, err = e.HeldRequest(ctx, approval.HeldCall{User: "jo@example.com", Calls: 1})
	require.ErrorContains(t, err, "name the tool")
	_, err = e.Statement(ctx, callhandler.Statement{User: "jo@example.com", Connection: "billing-api", Tool: "appdb.query", SQL: "SELECT 1"})
	require.ErrorContains(t, err, "APPROVAL_REQUIRED")

	w = newWorld()
	w.add("a", callhandler.KindPolicy, allowSrc, nil)
	e = w.evaluator(t)
	_, err = e.HeldRequest(ctx, approval.HeldCall{User: "jo@example.com", Tool: "api_invoke_endpoint", Arguments: map[string]any{"connection": "billing-api"}, Calls: 1})
	require.ErrorContains(t, err, "not held")
	tr, err := e.Statement(ctx, callhandler.Statement{User: "jo@example.com", Connection: "billing-api", Tool: "appdb.query", SQL: "SELECT 1"})
	require.NoError(t, err)
	assert.Nil(t, tr, "no transform covers it")
	tr, err = e.Statement(ctx, callhandler.Statement{User: "jo@example.com", Connection: "elsewhere", Tool: "appdb.query", SQL: "SELECT 1"})
	require.NoError(t, err)
	assert.Nil(t, tr)

	w.add("d", callhandler.KindPolicy, denySrc, nil)
	e = w.evaluator(t)
	_, err = e.HeldRequest(ctx, approval.HeldCall{User: "jo@example.com", Tool: "api_invoke_endpoint", Arguments: map[string]any{"connection": "billing-api"}, Calls: 1})
	require.ErrorContains(t, err, "refused by script-d")
	_, err = e.Statement(ctx, callhandler.Statement{User: "jo@example.com", Connection: "billing-api", Tool: "appdb.query", SQL: "SELECT 1"})
	require.ErrorContains(t, err, "POLICY_DENIED")

	w = newWorld()
	w.add("t", callhandler.KindTransform, "def transform(_call, result):\n    for r in result.get(\"rows\", []):\n        r[\"x\"] = 0\n    return result\n", nil)
	e = w.evaluator(t)
	tr, err = e.Statement(ctx, callhandler.Statement{User: "jo@example.com", Connection: "billing-api", Tool: "appdb.query", SQL: "SELECT x FROM t"})
	require.NoError(t, err)
	rows, err := tr.Rows(ctx, []string{"x"}, []map[string]any{{"x": 5}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 0, rows[0]["x"])
	body, err := tr.Body(ctx, map[string]any{"k": 1})
	require.NoError(t, err)
	m, _ := body.(map[string]any)
	assert.EqualValues(t, 1, m["k"])
}

// TestTest is the panel's answer for each outcome.
func TestTest(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("h", callhandler.KindPolicy, holdSrc, nil)
	w.add("m", callhandler.KindTransform, "def transform(_call, result):\n    result[\"seen\"] = True\n    return result\n", nil)
	w.add("w", callhandler.KindWatch, "def main():\n    pass\n", nil)
	e := w.evaluator(t)
	_, err := e.Test(ctx, callhandler.TestRequest{})
	require.ErrorContains(t, err, "connection")
	_, err = e.Test(ctx, callhandler.TestRequest{Connection: "billing-api"})
	require.ErrorContains(t, err, "tool")
	_, err = e.Test(ctx, callhandler.TestRequest{Connection: "billing-api", Tool: "api_invoke_endpoint", At: "nope"})
	require.ErrorContains(t, err, "RFC 3339")
	out, err := e.Test(ctx, callhandler.TestRequest{Connection: "billing-api", Tool: "api_invoke_endpoint", User: "Jo@Example.com", At: "2026-11-13T10:00:00Z"})
	require.NoError(t, err)
	assert.Equal(t, VerdictHeld, out["outcome"])
	assert.Equal(t, []string{"ops-leads"}, out["approvers"])

	w.handlers = w.handlers[1:]
	e = w.evaluator(t)
	out, err = e.Test(ctx, callhandler.TestRequest{Connection: "billing-api", Tool: "api_invoke_endpoint", Result: map[string]any{"v": 1}})
	require.NoError(t, err)
	assert.Equal(t, VerdictRuns, out["outcome"])
	got, _ := out["result"].(map[string]any)
	assert.EqualValues(t, 1, got["v"])
	assert.Equal(t, true, got["seen"])
	assert.Equal(t, 1, out["watches"])

	w.handlers = append(w.handlers, w.add("d", callhandler.KindPolicy, denySrc, nil))
	e = w.evaluator(t)
	out, err = e.Test(ctx, callhandler.TestRequest{Connection: "billing-api", Tool: "trino_query"})
	require.NoError(t, err)
	assert.Equal(t, VerdictDenied, out["outcome"])
	assert.Equal(t, "not on Fridays", out["reason"])
}

type shapes struct{}

func (shapes) CallShape(context.Context, string, map[string]any) Shape {
	return Shape{Operation: "addSubscribers", Method: "POST", Path: "/lists/7", Params: map[string]string{"id": "7"}}
}

// TestBuildCall reads a call's operation through the api toolkit and its body
// in either form.
func TestBuildCall(t *testing.T) {
	e := &Evaluator{Shapes: func(string) Shapes { return shapes{} }}
	pc := &middleware.PlatformContext{Connection: "billing-api", ToolkitKind: apiKind, UserEmail: "jo@example.com"}
	c := e.BuildCall(context.Background(), pc, "api_invoke_endpoint", map[string]any{"body": `{"a":1}`, "query_params": map[string]any{"q": "x"}})
	assert.Equal(t, "addSubscribers", c.Operation)
	assert.Equal(t, map[string]string{"id": "7"}, c.PathParams)
	assert.Equal(t, map[string]any{"a": float64(1)}, c.Body, "a string of JSON is read as the value it holds")
	assert.Equal(t, map[string]any{"q": "x"}, c.Query)
	assert.Equal(t, "not json", bodyOf("not json"))
	c = e.BuildCall(context.Background(), &middleware.PlatformContext{Connection: "acme"}, "trino_query", map[string]any{"sql": "SELECT 1"})
	assert.Equal(t, "SELECT 1", c.SQL)
	assert.Equal(t, "trino_query", c.Operation)
	assert.True(t, strings.HasPrefix(Rendering(c), "trino_query: SELECT 1"))
	assert.Equal(t, "acme trino_query", describeCall(&callhandler.Call{Connection: "acme", Tool: "trino_query"}))
	assert.Equal(t, "an administrator", approverText(nil))
	assert.Equal(t, apiKind, kindOf("api_export"))
	assert.Empty(t, kindOf("trino_query"))
}
