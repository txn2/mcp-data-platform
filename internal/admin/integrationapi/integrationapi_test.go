package integrationapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

type fakeService struct {
	err      error
	bound    integration.BindRequest
	actor    integration.Actor
	cleared  string
	retried  string
	overview []integration.Overview
}

func (f *fakeService) Overview(context.Context) ([]integration.Overview, error) {
	return f.overview, f.err
}

func (f *fakeService) Candidates(_ context.Context, a integration.Actor, op, search string) ([]integration.Candidate, error) {
	f.actor = a
	if f.err != nil {
		return nil, f.err
	}
	return []integration.Candidate{{ID: "s1", Name: "ingress-routes " + op + " " + search}}, nil
}

func (f *fakeService) Bind(_ context.Context, a integration.Actor, req integration.BindRequest) (*integration.Binding, error) {
	f.actor, f.bound = a, req
	if f.err != nil {
		return nil, f.err
	}
	return &integration.Binding{Operation: req.Operation, ScriptID: req.ScriptID}, nil
}

func (f *fakeService) Clear(_ context.Context, _ integration.Actor, op string) error {
	f.cleared = op
	return f.err
}

func (f *fakeService) Retry(_ context.Context, op string) error {
	f.retried = op
	return f.err
}

type recorder struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
}

func (r *recorder) Log(_ context.Context, ev middleware.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func serve(t *testing.T, svc Service, audit middleware.AuditLogger) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: svc, Audit: audit,
		Actor: func(*http.Request) integration.Actor { return integration.Actor{Email: "ops@example.com", Admin: true} },
	})
	return mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	return rec
}

// TestRoutes pins #2659's admin surface: list, the picker's scripts, a bind
// carrying the script, interval and approval, a clear and a retry, each
// change audited.
func TestRoutes(t *testing.T) {
	svc := &fakeService{overview: []integration.Overview{{Name: integration.OpSiteRouting, Counts: map[string]int{}}}}
	audit := &recorder{}
	mux := serve(t, svc, audit)

	rec := do(mux, http.MethodGet, basePath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"operation":"site_routing"`)

	rec = do(mux, http.MethodGet, basePath+"/site_routing/scripts?search=ing", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "ingress-routes site_routing ing")

	rec = do(mux, http.MethodPut, basePath+"/site_routing", `{"script_id":" s1 ","interval_seconds":600,"approve":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, integration.BindRequest{Operation: "site_routing", ScriptID: "s1", IntervalSeconds: 600, Approve: true}, svc.bound)
	assert.Equal(t, "ops@example.com", svc.actor.Email)

	rec = do(mux, http.MethodPut, basePath+"/site_routing", `{`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = do(mux, http.MethodDelete, basePath+"/site_routing", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "site_routing", svc.cleared)

	rec = do(mux, http.MethodPost, basePath+"/site_routing/retry", "")
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, "site_routing", svc.retried)

	require.Len(t, audit.events, 3)
	assert.Equal(t, []string{toolBind, toolClear, toolRetry}, []string{audit.events[0].ToolName, audit.events[1].ToolName, audit.events[2].ToolName})
	assert.Equal(t, "ops@example.com", audit.events[0].UserEmail)
	assert.True(t, audit.events[0].Success)
}

// TestRoutes_Errors: each refusal is answered by what it is, and audited as
// a failure.
func TestRoutes_Errors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{fmt.Errorf("x: %w", integration.ErrUnknown), http.StatusNotFound},
		{integration.ErrNotBound, http.StatusNotFound},
		{fmt.Errorf("x: %w", integration.ErrNotAllowed), http.StatusForbidden},
		{fmt.Errorf("defines no route(req): %w", integration.ErrInvalid), http.StatusBadRequest},
		{errors.New("db down"), http.StatusInternalServerError},
	} {
		audit := &recorder{}
		mux := serve(t, &fakeService{err: tc.err}, audit)
		rec := do(mux, http.MethodPut, basePath+"/site_routing", `{"script_id":"s1"}`)
		assert.Equal(t, tc.code, rec.Code, tc.err.Error())
		require.Len(t, audit.events, 1)
		assert.False(t, audit.events[0].Success)
		assert.Equal(t, tc.code, do(mux, http.MethodDelete, basePath+"/site_routing", "").Code)
		assert.Equal(t, tc.code, do(mux, http.MethodPost, basePath+"/site_routing/retry", "").Code)
		assert.Equal(t, tc.code, do(mux, http.MethodGet, basePath+"/site_routing/scripts", "").Code)
	}
	mux := serve(t, &fakeService{err: errors.New("down")}, nil)
	assert.Equal(t, http.StatusInternalServerError, do(mux, http.MethodGet, basePath, "").Code)
	assert.Equal(t, http.StatusInternalServerError, do(mux, http.MethodPut, basePath+"/site_routing", `{"script_id":"s1"}`).Code,
		"with no audit logger the refusal still answers")

	empty := http.NewServeMux()
	Register(empty, nil, Config{})
	assert.Equal(t, http.StatusNotFound, do(empty, http.MethodGet, basePath, "").Code, "no service mounts nothing")
}
