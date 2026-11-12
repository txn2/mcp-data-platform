// Package sitegw is the serving layer for everything the platform answers on a
// hostname other than its own (#2517, #2518): the static sites' environments
// and an application's browser pages. It sits outside the platform's mux and
// routes by Host alone, so a request to a site or page origin never reaches a
// portal, admin, MCP or API route, and a request to any other hostname passes
// through untouched.
//
// Nothing is cached by hostname beyond a moment: which release a hostname
// serves is read again every routeTTL and at once on the replica that moved
// it, so a promote or a rollback takes effect immediately. File bytes are
// cached by their digest, which never goes stale.
package sitegw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/contentenc"
	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/pkg/observability"

	"golang.org/x/sync/singleflight"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

const (
	routeTTL   = 500 * time.Millisecond
	hostsTTL   = time.Second
	maxRoutes  = 10000
	notFound   = "404.html"
	indexFile  = "index.html"
	hdrVary    = "Vary"
	hdrCache   = "Cache-Control"
	hdrType    = "Content-Type"
	hdrNosniff = "X-Content-Type-Options"
	nosniff    = "nosniff"
	plainText  = "text/plain; charset=utf-8"

	cacheImmutable   = "public, max-age=31536000, immutable"
	cacheRevalidate  = "public, max-age=0, must-revalidate"
	headerAcceptEnc  = "Accept-Encoding"
	headerContentEnc = "Content-Encoding"
	logKeyError      = "error"
	pathSep          = "/"
)

// Store reads what a hostname serves.
type Store interface {
	RouteHost(ctx context.Context, hostname string) (*site.Route, error)
	RouteOwn(ctx context.Context, siteName string) (*site.Route, error)
	RouteEnv(ctx context.Context, siteName, env string) (*site.Route, error)
	Files(ctx context.Context, releaseID string) ([]site.File, error)
	Hosts(ctx context.Context) ([]string, error)
}

// Objects reads a release's stored files.
type Objects interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
	GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) ([]byte, int64, error)
}

// Deps is what the gateway serves from.
type Deps struct {
	Store   Store
	Objects Objects
	Bucket  string
	Config  siteconfig.Config
	// Live, when set, is the configuration in force and Config only its seed.
	Live *siteconfig.Live
	// PortalHost is the portal's own hostname, passed through without a look.
	PortalHost string
	// Pages serves an application's page origin; nil serves none.
	Pages *Pages
	// Access guards a site's own address; with none, no such address answers.
	Access *Access
	// Metrics records what the gateway answers (#2597); nil records nothing.
	Metrics *observability.Metrics
	// Media serves a site's mounted resource folders (#2591); nil serves none.
	Media Media
	// Secure is true where the origins answer over https: what decides
	// whether an answer carries Strict-Transport-Security (#2557).
	Secure bool
}

// Gateway routes by Host.
type Gateway struct {
	deps  Deps
	cache *cache
	group singleflight.Group

	mu       sync.Mutex
	routes   map[string]routeEntry
	hosts    map[string]bool
	hostsAt  time.Time
	manifest map[string]*manifest
	media    map[string]mediaEntry
}

type routeEntry struct {
	route *site.Route
	until time.Time
}

// manifest is one release's files by path.
type manifest struct {
	files map[string]site.File
}

// New builds the gateway.
func New(d Deps) *Gateway {
	limits := d.Config.Limits()
	g := &Gateway{
		deps: d, cache: newCache(limits.Cache.MemoryBytes, limits.Cache.Dir, limits.Cache.DiskBytes),
		routes: map[string]routeEntry{}, manifest: map[string]*manifest{}, media: map[string]mediaEntry{},
	}
	if d.Pages != nil {
		d.Pages.gateway = g
	}
	if d.Access != nil {
		d.Access.resolve = g.route
	}
	return g
}

// pagesDomain is the pages domain in force.
func (g *Gateway) pagesDomain() string {
	if g.deps.Live != nil {
		return g.deps.Live.Get().PagesDomain
	}
	return g.deps.Config.PagesDomain
}

// Invalidate forgets every route this replica holds, so a move it made shows
// on its very next request.
func (g *Gateway) Invalidate(string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.routes = map[string]routeEntry{}
	g.media = map[string]mediaEntry{}
	g.hostsAt = time.Time{}
}

// Wrap sends a request for a served hostname to the gateway, and any other
// through to next.
func (g *Gateway) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := site.NormalizeHost(r.Host)
		if host == g.deps.PortalHost || !g.serves(r.Context(), host) {
			next.ServeHTTP(w, r)
			return
		}
		g.serve(w, r, host)
	})
}

// serves reports whether host is under the pages domain or an added domain.
// It answers from memory: the portal's own traffic costs no query here.
func (g *Gateway) serves(ctx context.Context, host string) bool {
	if site.SameOrUnder(host, g.pagesDomain()) {
		return true
	}
	g.mu.Lock()
	fresh := time.Now().Before(g.hostsAt.Add(hostsTTL))
	known := g.hosts[host]
	g.mu.Unlock()
	if fresh {
		return known
	}
	return g.refreshHosts(ctx)[host]
}

func (g *Gateway) refreshHosts(ctx context.Context) map[string]bool {
	shared := context.WithoutCancel(ctx)
	v, _, _ := g.group.Do("hosts", func() (any, error) {
		list, err := g.deps.Store.Hosts(shared)
		if err != nil {
			slog.WarnContext(ctx, "sites: reading the added hostnames failed", logKeyError, logsan.SanitizeForLog(err.Error()))
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.hosts, nil
		}
		set := make(map[string]bool, len(list))
		for _, h := range list {
			set[h] = true
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		g.hosts, g.hostsAt = set, time.Now()
		return set, nil
	})
	set, _ := v.(map[string]bool)
	return set
}

// serve answers a request for a served hostname. The request never reaches
// the mux, so it is recorded under the gateway's own route (#2597), and one
// that resolves to a site or a page origin is counted under its name.
func (g *Gateway) serve(w http.ResponseWriter, r *http.Request, host string) {
	httpobs.SetRoute(r, httpobs.RouteSite)
	g.strictTransport(w, r)
	if host == g.pagesDomain() {
		http.NotFound(w, r)
		return
	}
	route, err := g.route(r.Context(), host)
	switch {
	case errors.Is(err, site.ErrNoDomain) || errors.Is(err, site.ErrNotFound):
		notPublished(w, r, host)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "sites: reading a route failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		http.Error(w, "this site could not be read", http.StatusServiceUnavailable)
		return
	}
	sw := g.observe(w, r, route)
	defer sw.done()
	w = sw
	if route.Application != "" {
		if g.deps.Pages == nil {
			http.NotFound(w, r)
			return
		}
		g.deps.Pages.serve(w, r, route.Application)
		return
	}
	var viewer *epform.Viewer
	if route.Private {
		if g.deps.Access == nil {
			http.NotFound(w, r)
			return
		}
		v, ok := g.deps.Access.allow(w, r, route)
		if !ok {
			return
		}
		viewer = &v
	}
	if g.serveSiteRoutes(w, r, route, viewer) {
		return
	}
	g.serveSite(w, r, route)
}

// hdrHSTS is the header that keeps a browser on https.
const hdrHSTS = "Strict-Transport-Security"

// strictTransport sends Strict-Transport-Security on an https answer when the
// deployment turns it on (#2557). The platform does not verify who owns an
// added domain, so every hostname the gateway answers is held to it alike.
func (g *Gateway) strictTransport(w http.ResponseWriter, r *http.Request) {
	cfg := g.deps.Config
	if g.deps.Live != nil {
		cfg = g.deps.Live.Get()
	}
	if v := cfg.TLS.HSTS.Header(); v != "" && (g.deps.Secure || r.TLS != nil) {
		w.Header().Set(hdrHSTS, v)
	}
}

// releaseHeaders sets what the release's _headers file declares for the
// path, and the framing default when it declares no framing rule (#2557). A
// release that has none, or an environment that serves none yet, is held to
// the default alone.
func releaseHeaders(w http.ResponseWriter, r *http.Request, route *site.Route) {
	var rules []site.HeaderRule
	if route.Release != nil {
		rules = route.Release.Headers
	}
	h := w.Header()
	for _, hd := range site.HeadersFor(rules, path.Clean(pathSep+r.URL.Path)) {
		h.Add(hd.Name, hd.Value)
	}
}

// serveSiteRoutes answers the routes an environment keeps for itself beside
// its files: who is signed in, and, for an environment that is a part of an
// application, that application's endpoints run as the viewer. It reports
// whether it answered. An answer on a public domain has no viewer.
func (g *Gateway) serveSiteRoutes(w http.ResponseWriter, r *http.Request, route *site.Route, viewer *epform.Viewer) bool {
	switch {
	case r.URL.Path == pathViewer:
		writeSiteViewer(w, viewer)
		return true
	case route.PartOf != "" && g.deps.Pages != nil && strings.HasPrefix(r.URL.Path, prefixEP):
		g.deps.Pages.siteEndpoint(w, r, route.PartOf, viewer, g.deps.Access)
		return true
	}
	return false
}

// route is what host serves, held for a moment per replica.
func (g *Gateway) route(ctx context.Context, host string) (*site.Route, error) {
	g.mu.Lock()
	entry, ok := g.routes[host]
	g.mu.Unlock()
	if ok && time.Now().Before(entry.until) {
		if entry.route == nil {
			return nil, site.ErrNoDomain
		}
		return entry.route, nil
	}
	route, err := g.lookup(ctx, host)
	if err != nil && !errors.Is(err, site.ErrNoDomain) {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.routes) >= maxRoutes {
		g.routes = map[string]routeEntry{}
	}
	g.routes[host] = routeEntry{route: route, until: time.Now().Add(routeTTL)}
	return route, err
}

// lookup reads a hostname's route. A label under the pages domain is an
// application's page origin or a site's own address; a site and an application
// cannot be created with one name, and a pair from before that rule goes to the
// application so that nobody takes over a live page origin by creating a site.
// Anything else is an added domain.
//
// A label holding "--" is <site>--<env>, an environment's own address; no
// site or environment name holds "--", and an application created after #2604
// cannot either, so an application keeps the label only when it held it first.
func (g *Gateway) lookup(ctx context.Context, host string) (*site.Route, error) {
	domain := g.pagesDomain()
	label, under := strings.CutSuffix(host, "."+domain)
	if !under || domain == "" {
		return g.deps.Store.RouteHost(ctx, host) //nolint:wrapcheck // read by identity
	}
	if strings.Contains(label, ".") {
		return nil, site.ErrNoDomain
	}
	if g.deps.Pages != nil && g.deps.Pages.application(ctx, label) != nil {
		return &site.Route{Application: label}, nil
	}
	if siteName, env, ok := site.ParseEnvLabel(label); ok {
		return g.deps.Store.RouteEnv(ctx, siteName, env) //nolint:wrapcheck // read by identity
	}
	return g.deps.Store.RouteOwn(ctx, label) //nolint:wrapcheck // read by identity
}

// manifestOf is a release's files by path, held per release: a release never
// changes.
func (g *Gateway) manifestOf(ctx context.Context, rel *site.Release) (*manifest, error) {
	g.mu.Lock()
	m := g.manifest[rel.ID]
	g.mu.Unlock()
	if m != nil {
		return m, nil
	}
	// One caller's fetch serves every caller waiting, so a client that hangs
	// up must not fail the others.
	shared := context.WithoutCancel(ctx)
	v, err, _ := g.group.Do("manifest:"+rel.ID, func() (any, error) {
		files, err := g.deps.Store.Files(shared, rel.ID)
		if err != nil {
			return nil, err //nolint:wrapcheck // said by the caller
		}
		built := &manifest{files: make(map[string]site.File, len(files))}
		for _, f := range files {
			built.files[f.Path] = f
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if len(g.manifest) >= maxRoutes {
			g.manifest = map[string]*manifest{}
		}
		g.manifest[rel.ID] = built
		return built, nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading release %s: %w", rel.ID, err)
	}
	built, _ := v.(*manifest)
	return built, nil
}

// serveSite answers a request for an environment's release: the file at the
// path, a directory's index, a redirect rule, or the release's 404 page.
func (g *Gateway) serveSite(w http.ResponseWriter, r *http.Request, route *site.Route) {
	w.Header().Set(hdrNosniff, nosniff)
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	releaseHeaders(w, r, route)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "a site answers GET and HEAD", http.StatusMethodNotAllowed)
		return
	}
	if route.Release == nil {
		http.Error(w, "this environment has no release yet", http.StatusNotFound)
		return
	}
	m, err := g.manifestOf(r.Context(), route.Release)
	if err != nil {
		slog.ErrorContext(r.Context(), "sites: reading a manifest failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		http.Error(w, "this site could not be read", http.StatusServiceUnavailable)
		return
	}
	g.answer(w, r, route, m)
}

func (g *Gateway) answer(w http.ResponseWriter, r *http.Request, route *site.Route, m *manifest) {
	p := path.Clean(pathSep + r.URL.Path)
	if f, ok := m.resolve(p, strings.HasSuffix(r.URL.Path, pathSep)); ok {
		g.serveFile(w, r, route, f, http.StatusOK)
		return
	}
	if g.serveMedia(w, r, route, p) {
		return
	}
	if target, status, ok := matchRedirect(route.Release.Redirects, p); ok {
		if r.URL.RawQuery != "" && !strings.Contains(target, "?") {
			target += "?" + r.URL.RawQuery
		}
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
		http.Redirect(w, r, target, status) // #nosec G710 -- the target comes from this release's validated redirect rules
		return
	}
	if p != pathSep && !strings.HasSuffix(r.URL.Path, pathSep) {
		if _, ok := m.files[strings.TrimPrefix(p, pathSep)+pathSep+indexFile]; ok {
			// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
			http.Redirect(w, r, p+pathSep, http.StatusMovedPermanently) // #nosec G710 -- p is a path.Clean'd absolute path, and only a directory the manifest holds reaches this
			return
		}
	}
	if f, ok := m.files[notFound]; ok {
		g.serveFile(w, r, route, f, http.StatusNotFound)
		return
	}
	w.Header().Set(hdrType, plainText)
	http.Error(w, "not found", http.StatusNotFound)
}

// resolve is the file a path names: exactly, its directory index when the
// path ends in a slash, or name.html.
func (m *manifest) resolve(p string, dir bool) (site.File, bool) {
	name := strings.TrimPrefix(p, pathSep)
	if name == "" {
		name = indexFile
	}
	if f, ok := m.files[name]; ok {
		return f, true
	}
	if f, ok := m.files[name+pathSep+indexFile]; ok && dir {
		return f, true
	}
	f, ok := m.files[name+".html"]
	return f, ok
}

// matchRedirect is the first rule p matches, and where it sends p.
func matchRedirect(rules []site.Redirect, p string) (target string, status int, ok bool) {
	for _, rule := range rules {
		prefix, splat := strings.CutSuffix(rule.From, "/*")
		switch {
		case !splat && (rule.From == p || strings.TrimSuffix(rule.From, pathSep) == p):
			return rule.To, rule.Status, true
		case splat && (p == prefix || strings.HasPrefix(p, prefix+pathSep)):
			rest := strings.TrimPrefix(strings.TrimPrefix(p, prefix), pathSep)
			if strings.ContainsAny(rest, "\\\r\n\t") {
				continue // a browser reads a backslash in a target as a slash
			}
			return strings.ReplaceAll(rule.To, ":splat", rest), rule.Status, true
		}
	}
	return "", 0, false
}

// encoding is the stored form to send for the request's Accept-Encoding: "br",
// "gz", or "" for the file itself.
func encoding(accept string, f site.File) string {
	br, gz := contentenc.Accepted(accept)
	switch {
	case br && f.BrotliSize > 0:
		return "br"
	case gz && f.GzipSize > 0:
		return "gz"
	}
	return ""
}

// contentEncoding is the header value of a stored form.
func contentEncoding(enc string) string {
	if enc == "gz" {
		return "gzip"
	}
	return enc
}

func sizeOf(f site.File, enc string) int64 {
	switch enc {
	case "br":
		return f.BrotliSize
	case "gz":
		return f.GzipSize
	}
	return f.Size
}

// serveFile writes one file: a strong validator per stored form, immutable
// caching for a fingerprinted name, and ranges and conditional requests from
// http.ServeContent. A 404 page is sent whole with its status.
func (g *Gateway) serveFile(w http.ResponseWriter, r *http.Request, route *site.Route, f site.File, status int) {
	enc := encoding(r.Header.Get(headerAcceptEnc), f)
	h := w.Header()
	h.Add(hdrVary, headerAcceptEnc)
	h.Set(hdrType, f.ContentType)
	if enc != "" {
		h.Set(headerContentEnc, contentEncoding(enc))
	}
	key := sitesvc.ObjectKey(route.SiteID, f, enc)
	cacheKey := f.SHA256 + pathSep + enc
	if status != http.StatusOK {
		if !route.Private {
			h.Set(hdrCache, "no-cache")
		}
		body, err := g.bytes(r.Context(), cacheKey, key)
		if err != nil {
			g.unreadable(w, r, err)
			return
		}
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	h.Set("ETag", `"`+f.SHA256+suffix(enc)+`"`)
	switch {
	case route.Private:
		h.Set(hdrCache, cachePrivate)
	case fingerprinted(path.Base(f.Path)):
		h.Set(hdrCache, cacheImmutable)
	default:
		h.Set(hdrCache, cacheRevalidate)
	}
	content, closer, err := g.open(r.Context(), cacheKey, key, sizeOf(f, enc))
	if err != nil {
		g.unreadable(w, r, err)
		return
	}
	defer closer()
	http.ServeContent(w, r, "", time.Time{}, content)
}

func suffix(enc string) string {
	if enc == "" {
		return ""
	}
	return "-" + enc
}

func (*Gateway) unreadable(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "sites: reading a file failed", logKeyError, logsan.SanitizeForLog(err.Error()))
	w.Header().Del(headerContentEnc)
	http.Error(w, "this file could not be read", http.StatusServiceUnavailable)
}

// Where a file body came from, the outcome site_cache_reads_total counts.
const (
	cacheReadMemory = "memory"
	cacheReadDisk   = "disk"
	cacheReadObject = "object_store"
	cacheReadRange  = "range"
)

func (g *Gateway) cacheRead(ctx context.Context, outcome string) {
	g.deps.Metrics.RecordSiteCacheRead(ctx, outcome)
}

// bytes is an object of the sites bucket's content.
func (g *Gateway) bytes(ctx context.Context, cacheKey, objectKey string) ([]byte, error) {
	return g.bytesFrom(ctx, g.deps.Bucket, cacheKey, objectKey)
}

// bytesFrom is an object's content: from memory, disk, or object storage, one
// fetch per object at a time.
func (g *Gateway) bytesFrom(ctx context.Context, bucket, cacheKey, objectKey string) ([]byte, error) {
	if body, ok := g.cache.get(cacheKey); ok {
		g.cacheRead(ctx, cacheReadMemory)
		return body, nil
	}
	shared := context.WithoutCancel(ctx)
	v, err, _ := g.group.Do("object:"+cacheKey, func() (any, error) {
		if f := g.cache.readDisk(cacheKey); f != nil {
			defer func() { _ = f.Close() }()
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(f); err == nil {
				g.cache.put(cacheKey, buf.Bytes())
				g.cacheRead(ctx, cacheReadDisk)
				return buf.Bytes(), nil
			}
		}
		body, _, err := g.deps.Objects.GetObject(shared, bucket, objectKey)
		if err != nil {
			return nil, err //nolint:wrapcheck // said by the caller
		}
		g.cache.put(cacheKey, body)
		g.cache.writeDisk(cacheKey, body)
		g.cacheRead(ctx, cacheReadObject)
		return body, nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading object: %w", err)
	}
	body, _ := v.([]byte)
	return body, nil
}

// minHashLen is the shortest name segment read as a content hash.
const minHashLen = 8

// fingerprinted reports whether a file's name carries a content hash, which is
// what makes it safe to cache forever. A page never is: its address is what
// people keep. A segment is a hash when it is hex with letters and digits
// (site.3f2a9c1b.css), or mixes cases and digits (index-D1a2B3c4.js); a word
// with a number in it or a date is not.
func fingerprinted(base string) bool {
	ext := path.Ext(base)
	if ext == ".html" || ext == ".htm" {
		return false
	}
	stem := strings.TrimSuffix(base, ext)
	return slices.ContainsFunc(strings.FieldsFunc(stem, func(r rune) bool { return r == '.' || r == '-' }), hashSegment)
}

func hashSegment(s string) bool {
	if len(s) < minHashLen {
		return false
	}
	var seen hashRunes
	for _, r := range s {
		if !seen.add(r) {
			return false
		}
	}
	return seen.hashLike()
}

// hashRunes is which kinds of rune a name segment has held so far.
type hashRunes struct {
	digit, lower, upper, nonHex bool
}

// add records r, false for a rune no hash segment holds.
func (h *hashRunes) add(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		h.digit = true
	case r >= 'a' && r <= 'z':
		h.lower = true
		h.nonHex = h.nonHex || r > 'f'
	case r >= 'A' && r <= 'Z':
		h.upper = true
	case r != '_':
		return false
	}
	return true
}

// hashLike is the shape a hash has: hex with letters and digits, or digits
// with mixed case letters.
func (h *hashRunes) hashLike() bool {
	return h.digit && ((h.lower && !h.upper && !h.nonHex) || (h.lower && h.upper))
}
