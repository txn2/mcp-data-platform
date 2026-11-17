package sitegw

import (
	"context"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// Tile answers a request the page an environment's tile is drawn from makes
// (#2658), by the rules the environment's own address serves its release
// with: the file, a directory's index, the release's redirects and rewrites,
// its mounted media and its 404 page. The page is drawn for nobody, so it is
// answered as a visitor who has not signed in: /_viewer names no one and an
// application endpoint answers 401, which is what such a visitor's browser
// gets, and no endpoint runs for a tile. Nothing is counted as the site's
// traffic.
func (g *Gateway) Tile(w http.ResponseWriter, r *http.Request, route *site.Route) {
	switch {
	case r.URL.Path == pathViewer:
		writeSiteViewer(w, nil)
	case strings.HasPrefix(r.URL.Path, prefixEP):
		w.Header().Set(hdrCache, noStore)
		httpjson.WriteError(w, http.StatusUnauthorized, "sign in to use this endpoint")
	default:
		g.serveSite(w, r, route)
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
