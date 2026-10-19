package bindinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// store holds bindings by script, failing every read when err is set.
type store struct {
	byScript map[string]epbinding.Binding
	err      error
}

func (s *store) Set(_ context.Context, b *epbinding.Binding) error {
	b.ID = "ep_" + b.ScriptID
	s.byScript[b.ScriptID] = *b
	return nil
}

func (s *store) Get(_ context.Context, id string) (*epbinding.Binding, error) {
	if s.err != nil {
		return nil, s.err
	}
	b, ok := s.byScript[id]
	if !ok {
		return nil, epbinding.ErrNotFound
	}
	return &b, nil
}

func (*store) GetByName(context.Context, string) (*epbinding.Binding, error) {
	return nil, epbinding.ErrNotFound
}

func (*store) List(context.Context, epbinding.Filter) ([]epbinding.Binding, error) {
	return []epbinding.Binding{}, nil
}

func (s *store) SetEnabled(_ context.Context, id string, enabled bool, _ string) error {
	b, ok := s.byScript[id]
	if !ok {
		return epbinding.ErrNotFound
	}
	b.Enabled = enabled
	s.byScript[id] = b
	return nil
}

func (*store) RecordFailure(context.Context, epbinding.Failure) error { return nil }
func (*store) Failures(context.Context, string, int) ([]epbinding.Failure, error) {
	return []epbinding.Failure{}, nil
}
func (*store) SweepFailures(context.Context, time.Time) (int64, error) { return 0, nil }
func (*store) Stamp(context.Context, string) (epbinding.Stamp, error) {
	return epbinding.Stamp{}, nil
}

type scripts map[string]*script.Script

func (s scripts) GetByID(_ context.Context, id string) (*script.Script, error) { return s[id], nil }

// mux mounts the routes with the caller named by the X-Who header: "owner"
// owns script s1, "admin" is an administrator, anyone else owns nothing.
func mux(st *store) *http.ServeMux {
	m := http.NewServeMux()
	New(Deps{
		Commands: &epcmd.Handler{
			Store: st, BaseURL: "https://data.example.com",
			PublicAllowed: func(context.Context) (bool, error) { return true, nil },
		},
		Scripts: scripts{"s1": {ID: "s1", Name: "lookup", Source: "def handle(req):\n    return {}\n"}},
		Owned: func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
			switch who := r.Header.Get("X-Who"); who {
			case "owner", "admin":
				return r.PathValue("id"), who + "@example.com", true
			}
			http.Error(w, "not found", http.StatusNotFound)
			return "", "", false
		},
		Admin: func(r *http.Request) bool { return r.Header.Get("X-Who") == "admin" },
	}).Register(m, func(h http.Handler) http.Handler { return h })
	return m
}

func do(m *http.ServeMux, method, path, who, body string) (rec *httptest.ResponseRecorder, out map[string]any) {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("X-Who", who)
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	out = map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// #2171: an administrator binds a script on the portal and the binding is
// on the script's page; its owner reads it and is refused a change.
func TestAnAdministratorBindsAndTheOwnerReads(t *testing.T) {
	st := &store{byScript: map[string]epbinding.Binding{}}
	m := mux(st)
	rec, out := do(m, "GET", "/api/v1/portal/scripts/s1/endpoint", "owner", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, out["bound"])

	rec, _ = do(m, "PUT", "/api/v1/portal/scripts/s1/endpoint", "owner", `{"endpoint":"orders","auth_mode":"signed_in"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the owner is refused")
	assert.Empty(t, st.byScript)

	rec, out = do(m, "PUT", "/api/v1/portal/scripts/s1/endpoint", "admin", `{"endpoint":"orders","auth_mode":"signed_in","timeout_seconds":30}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "https://data.example.com/endpoints/orders", out["url"])
	assert.Equal(t, 30, st.byScript["s1"].TimeoutSeconds)

	rec, out = do(m, "GET", "/api/v1/portal/scripts/s1/endpoint", "owner", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, out["bound"])
	assert.Equal(t, []any{}, out["failures"])

	rec, _ = do(m, "POST", "/api/v1/portal/scripts/s1/endpoint/disable", "owner", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec, out = do(m, "POST", "/api/v1/portal/scripts/s1/endpoint/disable", "admin", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, false, out["enabled"])
	rec, _ = do(m, "POST", "/api/v1/portal/scripts/s1/endpoint/enable", "admin", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, st.byScript["s1"].Enabled)
}

func TestRefusals(t *testing.T) {
	st := &store{byScript: map[string]epbinding.Binding{}}
	m := mux(st)
	rec, _ := do(m, "GET", "/api/v1/portal/scripts/s1/endpoint", "stranger", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "neither owner nor administrator")
	rec, _ = do(m, "GET", "/api/v1/portal/scripts/nope/endpoint", "admin", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = do(m, "PUT", "/api/v1/portal/scripts/s1/endpoint", "admin", `{"endpoint":`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = do(m, "PUT", "/api/v1/portal/scripts/s1/endpoint", "admin", `{"endpointt":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "an unknown field is refused")
	rec, _ = do(m, "PUT", "/api/v1/portal/scripts/s1/endpoint", "admin", `{"endpoint":"Bad Name","auth_mode":"signed_in"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	st.err = errors.New("down")
	rec, _ = do(m, "GET", "/api/v1/portal/scripts/s1/endpoint", "owner", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "a store that failed is the platform's failure")
}
