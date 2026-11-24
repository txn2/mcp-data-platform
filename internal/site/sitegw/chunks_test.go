package sitegw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/site"
)

type listingStore struct {
	*memStore
	releases []site.Release
}

func (s *listingStore) Releases(context.Context, string) ([]site.Release, error) {
	return s.releases, nil
}

const jsType = "text/javascript; charset=utf-8"

func TestChunks_CachedForGoodAndServedFromAnEarlierRelease(t *testing.T) {
	h := newHarness(Deps{})
	old := h.addRelease(4, nil, testFile{path: "_chunks/Reports-ABCD2345.js", body: "old chunk", contentType: jsType})
	pruned := h.addRelease(3, nil, testFile{path: "_chunks/Gone-ABCD2345.js", body: "gone", contentType: jsType})
	current := h.addRelease(5, nil, testFile{path: "app.js", body: "entry", contentType: jsType},
		testFile{path: "_chunks/Home-ABCD2345.js", body: "home", contentType: jsType})
	h.gateway.deps.Store = &listingStore{memStore: h.store, releases: []site.Release{*current, *old, {ID: pruned.ID, Number: 3, Pruned: true}}}
	h.mapHost(current)

	home := h.get(testSiteHost, "/_chunks/Home-ABCD2345.js")
	assert.Equal(t, http.StatusOK, home.Code)
	assert.Equal(t, cacheImmutable, home.Header().Get(hdrCache), "a chunk's name is its content's hash")
	assert.Equal(t, cacheRevalidate, h.get(testSiteHost, "/app.js").Header().Get(hdrCache), "an entry is revalidated, which is how a release picks up new chunk names")

	earlier := h.get(testSiteHost, "/_chunks/Reports-ABCD2345.js")
	assert.Equal(t, http.StatusOK, earlier.Code, "a page still running release 4 loads its chunk after 5 is published")
	assert.Equal(t, "old chunk", earlier.Body.String())
	assert.Equal(t, http.StatusNotFound, h.get(testSiteHost, "/_chunks/Gone-ABCD2345.js").Code, "a pruned release serves nothing")
	assert.Equal(t, http.StatusNotFound, h.get(testSiteHost, "/_chunks/Missing-ABCD2345.js").Code)
	assert.Equal(t, http.StatusNotFound, h.get(testSiteHost, "/Reports-ABCD2345.js").Code, "only chunks are looked for in earlier releases")
}

func TestPrivateMaps(t *testing.T) {
	h := newHarness(Deps{})
	rel := h.addRelease(1, nil, testFile{path: "app.js", body: "entry", contentType: jsType},
		testFile{path: "app.js.map", body: `{"version":3}`, contentType: "application/json"})
	rel.PrivateMaps = true
	h.mapHost(rel)
	assert.Equal(t, http.StatusNotFound, h.get(testSiteHost, "/app.js.map").Code, "an anonymous request to a public address reads no private map")
	assert.Equal(t, http.StatusOK, h.get(testSiteHost, "/app.js").Code)

	managers := map[string]bool{"owner@example.com": true}
	h.gateway.deps.Access = &Access{
		IsAdmin:   func(p string) bool { return p == "admin" },
		MayManage: func(_ context.Context, _, email string) bool { return managers[email] },
	}
	route := &site.Route{SiteID: testSiteID, Release: rel, Private: true}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app.js.map", http.NoBody)
	for name, tc := range map[string]struct {
		viewer *epform.Viewer
		hidden bool
	}{
		"no one":        {nil, true},
		"a manager":     {&epform.Viewer{Email: "owner@example.com"}, false},
		"an unverified": {&epform.Viewer{Email: "owner@example.com", EmailUnverified: true}, true},
		"a viewer":      {&epform.Viewer{Email: "viewer@example.com"}, true},
		"an admin":      {&epform.Viewer{Email: "a@example.com", Persona: "admin"}, false},
	} {
		assert.Equal(t, tc.hidden, h.gateway.hiddenMap(req, route, "/app.js.map", tc.viewer), name)
	}
	assert.False(t, h.gateway.hiddenMap(req, route, "/app.js", nil), "only maps are kept back")
	public := *rel
	public.PrivateMaps = false
	assert.False(t, h.gateway.hiddenMap(req, &site.Route{Release: &public}, "/app.js.map", nil), "a public map is served")
}
