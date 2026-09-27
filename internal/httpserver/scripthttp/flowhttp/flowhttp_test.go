package flowhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func version(source string) func(http.ResponseWriter, *http.Request) (*script.Script, *script.Version, bool) {
	return func(http.ResponseWriter, *http.Request) (*script.Script, *script.Version, bool) {
		return &script.Script{ID: "s1"}, &script.Version{ScriptID: "s1", Version: 3, Source: source}, true
	}
}

func refuse(w http.ResponseWriter, _ *http.Request) (*script.Script, *script.Version, bool) {
	httpjson.WriteError(w, http.StatusNotFound, "version not found")
	return nil, nil, false
}

func signedIn(bool) func(*http.Request) bool {
	return func(*http.Request) bool { return true }
}

func get(t *testing.T, deps Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h := New(deps)
	pass := func(next http.Handler) http.Handler { return next }
	h.RegisterPortal(mux, pass)
	h.RegisterAdmin(mux, "/api/v1/admin", pass)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestGraphRoutes(t *testing.T) {
	deps := Deps{Load: version(`platform.query("SELECT 1")`), SignedIn: signedIn(true)}

	rec := get(t, deps, "/api/v1/portal/scripts/s1/versions/3/graph")
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "s1", body["script_id"])
	assert.InDelta(t, 3, body["version"], 0)
	assert.Equal(t, true, body["ok"])
	assert.Len(t, body["nodes"], 1)

	rec = get(t, Deps{Load: version("def f(:\n")}, "/api/v1/admin/scripts/s1/versions/3/graph")
	require.Equal(t, http.StatusOK, rec.Code)
	body = map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, false, body["ok"], "a source that does not parse is a 200 with its findings")
	assert.NotEmpty(t, body["findings"])
	assert.Equal(t, []any{}, body["nodes"])

	rec = get(t, Deps{Load: refuse, SignedIn: signedIn(true)}, "/api/v1/portal/scripts/s1/versions/3/graph")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// A portal request with nobody signed in is refused before any lookup.
	rec = get(t, Deps{Load: version("x = 1\n"), SignedIn: func(*http.Request) bool { return false }},
		"/api/v1/portal/scripts/s1/versions/3/graph")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec = get(t, Deps{Load: version("x = 1\n")}, "/api/v1/portal/scripts/s1/versions/3/graph")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
