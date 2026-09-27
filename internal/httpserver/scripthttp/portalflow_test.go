package scripthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp/flowhttp"
)

// serveFlow mounts the graph routes the way the composition root does, over
// this handler's own version lookup, and serves one GET.
func serveFlow(t *testing.T, deps Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	pass := func(h http.Handler) http.Handler { return h }
	flow := flowhttp.New(flowhttp.Deps{
		Load:     New(deps).LoadScriptVersion,
		SignedIn: func(r *http.Request) bool { return deps.PortalUser != nil && deps.PortalUser(r) != nil },
	})
	flow.RegisterPortal(mux, pass)
	flow.RegisterAdmin(mux, "/api/v1/admin", pass)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

// graphBody is the part of the graph response these tests read.
type graphBody struct {
	ScriptID string `json:"script_id"`
	Version  int    `json:"version"`
	OK       bool   `json:"ok"`
	Nodes    []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"nodes"`
	Edges []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"edges"`
}

// A version's diagram is read under the rule its source is (#1866, #1906):
// everyone signed in, the owner and a stranger alike.
func TestPortalGraph_ReadableByEveryoneSignedIn(t *testing.T) {
	for _, who := range []*PortalIdentity{owner, stranger, admin} {
		rec := serveFlow(t, portalDeps(portalStore(), nil, nil, who), "/api/v1/portal/scripts/script_1/versions/1/graph")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body graphBody
		decodeInto(t, rec, &body)
		assert.Equal(t, "script_1", body.ScriptID)
		assert.Equal(t, 1, body.Version)
		assert.True(t, body.OK)
		require.Len(t, body.Nodes, 2)
		assert.Equal(t, "Query warehouse", body.Nodes[0].Title)
		assert.Equal(t, "Export CSV to portal", body.Nodes[1].Title)
		require.Len(t, body.Edges, 1)
		assert.Equal(t, body.Nodes[0].ID, body.Edges[0].From)
	}
}

func TestPortalGraph_Refusals(t *testing.T) {
	rec := serveFlow(t, portalDeps(portalStore(), nil, nil, nil), "/api/v1/portal/scripts/script_1/versions/1/graph")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	for _, path := range []string{
		"/api/v1/portal/scripts/nope/versions/1/graph",
		"/api/v1/portal/scripts/script_1/versions/9/graph",
		"/api/v1/portal/scripts/script_1/versions/x/graph",
	} {
		rec = serveFlow(t, portalDeps(portalStore(), nil, nil, stranger), path)
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
	}
}

func TestAdminGraph(t *testing.T) {
	rec := serveFlow(t, adminDeps(), "/api/v1/admin/scripts/script_1/versions/1/graph")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body graphBody
	decodeInto(t, rec, &body)
	assert.True(t, body.OK)
	assert.Len(t, body.Nodes, 2)

	rec = serveFlow(t, adminDeps(), "/api/v1/admin/scripts/script_1/versions/7/graph")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// adminDeps is the admin surface's handler dependencies over one store.
func adminDeps() Deps {
	store := newStore()
	return Deps{Scripts: store, Versions: store}
}
