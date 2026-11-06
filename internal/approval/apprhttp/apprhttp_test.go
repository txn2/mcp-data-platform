package apprhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// store is an approval.Store in memory.
type store struct{ rows map[string]*approval.Request }

func (s *store) Insert(_ context.Context, r *approval.Request) error {
	r.Status = approval.StatusPending
	c := *r
	s.rows[r.ID] = &c
	return nil
}

func (s *store) Get(_ context.Context, id string) (*approval.Request, error) {
	r, ok := s.rows[id]
	if !ok {
		return nil, approval.ErrNotFound
	}
	c := *r
	return &c, nil
}

func (s *store) List(context.Context, approval.Filter) ([]approval.Request, error) {
	out := make([]approval.Request, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, *r)
	}
	return out, nil
}

func (s *store) Decide(_ context.Context, id string, d approval.Decision) (*approval.Request, error) {
	r, ok := s.rows[id]
	if !ok {
		return nil, approval.ErrNotFound
	}
	if r.Status != approval.StatusPending {
		return nil, approval.ErrDecided
	}
	at := d.At
	r.Status, r.DecidedBy, r.SelfApproved, r.DecidedAt = d.Status, d.By, d.SelfApproved, &at
	c := *r
	return &c, nil
}

func (*store) LatestApproved(context.Context, string, string) (*approval.Request, error) {
	return nil, approval.ErrNotFound
}

type scripts struct{}

var errNoScript = errors.New("no such script")

const form = "def form_spec(req):\n    return {}\n\ndef form_submit(req, values):\n    return None\n"

func (scripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	if id != "s1" {
		return nil, errNoScript
	}
	return &script.Script{ID: "s1", Name: "intake", OwnerEmail: "jane@example.com", Version: 1}, nil
}

func (scripts) GetByName(context.Context, string, string) (*script.Script, error) {
	return nil, errNoScript
}

func (scripts) GetVersion(_ context.Context, _ string, v int) (*script.Version, error) {
	if v != 1 {
		return nil, errNoScript
	}
	return &script.Version{Version: 1, Source: form}, nil
}

type auditor struct{ events []middleware.AuditEvent }

func (a *auditor) Log(_ context.Context, ev middleware.AuditEvent) error {
	a.events = append(a.events, ev)
	return errors.New("audit store down")
}

// TestRoutes is #2494's queue on the portal: signed in only, the owner
// files, a non-administrator deciding is refused 403, an administrator
// approves, every write is audited as manage_approval from the portal.
func TestRoutes(t *testing.T) {
	mux := http.NewServeMux()
	var who *approval.Actor
	audit := &auditor{}
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &approval.Service{Store: &store{rows: map[string]*approval.Request{}}, Scripts: scripts{}},
		Actor:   func(*http.Request) *approval.Actor { return who }, Audit: audit,
	})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
		return w
	}
	assert.Equal(t, http.StatusUnauthorized, do("GET", Prefix, "").Code)

	who = &approval.Actor{Email: "jane@example.com"}
	w := do("POST", Prefix, `{"script":"s1","reason":"launch"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	id := between(w.Body.String(), `"id":"`, `"`)
	assert.Equal(t, http.StatusUnprocessableEntity, do("POST", Prefix, `{"script":"nope"}`).Code)
	assert.Equal(t, http.StatusBadRequest, do("POST", Prefix, `{`).Code)
	assert.Equal(t, http.StatusForbidden, do("POST", Prefix+"/"+id+"/decide", `{"decision":"approve"}`).Code)
	assert.Equal(t, http.StatusOK, do("GET", Prefix+"/"+id, "").Code)
	assert.Contains(t, do("GET", Prefix+"?status=pending", "").Body.String(), `"count":1`)

	who = &approval.Actor{Email: "admin@example.com", Admin: true}
	w = do("POST", Prefix+"/"+id+"/decide", `{"decision":"approve","comment":"ok"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, http.StatusConflict, do("POST", Prefix+"/"+id+"/decide", `{"decision":"deny"}`).Code)
	assert.Equal(t, http.StatusNotFound, do("GET", Prefix+"/none", "").Code)

	require.Len(t, audit.events, 5)
	for _, ev := range audit.events {
		assert.Equal(t, "manage_approval", ev.ToolName)
		assert.Equal(t, "portal", ev.Source)
	}
	assert.Equal(t, http.StatusInternalServerError, status(errors.New("boom")))
	assert.NotPanics(t, func() { Register(http.NewServeMux(), nil, Config{}) })
}

func TestListFailure(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Config{
		Service: &approval.Service{Store: &store{rows: map[string]*approval.Request{}}},
		Actor:   func(*http.Request) *approval.Actor { return &approval.Actor{} },
	})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", Prefix, http.NoBody))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "a caller with no email reads nothing")
}

func between(s, open, closing string) string {
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	value, _, _ := strings.Cut(rest, closing)
	return value
}
