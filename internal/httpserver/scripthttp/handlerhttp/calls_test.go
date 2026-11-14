package handlerhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/platform/scripthandler"
)

// callStore keeps a world's tool-call handlers.
type callStore struct {
	w        *world
	failures []callhandler.Failure
	err      error
}

func (c callStore) Insert(context.Context, *callhandler.Handler) error { return c.err }
func (c callStore) Update(context.Context, *callhandler.Handler) error { return c.err }

func (c callStore) Get(_ context.Context, id string) (*callhandler.Handler, error) {
	h, ok := c.w.calls[id]
	if !ok {
		return nil, callhandler.ErrNotFound
	}
	return &h, nil
}

func (c callStore) ByScript(context.Context, string) ([]callhandler.Handler, error) {
	return nil, c.err
}

func (c callStore) SetEnabled(context.Context, string, bool, string) error { return c.err }
func (c callStore) Delete(context.Context, string) error                   { return c.err }

func (c callStore) Failures(context.Context, string, int) ([]callhandler.Failure, error) {
	return c.failures, c.err
}

func (c callStore) All(context.Context) ([]callhandler.Handler, error) {
	out := make([]callhandler.Handler, 0, len(c.w.calls))
	for _, h := range c.w.calls {
		out = append(out, h)
	}
	return out, c.err
}

func (callStore) Stats(context.Context, string) (map[string]callhandler.Stat, error) {
	return map[string]callhandler.Stat{"p1": {Matched: 3}}, nil
}

func (c callStore) SetOrder(_ context.Context, ids []string, _ string) error {
	for i, id := range ids {
		h, ok := c.w.calls[id]
		if !ok {
			return callhandler.ErrNotFound
		}
		h.Order = i
		c.w.calls[id] = h
	}
	return c.err
}

type tester struct {
	got callhandler.TestRequest
	err error
}

func (t *tester) Test(_ context.Context, req callhandler.TestRequest) (map[string]any, error) {
	t.got = req
	return map[string]any{"outcome": "runs"}, t.err
}

// callRig is the routes with two transforms and a policy on billing-api.
func callRig(t *testing.T, failures []callhandler.Failure) (*rig, *tester) {
	t.Helper()
	g := newRig()
	g.w.calls = map[string]callhandler.Handler{
		"p1": {ID: "p1", ScriptID: "s1", Kind: scripthandler.KindPolicy, Connection: "billing-api", Enabled: true},
		"t1": {ID: "t1", ScriptID: "s1", Kind: scripthandler.KindTransform, Connection: "billing-api", Order: 0, Enabled: true},
		"t2": {ID: "t2", ScriptID: "s1", Kind: scripthandler.KindTransform, Connection: "billing-api", Order: 1, Enabled: true},
	}
	g.svc.Calls = callStore{w: g.w, failures: failures}
	tst := &tester{}
	g.mux = http.NewServeMux()
	h := New(Deps{
		Handlers: g.svc, Scripts: scripts{g.w}, Tester: tst,
		Caller: func(r *http.Request) (scripthandler.Caller, bool) {
			email := r.Header.Get("X-User")
			return scripthandler.Caller{Email: email, Admin: r.Header.Get("X-Admin") == "yes"}, email != ""
		},
	})
	h.Register(g.mux, func(next http.Handler) http.Handler { return next })
	return g, tst
}

// TestFailures is GET /portal/handlers/{id}/failures: an administrator reads
// a tool-call handler's failures; anyone else is answered as if there were
// no handler.
func TestFailures(t *testing.T) {
	g, _ := callRig(t, []callhandler.Failure{{HandlerID: "p1", Error: "timed out"}})
	rec := g.do(t, http.MethodGet, "/api/v1/portal/handlers/p1/failures", "", admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "timed out")
	assert.Equal(t, http.StatusNotFound, g.do(t, http.MethodGet, "/api/v1/portal/handlers/p1/failures", "", jane).Code)
	assert.Equal(t, http.StatusUnauthorized, g.do(t, http.MethodGet, "/api/v1/portal/handlers/p1/failures", "", "").Code)
}

// TestConnectionHandlersRoute lists a connection's handlers, policies first,
// for an administrator only.
func TestConnectionHandlersRoute(t *testing.T) {
	g, _ := callRig(t, nil)
	rec := g.do(t, http.MethodGet, "/api/v1/portal/connections/billing-api/handlers", "", admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Less(t, strings.Index(body, `"p1"`), strings.Index(body, `"t1"`), "policies before transforms")
	assert.Less(t, strings.Index(body, `"t1"`), strings.Index(body, `"t2"`), "transforms in their order")
	assert.Contains(t, body, `"matched":3`)
	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodGet, "/api/v1/portal/connections/billing-api/handlers", "", jane).Code)
}

// TestTestPanelRoute answers what the handlers make of a call, for an
// administrator only, without sending it.
func TestTestPanelRoute(t *testing.T) {
	g, tst := callRig(t, nil)
	path := "/api/v1/portal/connections/billing-api/handlers/test"
	body := `{"tool":"api_invoke_endpoint","arguments":{"method":"PUT"},"as_user":"jo@example.com","as_persona":"marketing","at":"2026-11-13T15:40:00Z"}`
	rec := g.do(t, http.MethodPost, path, body, admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"outcome":"runs"`)
	assert.Equal(t, callhandler.TestRequest{
		Connection: "billing-api", Tool: "api_invoke_endpoint", Arguments: map[string]any{"method": "PUT"},
		User: "jo@example.com", Persona: "marketing", At: "2026-11-13T15:40:00Z",
	}, tst.got)
	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodPost, path, body, jane).Code)
	assert.Equal(t, http.StatusBadRequest, g.do(t, http.MethodPost, path, "{", admin).Code)
	tst.err = errors.New("at is not a time")
	rec = g.do(t, http.MethodPost, path, body, admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "at is not a time")

	none := newRig()
	assert.Equal(t, http.StatusBadRequest, none.do(t, http.MethodPost, path, body, admin).Code, "a deployment with no tester says so")
}

// TestOrderRoute sets the order transforms run in and answers the list.
func TestOrderRoute(t *testing.T) {
	g, _ := callRig(t, nil)
	path := "/api/v1/portal/connections/billing-api/handlers/order"
	rec := g.do(t, http.MethodPut, path, `{"ids":["t2","t1"]}`, admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Less(t, strings.Index(body, `"t2"`), strings.Index(body, `"t1"`), "the new order")
	assert.Equal(t, http.StatusForbidden, g.do(t, http.MethodPut, path, `{"ids":["t1"]}`, jane).Code)
	assert.Equal(t, http.StatusBadRequest, g.do(t, http.MethodPut, path, "[", admin).Code)
}
