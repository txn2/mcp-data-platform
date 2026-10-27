package changesethttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// recordingChangesets answers each route with 200 and records the admin user
// the request reached it with.
type recordingChangesets struct {
	served []string
	user   *admin.User
}

func (c *recordingChangesets) record(name string, w http.ResponseWriter, r *http.Request) {
	c.served = append(c.served, name+" "+r.PathValue("id"))
	c.user = admin.GetUser(r.Context())
	w.WriteHeader(http.StatusOK)
}

func (c *recordingChangesets) ListChangesets(w http.ResponseWriter, r *http.Request) {
	c.record("list", w, r)
}

func (c *recordingChangesets) GetChangeset(w http.ResponseWriter, r *http.Request) {
	c.record("get", w, r)
}

func (c *recordingChangesets) RollbackChangeset(w http.ResponseWriter, r *http.Request) {
	c.record("rollback", w, r)
}

// callerHeader names the test caller; the reviewer role holds apply_knowledge.
const (
	callerHeader = "X-Test-Caller"
	reviewer     = "reviewer"
)

func newMux(cs Changesets) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Deps{
		Changesets: cs,
		Caller: func(r *http.Request) *Caller {
			role := r.Header.Get(callerHeader)
			if role == "" {
				return nil
			}
			return &Caller{UserID: "u-" + role, Email: role + "@example.com", Roles: []string{role}}
		},
		CanApply: func(c *Caller) bool { return c.Roles[0] == reviewer },
	})
	return mux
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		name, method, path, caller string
		status                     int
		served                     string
	}{
		{"list for an apply_knowledge holder", http.MethodGet, listPath, reviewer, http.StatusOK, "list "},
		{"one changeset", http.MethodGet, "/api/v1/portal/knowledge/changesets/cs-1", reviewer, http.StatusOK, "get cs-1"},
		{"rollback", http.MethodPost, "/api/v1/portal/knowledge/changesets/cs-1/rollback", reviewer, http.StatusOK, "rollback cs-1"},
		{"no caller", http.MethodGet, listPath, "", http.StatusUnauthorized, ""},
		{"caller without apply_knowledge", http.MethodGet, listPath, "analyst", http.StatusForbidden, ""},
		{"rollback without apply_knowledge", http.MethodPost, "/api/v1/portal/knowledge/changesets/cs-1/rollback", "analyst", http.StatusForbidden, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &recordingChangesets{}
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody)
			if tt.caller != "" {
				req.Header.Set(callerHeader, tt.caller)
			}
			w := httptest.NewRecorder()
			newMux(cs).ServeHTTP(w, req)

			assert.Equal(t, tt.status, w.Code)
			if tt.served == "" {
				assert.Empty(t, cs.served, "a refused caller reaches no handler")
				return
			}
			require.Equal(t, []string{tt.served}, cs.served)
			require.NotNil(t, cs.user, "the admin handler is handed the portal caller")
			assert.Equal(t, "u-reviewer", cs.user.UserID)
			assert.Equal(t, "reviewer@example.com", cs.user.Email)
		})
	}
}

func TestRegisterWithoutHandlerMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Deps{})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, listPath, http.NoBody))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Mount admits a portal caller whose persona lists apply_knowledge, and an
// administrator; anyone else is refused before the admin handler runs.
func TestMount(t *testing.T) {
	kh := admin.NewKnowledgeHandler(nil, nil, nil, nil, nil, nil)
	resolver := func(roles []string) *portal.PersonaInfo {
		if roles[0] == "analyst" {
			return &portal.PersonaInfo{Name: "analyst", Tools: []string{"trino_query"}}
		}
		return nil
	}
	isAdmin := func(roles []string) bool { return roles[0] == "admin" }
	as := func(roles ...string) func(http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if len(roles) > 0 {
					r = r.WithContext(portal.ContextWithUser(r.Context(), &portal.User{UserID: "u", Email: "u@example.com", Roles: roles}))
				}
				h.ServeHTTP(w, r)
			})
		}
	}
	for _, tc := range []struct {
		roles []string
		want  int
	}{{nil, http.StatusUnauthorized}, {[]string{"analyst"}, http.StatusForbidden}} {
		mux := http.NewServeMux()
		Mount(mux, as(tc.roles...), kh, resolver, isAdmin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/portal/knowledge/changesets/cs-1/rollback", http.NoBody))
		assert.Equal(t, tc.want, w.Code, "%v", tc.roles)
	}

	none := http.NewServeMux()
	Mount(none, as(), nil, resolver, isAdmin)
	w := httptest.NewRecorder()
	none.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, listPath, http.NoBody))
	assert.Equal(t, http.StatusNotFound, w.Code, "no knowledge handler, no routes")
	Mount(http.NewServeMux(), as(), kh, nil, isAdmin)
}
