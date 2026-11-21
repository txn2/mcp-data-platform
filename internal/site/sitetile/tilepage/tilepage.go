// Package tilepage builds the page a site environment's tile is drawn from
// (#2658): the release, served in process by the rules the environment's own
// address serves it with, drawn as the site's owner sees it (#2747). Every
// address on the pages domain asks for sign-in, so a signed-out view is one
// nobody sees; the owner is resolved from the live directory and persona
// registry when the page is built, and a site whose owner no longer resolves
// is drawn for nobody.
//
// Every request the page makes is answered as its address would answer it, or
// as a browser on the internet would get it:
//
//   - the page's own origin, and every hostname of the same site (its pages
//     addresses and domains, which a release names absolutely), is the
//     release: its files, redirects and rewrites, mounted media and 404 page
//     (sitegw.Gateway.Tile), and /_viewer and the application's endpoints
//     answer as the owner. No address is opened, so drawing a private
//     environment opens nothing;
//   - the platform's own hostname answers its public routes in process (the
//     vendored runtimes a page loads from the platform);
//   - a hostname of another site, or an application's page origin, is not
//     found;
//   - every other hostname is a public file, fetched by the renderer through
//     its egress guard (a CDN's script, a web font).
//
// Before #2681 the page served the release's files alone and refused every
// other hostname, so a page built on a CDN's React drew blank and one that
// fell back to index.html through _redirects drew an error.
package tilepage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// The page a tile is drawn at: a desktop page, reduced to a tile.
const (
	pageWidth  = 1280
	pageHeight = 960
	pageScale  = 0.625
)

// The network wait a site's page gets (headless.Page.Quiet): a page that draws
// itself from what it fetches after its load event has drawn nothing at it.
const (
	quiet    = 500 * time.Millisecond
	quietMax = 8 * time.Second
)

// ready waits for what is in the picture and nothing else (#2677): the load
// event, the fonts, then every image whose box intersects the 1280x960 frame
// the tile is captured from. An image outside the frame cannot change a pixel
// of the tile, and a loading="lazy" one there is never fetched, so its
// decode() never settles; waiting on it held every such page to the render
// deadline. An image inside the frame is fetched by the browser on its own,
// lazy or not, so nothing about the page is rewritten.
const ready = `new Promise(function(done){
  if (document.readyState === "complete") { done(); return; }
  addEventListener("load", function(){ done(); }, {once: true});
}).then(function(){
  return document.fonts ? document.fonts.ready : null;
}).then(function(){
  return Promise.all(Array.prototype.filter.call(document.images, function(i){
    var r = i.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth;
  }).map(function(i){ return i.decode().catch(function(){}); }));
}).then(function(){ return ""; })`

// slash is the root of a site's paths.
const slash = "/"

// maxFile bounds one answer read from the gateway for the page.
const maxFile = 64 << 20

// ErrNoDocument is a release with no page at its root to draw.
var ErrNoDocument = errors.New("the release has no index.html at its root to draw")

// Server answers a site's requests: sitegw.Gateway.
type Server interface {
	// Tile answers r for route's release as the environment's address does
	// to owner; a nil owner is nobody.
	Tile(w http.ResponseWriter, r *http.Request, route *site.Route, owner *epform.Viewer)
	// SiteOf is the site a hostname serves, and whether the platform
	// answers the hostname at all.
	SiteOf(ctx context.Context, host string) (siteID string, served bool)
}

// Releases reads a site, an environment's route and a release.
type Releases interface {
	Get(ctx context.Context, id string) (*site.Site, error)
	RouteEnv(ctx context.Context, siteName, env string) (*site.Route, error)
	Release(ctx context.Context, siteID string, number int) (*site.Release, error)
}

// Owner resolves the person a site's owner address names, nil when the
// platform cannot speak for them (removed from the directory, or holding no
// persona).
type Owner func(ctx context.Context, email string) *epform.Viewer

// Platform answers a path of the platform's own public routes for the page,
// false for a path it does not serve that way.
type Platform func(path string, header http.Header) (headless.File, bool)

// Release is where a site's releases are read and served from.
type Release struct {
	Store  Releases
	Server Server
	// PlatformHost is the platform's own hostname, whose public routes
	// platform answers; "" leaves it to the public fetch.
	PlatformHost string
	// Owner resolves the site's owner the page is drawn as; nil draws every
	// page for nobody.
	Owner Owner
}

// Page is the page release number of the site's environment is drawn from.
func (r Release) Page(ctx context.Context, siteID, environment string, number int, platform Platform) (headless.Page, error) {
	if r.Store == nil || r.Server == nil {
		return headless.Page{}, errors.New("this deployment serves no sites")
	}
	route, owner, err := r.route(ctx, siteID, environment, number)
	if err != nil {
		return headless.Page{}, err
	}
	t := tile{ctx: ctx, server: r.Server, route: route, owner: owner, platformHost: r.PlatformHost, platform: platform}
	root := t.serve(slash, nil)
	if root.Status != http.StatusOK || !strings.Contains(strings.ToLower(root.ContentType), "html") {
		return headless.Page{}, ErrNoDocument
	}
	return headless.Page{
		Document: root.Body,
		Files: func(p string, h http.Header) (headless.File, bool) {
			return t.serve(p, h), true
		},
		Substitute:          t.substitute,
		Ready:               ready,
		Width:               pageWidth,
		Height:              pageHeight,
		Scale:               pageScale,
		RequireSubresources: true,
		Quiet:               quiet,
		QuietMax:            quietMax,
	}, nil
}

// route is what the environment's address serves with release number in
// place of the one it serves now, and the owner the page is drawn as. The
// application it is a part of is the environment's own, so its endpoints
// run in the application environment they run in for a visitor.
func (r Release) route(ctx context.Context, siteID, environment string, number int) (*site.Route, *epform.Viewer, error) {
	st, err := r.Store.Get(ctx, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the site: %w", err)
	}
	env, err := r.Store.RouteEnv(ctx, st.Name, environment)
	if err != nil {
		return nil, nil, fmt.Errorf("reading environment %s: %w", environment, err)
	}
	rel := env.Release
	if rel == nil || rel.Number != number {
		if rel, err = r.Store.Release(ctx, siteID, number); err != nil {
			return nil, nil, fmt.Errorf("reading release %d: %w", number, err)
		}
	}
	route := &site.Route{
		SiteID: siteID, Site: st.Name, Environment: environment, Release: rel, Private: true,
		PartOf: env.PartOf, PartOfEnvironment: env.PartOfEnvironment,
	}
	var owner *epform.Viewer
	if r.Owner != nil && st.OwnerEmail != "" {
		owner = r.Owner(ctx, st.OwnerEmail)
	}
	return route, owner, nil
}

// tile is one page's answers.
type tile struct {
	ctx          context.Context //nolint:containedctx // the page's answers are bounded by the render that asks them
	server       Server
	route        *site.Route
	owner        *epform.Viewer
	platformHost string
	platform     Platform
}

// serve is the release's answer to a path of the page's own origin.
func (t tile) serve(p string, h http.Header) headless.File {
	target := &url.URL{Scheme: "http", Host: "tile.invalid", Path: p}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		target.Path, target.RawQuery = p[:i], strings.TrimPrefix(p[i:], "?")
	}
	req := httptest.NewRequestWithContext(t.ctx, http.MethodGet, target.String(), http.NoBody)
	for k, v := range h {
		if strings.EqualFold(k, "Range") || strings.EqualFold(k, "Accept") {
			req.Header[k] = v
		}
	}
	rec := httptest.NewRecorder()
	t.server.Tile(rec, req, t.route, t.owner)
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFile))
	if err != nil {
		return headless.File{Status: http.StatusServiceUnavailable}
	}
	return headless.RouteFile(res.StatusCode, res.Header, body)
}

// substitute answers a request on another hostname that is not a public
// file: the same site's other addresses as the release, the platform's public
// routes, and another site, an application's page origin or any other
// hostname the platform answers as not found, so the page never reaches the
// platform from outside. Everything else goes to the renderer's public fetch.
func (t tile) substitute(u *url.URL, own bool) (headless.File, bool) {
	if own {
		return headless.File{}, false
	}
	host := strings.ToLower(u.Hostname())
	path := u.EscapedPath()
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	if t.platformHost != "" && strings.EqualFold(host, t.platformHost) {
		if t.platform != nil {
			if f, ok := t.platform(u.Path, nil); ok {
				return f, true
			}
		}
		return headless.File{Status: http.StatusNotFound}, true
	}
	siteID, served := t.server.SiteOf(t.ctx, host)
	switch {
	case !served:
		return headless.File{}, false
	case siteID == t.route.SiteID:
		return t.serve(path, nil), true
	default:
		return headless.File{Status: http.StatusNotFound}, true
	}
}
