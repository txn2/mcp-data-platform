package sitegw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

func (h *harness) tile(route *site.Route, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://tile.invalid"+target, nil)
	rec := httptest.NewRecorder()
	h.gateway.Tile(rec, req, route)
	return rec
}

// TestTile_AnswersAsTheAddressServesTheRelease: a tile's page is answered by
// the release's files, rewrites, redirects and 404 page, as a visitor who
// has not signed in: /_viewer names nobody and an application endpoint is
// 401 without running.
func TestTile_AnswersAsTheAddressServesTheRelease(t *testing.T) {
	h := newHarness(Deps{})
	rel := h.addRelease(1, []site.Redirect{
		{From: "/old", To: "/new", Status: http.StatusMovedPermanently},
		{From: "/*", To: "/index.html", Status: http.StatusOK},
	},
		testFile{path: indexFile, body: "home", contentType: ctHTML},
		testFile{path: "style.css", body: "body{}", contentType: ctCSS},
	)
	route := &site.Route{SiteID: testSiteID, Release: rel, Private: true, PartOf: "store"}

	rec := h.tile(route, "/")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "home", rec.Body.String())
	rec = h.tile(route, "/style.css")
	assert.Equal(t, "body{}", rec.Body.String())
	rec = h.tile(route, "/old")
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/new", rec.Header().Get("Location"))
	rec = h.tile(route, "/products/42")
	assert.Equal(t, http.StatusOK, rec.Code, "an application's routes fall back to its page")
	assert.Equal(t, "home", rec.Body.String())

	rec = h.tile(route, pathViewer)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "nobody is signed in on a tile")
	rec = h.tile(route, prefixEP+"stock")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no endpoint runs for a tile")
	assert.Contains(t, rec.Header().Get("Content-Type"), "json")
}

// TestSiteOf: a site's address names its site; an application's origin and
// the pages domain are the platform's with no site; any other host is not
// the platform's.
func TestSiteOf(t *testing.T) {
	h := newHarness(Deps{})
	rel := baseRelease(h)
	h.mapHost(rel)
	id, served := h.gateway.SiteOf(context.Background(), testSiteHost)
	require.True(t, served)
	assert.Equal(t, testSiteID, id)

	id, served = h.gateway.SiteOf(context.Background(), testPagesDomain)
	assert.True(t, served, "the pages domain is the platform's")
	assert.Empty(t, id)
	id, served = h.gateway.SiteOf(context.Background(), "nothing."+testPagesDomain)
	assert.True(t, served, "a label under the pages domain is the platform's even with no site")
	assert.Empty(t, id)

	_, served = h.gateway.SiteOf(context.Background(), "cdn.jsdelivr.net")
	assert.False(t, served)
	_, served = h.gateway.SiteOf(context.Background(), "")
	assert.False(t, served)
}
