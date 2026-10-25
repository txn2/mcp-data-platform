package eprun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// memBindings is an epbinding.Store over one map, with a stamp a test moves
// as a save would.
type memBindings struct {
	mu       sync.Mutex
	byName   map[string]*epbinding.Binding
	stamps   map[string]epbinding.Stamp
	failures []epbinding.Failure
	reads    int
}

func newBindings(bs ...*epbinding.Binding) *memBindings {
	m := &memBindings{byName: map[string]*epbinding.Binding{}, stamps: map[string]epbinding.Stamp{}}
	for _, b := range bs {
		m.byName[b.Name] = b
		m.stamps[b.Name] = epbinding.Stamp{Binding: time.Unix(1, 0), Script: time.Unix(1, 0)}
	}
	return m
}

func (m *memBindings) touch(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.stamps[name]
	st.Script = st.Script.Add(time.Second)
	m.stamps[name] = st
}

func (*memBindings) Set(context.Context, *epbinding.Binding) error { return nil }
func (*memBindings) Get(context.Context, string) (*epbinding.Binding, error) {
	return nil, epbinding.ErrNotFound
}

func (m *memBindings) GetByName(_ context.Context, name string) (*epbinding.Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if b, ok := m.byName[name]; ok {
		return b, nil
	}
	return nil, epbinding.ErrNotFound
}

func (*memBindings) List(context.Context, epbinding.Filter) ([]epbinding.Binding, int, error) {
	return nil, 0, nil
}

func (*memBindings) SetEnabled(context.Context, string, bool, string) error { return nil }

func (m *memBindings) RecordFailure(_ context.Context, f epbinding.Failure) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = append(m.failures, f)
	return nil
}

func (m *memBindings) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return m.failures, nil
}

func (*memBindings) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }

func (m *memBindings) Stamp(_ context.Context, name string) (epbinding.Stamp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.stamps[name]
	if !ok {
		return st, epbinding.ErrNotFound
	}
	return st, nil
}

// memScripts holds one script and its versions.
type memScripts struct {
	mu       sync.Mutex
	sc       *script.Script
	versions map[int]*script.Version
}

func (m *memScripts) save(source string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := len(m.versions) + 1
	m.versions[next] = &script.Version{
		ID: "v" + strconv.Itoa(next), ScriptID: m.sc.ID, Version: next, Source: source,
		Author: "jane@example.com", AuthorRoles: []string{"analyst"},
	}
	cp := *m.sc
	cp.Version = next
	m.sc = &cp
}

func (m *memScripts) GetByID(context.Context, string) (*script.Script, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sc, nil
}

func (m *memScripts) GetVersion(_ context.Context, _ string, version int) (*script.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versions[version], nil
}

type memAudit struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
}

func (a *memAudit) Log(_ context.Context, e middleware.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

// lookupServer is an MCP server with one tool a handler calls, which reports
// the identity the call arrived with.
func lookupServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args map[string]any
		_ = json.Unmarshal(req.Params.Arguments, &args)
		who := ""
		if u := middleware.GetPreAuthenticatedUser(ctx); u != nil {
			who = u.UserID
		}
		out, _ := json.Marshal(map[string]any{"id": args["id"], "who": who, "source": middleware.GetSource(ctx)})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
	})
	// stall answers only when the call's context ends.
	server.AddTool(&mcp.Tool{Name: "stall", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	return server
}

const lookupSource = `
def handle(req):
    """Looks an order up."""
    res = platform.call("lookup", {"id": req["params"]["id"]})
    caller = req["caller"]
    return {"status": 200, "headers": {"X-Order": req["params"]["id"]}, "json": {"order": res, "q": req["query"].get("q"), "caller": caller["email"] if caller else None}}
`

type fixture struct {
	runner   *Runner
	bindings *memBindings
	scripts  *memScripts
	audit    *memAudit
}

func newFixture(t *testing.T, source string) *fixture {
	t.Helper()
	b := &epbinding.Binding{
		ID: "e1", ScriptID: "s1", Name: "orders", Methods: []string{"GET"}, Path: "/{id}", AuthMode: epbinding.AuthSignedIn,
		TimeoutSeconds: 5, MaxMemoryBytes: 64 << 20, MaxBodyBytes: 1 << 20, RateLimit: 600, CallerRateLimit: 60, Enabled: true,
	}
	scripts := &memScripts{sc: &script.Script{ID: "s1", Name: "order-lookup", OwnerEmail: "jane@example.com", Enabled: true, Status: script.StatusActive}, versions: map[int]*script.Version{}}
	scripts.save(source)
	f := &fixture{bindings: newBindings(b), scripts: scripts, audit: &memAudit{}}
	f.runner = &Runner{Bindings: f.bindings, Scripts: scripts, Server: lookupServer(), Audit: f.audit}
	return f
}

func (f *fixture) serve(t *testing.T, req Request) Response {
	t.Helper()
	target, err := f.runner.Resolve(context.Background(), "orders")
	require.NoError(t, err)
	return f.runner.Serve(context.Background(), target, req)
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	return out
}

func TestServe_RunsTheHandlerAsTheScriptAndAnswersWithItsReturn(t *testing.T) {
	f := newFixture(t, lookupSource)
	resp := f.serve(t, Request{
		Method: "GET", Path: "/endpoints/orders/42", Params: map[string]string{"id": "42"},
		Query: map[string][]string{"q": {"first", "second"}}, Header: http.Header{"Authorization": {"Bearer secret"}},
		Caller: &Caller{UserID: "u1", Email: "ann@example.com", AuthType: "apikey"},
	})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	assert.Equal(t, "42", resp.Header.Get("X-Order"))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	out := decode(t, resp.Body)
	assert.Equal(t, "first", out["q"], "a query parameter's first value")
	assert.Equal(t, "ann@example.com", out["caller"])
	order, _ := out["order"].(map[string]any)
	assert.Equal(t, "42", order["id"])
	assert.Equal(t, "script:order-lookup", order["who"], "the handler's call is the script principal's")
	assert.Equal(t, middleware.SourceScript, order["source"], "and arrives with the script source")

	require.Len(t, f.audit.events, 1)
	e := f.audit.events[0]
	assert.Equal(t, SourceEndpoint, e.Source)
	assert.Equal(t, string(audit.EventTypeEndpointRequest), e.EventKind)
	assert.Equal(t, "script:order-lookup", e.UserID)
	assert.Equal(t, "orders", e.Parameters["endpoint"])
	assert.Equal(t, "ann@example.com", e.Parameters["caller"])
	assert.Equal(t, 200, e.Parameters["status"])
	assert.True(t, e.Success)
	assert.True(t, strings.HasPrefix(e.SessionID, "dpx_"))
	assert.Empty(t, f.bindings.failures)
}

func TestServe_TheHandlerIsNotShownTheCallersCredentials(t *testing.T) {
	f := newFixture(t, `
def handle(req):
    """Echoes the headers."""
    return {"json": req["headers"]}
`)
	resp := f.serve(t, Request{Method: "GET", Header: http.Header{
		"Authorization": {"Bearer k"}, "Cookie": {"s=1"}, "X-Api-Key": {"k"}, "Accept": {"a/b", "c/d"},
	}})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	assert.Equal(t, map[string]any{"accept": "a/b, c/d"}, decode(t, resp.Body))
}

func TestServe_ReadsAJSONBodyAndAForm(t *testing.T) {
	f := newFixture(t, `
def handle(req):
    """Echoes what it was sent."""
    return {"json": {"json": req["json"], "form": req["form"], "body": str(req["body"]), "method": req["method"]}}
`)
	resp := f.serve(t, Request{Method: "POST", Header: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: []byte(`{"a":[1,2]}`)})
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	out := decode(t, resp.Body)
	assert.Equal(t, map[string]any{"a": []any{float64(1), float64(2)}}, out["json"])
	assert.Nil(t, out["form"])

	resp = f.serve(t, Request{Method: "POST", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, Body: []byte("name=Ann&name=Bo&x=1")})
	out = decode(t, resp.Body)
	assert.Equal(t, map[string]any{"name": "Ann", "x": "1"}, out["form"])
	assert.Nil(t, out["json"])

	resp = f.serve(t, Request{Method: "POST", Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{nope`)})
	assert.Equal(t, http.StatusBadRequest, resp.Status, "a body that says it is JSON and is not is refused")
}

// #2172: a handler past its timeout gets 504, and the failure, with its
// error, is kept for the owner.
func TestServe_PastItsTimeoutIs504AndKept(t *testing.T) {
	f := newFixture(t, `
def handle(req):
    """Waits on a call that never answers."""
    return {"json": platform.call("stall", {})}
`)
	f.runner.unit = 20 * time.Millisecond
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusGatewayTimeout, resp.Status)
	assert.Equal(t, "the endpoint did not answer in time", decode(t, resp.Body)["error"], "the caller is told nothing about the script")
	require.Len(t, f.bindings.failures, 1)
	assert.Equal(t, http.StatusGatewayTimeout, f.bindings.failures[0].Status)
	assert.Contains(t, f.bindings.failures[0].Error, "time limit")
	assert.Equal(t, 1, f.bindings.failures[0].Version)
	assert.False(t, f.audit.events[0].Success)
}

func TestServe_PastItsMemoryIs503(t *testing.T) {
	f := newFixture(t, `
def handle(req):
    """Holds too much."""
    rows = []
    for i in range(200000):
        rows.append({"i": i, "s": "x" * 100})
        platform.call("lookup", {"id": "1"}) if i % 50000 == 0 else None
    return {"json": len(rows)}
`)
	f.bindings.byName["orders"].MaxMemoryBytes = 1 << 20
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status, string(resp.Body))
	require.Len(t, f.bindings.failures, 1)
	assert.Contains(t, f.bindings.failures[0].Error, "memory")
}

func TestServe_AMalformedReturnIs500AndKept(t *testing.T) {
	for name, src := range map[string]string{
		"not a dict":      "def handle(req):\n    return 3\n",
		"unknown key":     "def handle(req):\n    return {\"code\": 200}\n",
		"json and body":   "def handle(req):\n    return {\"json\": 1, \"body\": \"x\"}\n",
		"bad status":      "def handle(req):\n    return {\"status\": 99}\n",
		"status a string": "def handle(req):\n    return {\"status\": \"200\"}\n",
		"reserved header": "def handle(req):\n    return {\"headers\": {\"Set-Cookie\": \"s=1\"}}\n",
		"cors header":     "def handle(req):\n    return {\"headers\": {\"Access-Control-Allow-Origin\": \"*\"}}\n",
		"bad header":      "def handle(req):\n    return {\"headers\": {\"X-A\": \"a\\r\\nb\"}}\n",
		"header not str":  "def handle(req):\n    return {\"headers\": {\"X-A\": 1}}\n",
		"headers list":    "def handle(req):\n    return {\"headers\": [1]}\n",
		"body an int":     "def handle(req):\n    return {\"body\": 1}\n",
		"key not str":     "def handle(req):\n    return {1: 2}\n",
		"script fails":    "def handle(req):\n    fail(\"nope\")\n",
		"no handle":       "def main():\n    pass\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, src)
			resp := f.serve(t, Request{Method: "GET"})
			assert.Equal(t, http.StatusInternalServerError, resp.Status, string(resp.Body))
			require.Len(t, f.bindings.failures, 1)
			assert.NotEmpty(t, f.bindings.failures[0].Error)
		})
	}
}

func TestServe_BodyForms(t *testing.T) {
	for name, tc := range map[string]struct {
		src, contentType, body string
		status                 int
	}{
		"string":         {"def handle(req):\n    return {\"body\": \"hi\"}\n", "text/plain; charset=utf-8", "hi", 200},
		"bytes":          {"def handle(req):\n    return {\"body\": b\"hi\"}\n", "application/octet-stream", "hi", 200},
		"own type":       {"def handle(req):\n    return {\"status\": 201, \"headers\": {\"Content-Type\": \"text/csv\"}, \"body\": \"a,b\"}\n", "text/csv", "a,b", 201},
		"empty":          {"def handle(req):\n    return {\"status\": 204}\n", "", "", 204},
		"json of a list": {"def handle(req):\n    return {\"json\": [1, \"a\", None]}\n", "application/json", `[1,"a",null]`, 200},
	} {
		t.Run(name, func(t *testing.T) {
			resp := newFixture(t, tc.src).serve(t, Request{Method: "GET"})
			assert.Equal(t, tc.status, resp.Status)
			assert.Equal(t, tc.contentType, resp.Header.Get("Content-Type"))
			assert.Equal(t, tc.body, string(resp.Body))
		})
	}
}

func TestServe_ADisabledScriptIs503(t *testing.T) {
	f := newFixture(t, lookupSource)
	f.scripts.sc.Enabled = false
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status)
	assert.Equal(t, "this endpoint is not available", decode(t, resp.Body)["error"])
}

func TestServe_ExportsAreRefused(t *testing.T) {
	f := newFixture(t, "def handle(req):\n    platform.export(name = \"x\", rows = [{\"a\": 1}], format = \"csv\")\n    return {}\n")
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusInternalServerError, resp.Status)
	require.Len(t, f.bindings.failures, 1)
	assert.Contains(t, f.bindings.failures[0].Error, "not available to an endpoint")
}

// #2172: after a new version is saved, the next request runs it. The stamp
// is what tells a replica; a request on an unchanged stamp reads nothing
// else.
func TestResolve_ANewVersionIsRunByTheNextRequest(t *testing.T) {
	f := newFixture(t, "def handle(req):\n    return {\"json\": 1}\n")
	assert.Equal(t, "1", string(f.serve(t, Request{Method: "GET"}).Body))
	reads := f.bindings.reads
	assert.Equal(t, "1", string(f.serve(t, Request{Method: "GET"}).Body))
	assert.Equal(t, reads, f.bindings.reads, "an unchanged stamp reuses what the replica holds")

	f.scripts.save("def handle(req):\n    return {\"json\": 2}\n")
	f.bindings.touch("orders")
	assert.Equal(t, "2", string(f.serve(t, Request{Method: "GET"}).Body))
}

// A stamp that moved without a new version (the binding's limits changed)
// keeps the compiled module.
func TestResolve_KeepsTheProgramOfTheSameVersion(t *testing.T) {
	f := newFixture(t, lookupSource)
	first, err := f.runner.Resolve(context.Background(), "orders")
	require.NoError(t, err)
	f.bindings.touch("orders")
	second, err := f.runner.Resolve(context.Background(), "orders")
	require.NoError(t, err)
	assert.NotSame(t, first, second)
	assert.Same(t, first.program, second.program)
}

func TestResolve_AnUnknownNameIsNotFound(t *testing.T) {
	f := newFixture(t, lookupSource)
	_, err := f.runner.Resolve(context.Background(), "nope")
	require.ErrorIs(t, err, epbinding.ErrNotFound)
}

func TestResolve_ReadFailures(t *testing.T) {
	f := newFixture(t, lookupSource)
	f.scripts.sc = nil
	_, err := f.runner.Resolve(context.Background(), "orders")
	require.ErrorIs(t, err, epbinding.ErrNotFound, "a binding whose script is gone serves nothing")

	f = newFixture(t, lookupSource)
	f.scripts.versions = map[int]*script.Version{}
	_, err = f.runner.Resolve(context.Background(), "orders")
	require.ErrorContains(t, err, "has no version")
}

func TestRequestValue_RefusesAFormThatIsNotOne(t *testing.T) {
	_, err := requestValue(Request{Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, Body: []byte("a=%zz")})
	require.Error(t, err)
}

func TestFailedRun(t *testing.T) {
	assert.Equal(t, http.StatusInternalServerError, failedRun(errors.New("boom")).Status)
}

// A replica past MaxConcurrent running handlers answers the next at once,
// with 503 and Retry-After, and runs nothing.
func TestServe_PastTheConcurrencyCapIs503(t *testing.T) {
	f := newFixture(t, lookupSource)
	target, err := f.runner.Resolve(context.Background(), "orders")
	require.NoError(t, err)
	f.runner.slotsOnce.Do(func() { f.runner.slots = make(chan struct{}, MaxConcurrent) })
	for range MaxConcurrent {
		f.runner.slots <- struct{}{}
	}
	resp := f.runner.Serve(context.Background(), target, Request{Method: "GET"})
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status)
	assert.Equal(t, "1", resp.Header.Get("Retry-After"))
	assert.Empty(t, f.audit.events, "nothing ran")

	<-f.runner.slots
	resp = f.runner.Serve(context.Background(), target, Request{Method: "GET", Params: map[string]string{"id": "1"}})
	assert.Equal(t, http.StatusOK, resp.Status, "a freed slot is taken")
}

// tokenSource mints a token on a request with no ?token and verifies the one
// it is given on a request with one (#2175).
const tokenSource = `
def handle(req):
    """Mints a token, or verifies the one the request carries."""
    tok = req["query"].get("token")
    if tok == None:
        return {"json": {"token": platform.mint_token({"applicant": "a-17"}, 600, secret = "credit-key", single_use = True)}}
    return {"json": platform.verify_token(tok, secret = "credit-key")}
`

// tokenUses is the single-use record in memory.
type tokenUses struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (u *tokenUses) Use(_ context.Context, id string, _ time.Time) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	first := !u.seen[id]
	u.seen[id] = true
	return first, nil
}

func (*tokenUses) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

// A token one request to an endpoint mints verifies on a later request to the
// same endpoint with its claims intact, once; it is refused at another
// endpoint; and the key is read for the persona the version's author roles
// resolve to.
func TestServe_ATokenMintedByOneRequestVerifiesOnALaterOne(t *testing.T) {
	f := newFixture(t, tokenSource)
	other := *f.bindings.byName["orders"]
	other.ID, other.Name = "e2", "elsewhere"
	f.bindings.byName["elsewhere"] = &other
	f.bindings.stamps["elsewhere"] = f.bindings.stamps["orders"]
	var personas []string
	f.runner.Tokens = &scripttoken.Issuer{
		Keys: func(_ context.Context, name, persona string, _ bool) (string, error) {
			personas = append(personas, persona)
			return "signing-" + name, nil
		},
		Uses:      &tokenUses{seen: map[string]bool{}},
		Authority: func(roles []string) (string, bool) { return strings.Join(roles, "+"), false },
	}
	f.scripts.versions[1].AuthorRoles = []string{"sales"}

	minted := decode(t, f.serve(t, Request{Method: "GET"}).Body)
	tok, _ := minted["token"].(string)
	require.NotEmpty(t, tok, "%v", minted)

	verify := func(name string) map[string]any {
		target, err := f.runner.Resolve(context.Background(), name)
		require.NoError(t, err)
		resp := f.runner.Serve(context.Background(), target, Request{Method: "GET", Query: map[string][]string{"token": {tok}}})
		require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
		return decode(t, resp.Body)
	}
	elsewhere := verify("elsewhere")
	assert.Equal(t, false, elsewhere["ok"])
	assert.Equal(t, scripttoken.ReasonOtherEndpoint, elsewhere["reason"])

	first := verify("orders")
	assert.Equal(t, true, first["ok"], "%v", first)
	assert.Equal(t, map[string]any{"applicant": "a-17"}, first["claims"])
	second := verify("orders")
	assert.Equal(t, false, second["ok"])
	assert.Equal(t, scripttoken.ReasonUsed, second["reason"])
	assert.Equal(t, []string{"sales", "sales", "sales", "sales"}, personas)
}

// Without an issuer the members refuse and the request fails as a failed run.
func TestServe_TokensWithoutAnIssuerFail(t *testing.T) {
	f := newFixture(t, tokenSource)
	resp := f.serve(t, Request{Method: "GET"})
	assert.Equal(t, http.StatusInternalServerError, resp.Status)
	require.Len(t, f.bindings.failures, 1)
	assert.Contains(t, f.bindings.failures[0].Error, "issues no tokens")
}
