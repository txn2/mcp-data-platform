package sitegw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/site"
)

func (h *harness) tile(route *site.Route, target string) *httptest.ResponseRecorder {
	return h.tileAs(route, nil, http.MethodGet, target)
}

func (h *harness) tileAs(route *site.Route, owner *epform.Viewer, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, "http://tile.invalid"+target, nil)
	req.AddCookie(&http.Cookie{Name: eprun.CookiePrefixPlain + "session", Value: "visitor", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	rec := httptest.NewRecorder()
	h.gateway.Tile(rec, req, route, owner)
	return rec
}

// TestTile_AnswersAsTheAddressServesTheRelease: a tile's page is answered by
// the release's files, rewrites, redirects and 404 page; with no owner
// /_viewer names nobody and an application endpoint is 401 without running.
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
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "with no owner, nobody is signed in on a tile")
}

// tilePages is a tile's gateway with an application endpoint server that
// records the caller and the cookie choice of every run.
type tilePages struct {
	h       *harness
	route   *site.Route
	routes  []ephttp.PageRoute
	callers []*eprun.Caller
}

func tileWithPages(t *testing.T) *tilePages {
	t.Helper()
	tp := &tilePages{}
	server := &pageServer{fn: func(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute) {
		c, err := route.Caller(r)
		require.NoError(t, err)
		tp.routes, tp.callers = append(tp.routes, route), append(tp.callers, c)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"email":"` + c.Email + `"}`))
	}}
	pages := &Pages{Apps: &memApps{byName: appsNamed(appShop)}, Server: server, PagesDomain: func() string { return testPagesDomain }}
	h := newHarness(Deps{Pages: pages})
	rel := h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML})
	tp.h = h
	tp.route = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "dev", Release: rel, Private: true, PartOf: appShop, PartOfEnvironment: "dev"}
	return tp
}

// TestTile_IsDrawnAsTheOwner (#2747): /_viewer names the owner, and an
// endpoint GET or HEAD runs as them, marked as a tile draw, with the page's
// application cookies withheld; any other method answers 405 without running
// a script; with no owner, the endpoint answers 401 without running.
func TestTile_IsDrawnAsTheOwner(t *testing.T) {
	tp := tileWithPages(t)
	h, route, routes, callers := tp.h, tp.route, &tp.routes, &tp.callers
	owner := &epform.Viewer{UserID: "sub-jane", Email: ownerMail, Persona: personaUser, Admin: true, Name: "Jane Doe"}

	rec := h.tileAs(route, owner, http.MethodGet, pathViewer)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), ownerMail)
	assert.Equal(t, noStore, rec.Header().Get(hdrCache))

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec = h.tileAs(route, owner, method, prefixEP+"state")
		require.Equal(t, http.StatusOK, rec.Code, method)
	}
	require.Len(t, *callers, 2)
	for i, c := range *callers {
		assert.Equal(t, &eprun.Caller{UserID: "sub-jane", Email: ownerMail, AuthType: eprun.AuthTypeSiteTile, Admin: true, Persona: personaUser}, c)
		r := (*routes)[i]
		assert.True(t, r.CookiesWithheld, "the tile page's cookies never reach a handler")
		assert.Equal(t, "dev", r.Environment, "the run is in the application environment of the environment's name")
		assert.Equal(t, "state", r.Name)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec = h.tileAs(route, owner, method, prefixEP+"state")
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, method)
		assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
	}
	assert.Len(t, *callers, 2, "a write ran no script")

	rec = h.tileAs(route, nil, http.MethodGet, prefixEP+"state")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "json")
	assert.Len(t, *callers, 2, "with no owner, no script ran")
}

// TestTile_AnEnvironmentOfNoApplicationServesTheRelease: a site environment
// that is not a part of an application has no endpoints, so /endpoints/ is
// the release's own path, as on its address.
func TestTile_AnEnvironmentOfNoApplicationServesTheRelease(t *testing.T) {
	tp := tileWithPages(t)
	h, route, callers := tp.h, tp.route, &tp.callers
	route.PartOf, route.PartOfEnvironment = "", ""
	rec := h.tileAs(route, &epform.Viewer{Email: ownerMail}, http.MethodGet, prefixEP+"state")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, *callers)
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
