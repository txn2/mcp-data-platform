package sitegw

import (
	"context"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// Tile answers a request the page an environment's tile is drawn from makes
// (#2658), by the rules the environment's own address serves its release
// with: the file, a directory's index, the release's redirects and rewrites,
// its mounted media and its 404 page.
//
// The page is drawn as the site's owner (#2747), resolved from the live
// directory and persona registry for the draw: /_viewer names them, and an
// application endpoint runs as them, as it does when they open the
// environment. A pages address has no signed-out visitor, so the tile shows
// a page someone sees. With no owner (removed from the directory, or holding
// no persona) /_viewer names nobody and an endpoint answers 401. Only GET and
// HEAD reach an endpoint: any other method answers 405 without running a
// script. The run's caller is marked eprun.AuthTypeSiteTile, which its audit
// row records, and the request's application cookies are withheld. Nothing is
// counted as the site's traffic.
func (g *Gateway) Tile(w http.ResponseWriter, r *http.Request, route *site.Route, owner *epform.Viewer) {
	switch {
	case r.URL.Path == pathViewer:
		writeSiteViewer(w, owner)
	case strings.HasPrefix(r.URL.Path, prefixEP):
		g.tileEndpoint(w, r, route, owner)
	default:
		g.serveSite(w, r, route, nil)
	}
}

// tileEndpoint answers an application endpoint for a tile's page.
func (g *Gateway) tileEndpoint(w http.ResponseWriter, r *http.Request, route *site.Route, owner *epform.Viewer) {
	w.Header().Set(hdrCache, noStore)
	switch {
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		w.Header().Set("Allow", "GET, HEAD")
		httpjson.WriteError(w, http.StatusMethodNotAllowed, "a page being drawn for a tile reads and changes nothing")
	case route.PartOf == "" || g.deps.Pages == nil:
		g.serveSite(w, r, route, nil)
	case owner == nil:
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to use this endpoint")
	default:
		caller := &eprun.Caller{UserID: owner.UserID, Email: owner.Email, AuthType: eprun.AuthTypeSiteTile, Admin: owner.Admin, Persona: owner.Persona}
		g.deps.Pages.runSiteEndpoint(w, r, route, func(*http.Request) (*eprun.Caller, error) { return caller, nil }, true)
	}
}

// SiteOf is the site host serves, and whether the platform answers host at
// all: a site's address or domain names its site, and an application's page
// origin, the pages domain and any hostname under it are the platform's with
// no site. A host the platform does not answer is false.
func (g *Gateway) SiteOf(ctx context.Context, host string) (siteID string, served bool) {
	if host == "" {
		return "", false
	}
	if domain := g.pagesDomain(); domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
		served = true
	}
	route, err := g.route(ctx, host)
	if err != nil || route == nil {
		return "", served
	}
	return route.SiteID, true
}
