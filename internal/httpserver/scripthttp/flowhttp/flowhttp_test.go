package flowhttp

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGraphRoutes_CompareWithAnOlderVersion(t *testing.T) {
	older := func(_ context.Context, id string, n int) (*script.Version, error) {
		switch n {
		case 1:
			return &script.Version{ScriptID: id, Version: 1, Source: `platform.export("o", [], format="csv")` + "\n"}, nil
		case 5:
			return nil, errors.New("boom")
		}
		return nil, nil //nolint:nilnil // the store contract: nil, nil is not found
	}
	deps := Deps{
		Load:     version(`platform.export("o", [], format="csv", destination="acme-drop", key="o.csv")` + "\n"),
		SignedIn: signedIn(true),
		Version:  older,
	}
	rec := get(t, deps, "/api/v1/portal/scripts/s1/versions/3/graph?compare=1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		ComparedWith int `json:"compared_with"`
		Nodes        []struct {
			Title  string `json:"title"`
			Change string `json:"change"`
			Was    struct {
				Title string `json:"title"`
			} `json:"was"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.ComparedWith)
	require.Len(t, body.Nodes, 1)
	assert.Equal(t, "changed", body.Nodes[0].Change)
	assert.Equal(t, "Export CSV to portal", body.Nodes[0].Was.Title)

	for query, code := range map[string]int{"?compare=x": 400, "?compare=0": 400, "?compare=9": 404, "?compare=5": 500} {
		rec = get(t, deps, "/api/v1/portal/scripts/s1/versions/3/graph"+query)
		assert.Equal(t, code, rec.Code, query)
	}
	rec = get(t, Deps{Load: deps.Load, SignedIn: deps.SignedIn}, "/api/v1/portal/scripts/s1/versions/3/graph?compare=1")
	assert.Equal(t, http.StatusNotFound, rec.Code, "no version store, no comparison")
}
