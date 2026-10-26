package submissionshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epsubmit"
	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

type sources []whsource.Source

func (s sources) List(context.Context) ([]whsource.Source, error) { return s, nil }

type tables struct{}

func (tables) TargetFor(c string) (whtable.Target, error) {
	return whtable.Target{Connection: c, Catalog: "hive", Schema: "scratch"}, nil
}

func fixture(queryErr error) (mux *http.ServeMux, gotSQLp *string) {
	var gotSQL string
	r := &epsubmit.Reader{
		Sources: sources{{
			Name: "form-contact", Connection: "scratch",
			Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_1", Endpoint: "contact"}},
		}},
		Tables: tables{},
		Query: func(_ context.Context, _, sql string) ([]map[string]any, error) {
			gotSQL = sql
			if queryErr != nil {
				return nil, queryErr
			}
			return []map[string]any{{
				"event_id": "e1", "received_at": "2026-10-26 09:00:00",
				"payload": `{"fields":["email"],"values":{"email":"ann@example.com"}}`,
			}}, nil
		},
	}
	h := New(Deps{
		Reader: r,
		Owned: func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
			if r.Header.Get("X-Owner") == "" {
				w.WriteHeader(http.StatusNotFound)
				return "", "", false
			}
			return r.PathValue("id"), "owner@example.com", true
		},
		Viewer: func(*http.Request) epsubmit.Viewer { return epsubmit.Viewer{Email: "owner@example.com"} },
	})
	mux = http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	return mux, &gotSQL
}

func get(mux *http.ServeMux, path string, owner bool) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	if owner {
		req.Header.Set("X-Owner", "1")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestList(t *testing.T) {
	mux, sql := fixture(nil)
	rec := get(mux, "/api/v1/portal/scripts/scr_1/submissions?limit=5", true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, *sql, "LIMIT 5")
	var page epsubmit.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Submissions, 1)
	assert.Equal(t, "ann@example.com", page.Submissions[0].Values["email"])
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	assert.Equal(t, http.StatusNotFound, get(mux, "/api/v1/portal/scripts/scr_1/submissions", false).Code, "only the owner")
	assert.Equal(t, http.StatusNotFound, get(mux, "/api/v1/portal/scripts/scr_2/submissions", true).Code, "not a form")

	broken, _ := fixture(errors.New("trino down"))
	rec = get(broken, "/api/v1/portal/scripts/scr_1/submissions", true)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "trino down")
}

func TestExport(t *testing.T) {
	mux, sql := fixture(nil)
	rec := get(mux, "/api/v1/portal/scripts/scr_1/submissions/export", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, *sql, "LIMIT 10000", "an export reads up to the most one read returns")
	assert.Equal(t, `attachment; filename="form-contact-submissions.csv"`, rec.Header().Get("Content-Disposition"))
	assert.Contains(t, rec.Body.String(), "ann@example.com")

	rec = get(mux, "/api/v1/portal/scripts/scr_1/submissions/export?format=xlsx&limit=3", true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, *sql, "LIMIT 3")
	assert.Contains(t, rec.Header().Get("Content-Type"), "spreadsheetml")

	assert.Equal(t, http.StatusBadRequest, get(mux, "/api/v1/portal/scripts/scr_1/submissions/export?format=pdf", true).Code)
	assert.Equal(t, http.StatusNotFound, get(mux, "/api/v1/portal/scripts/scr_1/submissions/export", false).Code)
}

// ForPortal builds the routes over the scripts handler's ownership gate and
// the portal's caller; with no reader it mounts nothing.
func TestForPortal(t *testing.T) {
	assert.Nil(t, ForPortal(nil, scripthttp.Deps{}, nil))
	var none *Handler
	none.Register(http.NewServeMux(), func(next http.Handler) http.Handler { return next })

	reader := &epsubmit.Reader{Sources: sources{}, Tables: tables{}, Query: func(context.Context, string, string) ([]map[string]any, error) { return nil, nil }}
	var who *scripthttp.PortalIdentity
	deps := scripthttp.Deps{PortalUser: func(*http.Request) *scripthttp.PortalIdentity { return who }}
	h := ForPortal(scripthttp.New(deps), deps, reader)
	require.NotNil(t, h)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	assert.Equal(t, epsubmit.Viewer{}, h.deps.Viewer(req), "nobody signed in is nobody")
	who = &scripthttp.PortalIdentity{UserID: "u1", Email: "ann@example.com", Persona: "analyst"}
	assert.Equal(t, epsubmit.Viewer{UserID: "u1", Email: "ann@example.com", Persona: "analyst"}, h.deps.Viewer(req))

	who = nil
	mux := http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/scripts/s1/submissions", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the scripts handler's gate refuses a caller who is nobody")
}
