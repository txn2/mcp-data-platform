package sitegw

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epformpage"
	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// The routes of a page origin, and where the portal hands a person to one.
const (
	pathAuth     = "/_auth"
	pathViewer   = "/_viewer"
	prefixStatic = "/_static/"
	prefixEP     = "/endpoints/"
	// HandoffPath is the portal route that hands a signed-in person to a page
	// origin.
	HandoffPath = "/pages/handoff"
)

// Key labels, each separating a token's key from the form key it derives from.
const (
	viewerKeyLabel  = "page-viewer-v1"
	handoffKeyLabel = "page-handoff-v1"
)

const (
	cookieViewer    = "page_viewer"
	cookieState     = "page_state"
	securePrefix    = "__Host-"
	handoffTTL      = time.Minute
	stateTTLSeconds = 600
	viewerScope     = "app:"
	handoffScope    = "handoff:"
	staticPolicy    = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; sandbox"
	staticCache     = "public, max-age=60, must-revalidate"
	queryCode       = "code"
	queryPath       = "path"
	queryHost       = "host"
	appTTL          = 500 * time.Millisecond
	schemeHTTPS     = "https"
	noStore         = "no-store"
)

var (
	stateShape = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// hostShape is what a Host a redirect is built from may be: a name and
	// a numeric port, so that nothing in it can read as a userinfo.
	hostShape = regexp.MustCompile(`^[a-z0-9.-]+(:\d{1,5})?$`)
)

// AppStore reads an application and its parts.
type AppStore interface {
	GetByName(ctx context.Context, name string) (*application.Application, error)
}

// Assets reads an application's asset parts.
type Assets interface {
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
}

// PageServer answers an application's endpoints on its page origin.
type PageServer interface {
	ServePage(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute)
}

// Authenticate is who a portal request is signed in as, ephttp.ErrForbidden
// for a person who may not reach pages.
type Authenticate func(r *http.Request) (*eprun.Caller, error)

// Pages serves an application's page origin and the handoff that signs a
// person in to it. A viewer is a signed token scoped to one application, in a
// cookie of that origin alone: the portal's session never crosses.
type Pages struct {
	Apps     AppStore
	Assets   Assets
	Versions portaldomain.VersionReader
	Server   PageServer
	// Authenticate reads the portal session on the handoff route.
	Authenticate Authenticate
	// RouteHost says what an added hostname serves.
	RouteHost func(ctx context.Context, host string) (*site.Route, error)
	// UseHandoff records a code's hash and reports false when it was used.
	UseHandoff func(ctx context.Context, codeHash string, expires time.Time) (bool, error)
	// FormKey is what the two token keys derive from; nil signs nobody in.
	FormKey   []byte
	PortalURL string
	// PagesDomain is the pages domain in force.
	PagesDomain func() string
	// PublicPort is the port the origins answer on, "" for the scheme's own.
	PublicPort string
	// Secure is whether the origins are https, which their cookies follow.
	Secure bool
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	gateway *Gateway

	mu       sync.Mutex
	appCache map[string]appEntry
}

// appEntry is an application as a page origin reads it, held a moment.
type appEntry struct {
	app    *application.Application
	assets map[string]*portaldomain.Asset
	until  time.Time
}

func (p *Pages) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pages) viewerKey() []byte {
	if len(p.FormKey) == 0 {
		return nil
	}
	return shareguest.DeriveKey(p.FormKey, viewerKeyLabel)
}

func (p *Pages) handoffKey() []byte {
	if len(p.FormKey) == 0 {
		return nil
	}
	return shareguest.DeriveKey(p.FormKey, handoffKeyLabel)
}

func (p *Pages) cookieName(base string) string {
	if p.Secure {
		return securePrefix + base
	}
	return base
}

// application is the named application and its assets by name and id, held a
// moment; nil for none.
func (p *Pages) application(ctx context.Context, name string) *appEntry {
	p.mu.Lock()
	entry, ok := p.appCache[name]
	p.mu.Unlock()
	if ok && p.now().Before(entry.until) {
		return &entry
	}
	a, err := p.Apps.GetByName(ctx, name)
	if err != nil {
		if !errors.Is(err, application.ErrNotFound) {
			slog.WarnContext(ctx, "pages: reading an application failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		}
		return nil
	}
	built := appEntry{app: a, assets: p.loadAssets(ctx, a), until: p.now().Add(appTTL)}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.appCache == nil || len(p.appCache) >= maxRoutes {
		p.appCache = map[string]appEntry{}
	}
	p.appCache[name] = built
	return &built
}

// loadAssets reads the application's asset parts as they are served. Two
// parts sharing a name answer to neither name: which one a page got would
// depend on the order they were read in. Each still answers to its id.
func (p *Pages) loadAssets(ctx context.Context, a *application.Application) map[string]*portaldomain.Asset {
	out := map[string]*portaldomain.Asset{}
	shared := map[string]bool{}
	for _, part := range a.Parts {
		if part.Kind != application.KindAsset {
			continue
		}
		as, err := p.Assets.Get(ctx, part.ID)
		if err != nil || as == nil || as.DeletedAt != nil {
			continue
		}
		served, err := portaldomain.Served(ctx, p.Versions, as)
		if err != nil {
			slog.WarnContext(ctx, "pages: reading a served asset failed", logKeyError, logsan.SanitizeForLog(err.Error()))
			continue
		}
		out[served.ID] = served
		if _, dup := out[served.Name]; dup {
			shared[served.Name] = true
		}
		out[served.Name] = served
	}
	for name := range shared {
		delete(out, name)
	}
	return out
}

// serve answers a request on an application's page origin.
func (p *Pages) serve(w http.ResponseWriter, r *http.Request, appName string) {
	entry := p.application(r.Context(), appName)
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set(hdrNosniff, nosniff)
	switch {
	case r.URL.Path == pathAuth:
		p.completeHandoff(w, r, appName)
	case r.URL.Path == pathViewer:
		p.viewer(w, r, appName)
	case strings.HasPrefix(r.URL.Path, prefixStatic):
		p.static(w, r, entry, strings.TrimPrefix(r.URL.Path, prefixStatic))
	case strings.HasPrefix(r.URL.Path, prefixEP):
		name, rest, found := strings.Cut(strings.TrimPrefix(r.URL.Path, prefixEP), "/")
		if found {
			rest = pathSep + rest
		}
		p.endpoint(w, r, entry.app, name, rest)
	default:
		p.endpoint(w, r, entry.app, appName, strings.TrimSuffix(r.URL.Path, pathSep))
	}
}

func (p *Pages) endpoint(w http.ResponseWriter, r *http.Request, a *application.Application, name, rest string) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "the page could not be served", http.StatusInternalServerError)
		return
	}
	p.Server.ServePage(w, r, ephttp.PageRoute{
		Application: a.ID, Name: name, Rest: rest, Nonce: base64.RawStdEncoding.EncodeToString(raw[:]),
		Caller: func(req *http.Request) (*eprun.Caller, error) { return p.caller(req, a.Name) },
		SignIn: func(rw http.ResponseWriter, req *http.Request) { p.signIn(rw, req, a.Name) },
		// The application's own cookies (#2555), withheld from a request that
		// changes something and does not prove it came from this origin:
		// sibling page origins are one site, so SameSite cannot tell them apart.
		CookiePrefix: p.cookiePrefix(), Secure: p.Secure,
		CookiesWithheld: r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r),
	})
}

// cookiePrefix is what an application's cookies are written under on its
// origins: __Host- over https, which no sibling origin can set.
func (p *Pages) cookiePrefix() string {
	if p.Secure {
		return eprun.CookiePrefixSecure
	}
	return eprun.CookiePrefixPlain
}

// caller is who the origin's cookie names, nil for nobody. A request that
// changes something must prove it came from this origin: a sibling page
// origin under one pages domain is same-site, so SameSite alone would let one
// application's script act as a visitor to another.
func (p *Pages) caller(r *http.Request, app string) (*eprun.Caller, error) {
	v, ok := p.openViewer(r, app)
	if !ok {
		return nil, nil //nolint:nilnil // nobody
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
		return nil, ephttp.ErrForbidden
	}
	return &eprun.Caller{UserID: v.UserID, Email: v.Email, AuthType: v.AuthType, Admin: v.Admin, Persona: v.Persona}, nil
}

func (p *Pages) openViewer(r *http.Request, app string) (epform.Viewer, bool) {
	c, err := r.Cookie(p.cookieName(cookieViewer))
	if err != nil {
		return epform.Viewer{}, false
	}
	v, err := epform.OpenViewer(p.viewerKey(), viewerScope+app, c.Value, p.now())
	return v, err == nil
}

// sameOrigin reports whether the browser says the request came from the
// origin it is sent to.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// viewer tells a page's script who is signed in on this origin.
func (p *Pages) viewer(w http.ResponseWriter, r *http.Request, app string) {
	w.Header().Set(hdrCache, noStore)
	v, ok := p.openViewer(r, app)
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "nobody is signed in on this origin")
		return
	}
	w.Header().Set(hdrType, "application/json")
	_ = wirejson.Encode(w, map[string]string{"email": v.Email, "user_id": v.UserID, "persona": v.Persona})
}

// signIn starts the handoff for a browser with no viewer: a state cookie on
// this origin, and a redirect to the portal naming only its digest, so the
// value that proves this browser started the handoff is never in a URL.
func (p *Pages) signIn(w http.ResponseWriter, r *http.Request, app string) {
	if p.PortalURL == "" || len(p.FormKey) == 0 {
		httpjson.WriteError(w, http.StatusUnauthorized, "this page needs a signed-in viewer, and this deployment cannot sign one in")
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the sign-in could not be started")
		return
	}
	state := hex.EncodeToString(raw[:])
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the origins' scheme
		Name: p.cookieName(cookieState), Value: state, Path: pathSep, MaxAge: stateTTLSeconds,
		HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{
		"application": {app}, "host": {r.Host}, queryPath: {r.URL.RequestURI()}, "state": {digest(state)},
	}
	w.Header().Set(hdrCache, noStore)
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, strings.TrimSuffix(p.PortalURL, pathSep)+HandoffPath+"?"+q.Encode(), http.StatusFound)
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// isLocalURL reports whether a redirect to p stays on the origin it is sent
// from: an absolute path whose second character cannot make a browser read it
// as a host, with nothing in it that could end the header.
func isLocalURL(p string) bool {
	if p == "" || p[0] != '/' {
		return false
	}
	if len(p) > 1 && (p[1] == '/' || p[1] == '\\') {
		return false
	}
	return !strings.ContainsAny(p, "\\\r\n\t")
}

// originHost is the host to send a person back to for an application, read
// from the stored application or hostname and the configured port and never
// from the request, which only selects it. Empty when host is not one of that
// application's origins.
func (p *Pages) originHost(ctx context.Context, host, app string) string {
	if !hostShape.MatchString(host) || app == "" {
		return ""
	}
	entry := p.application(ctx, app)
	if entry == nil {
		return ""
	}
	stored := p.storedHost(ctx, site.NormalizeHost(host), entry.app.Name)
	if stored == "" || p.PublicPort == "" {
		return stored
	}
	return stored + ":" + p.PublicPort
}

// storedHost is the record naming h as an origin of the application, "" for
// none.
func (p *Pages) storedHost(ctx context.Context, h, app string) string {
	if pageHost := app + "." + p.PagesDomain(); h == pageHost {
		return pageHost
	}
	if p.RouteHost == nil {
		return ""
	}
	route, err := p.RouteHost(ctx, h)
	if err != nil || route.Application != app {
		return ""
	}
	return route.Hostname
}

// Handoff is the portal route that hands a signed-in person to a page origin:
// a code good for a minute, once, by the browser holding the state it names.
func (p *Pages) Handoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(hdrCache, noStore)
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	app, host, target, state := q.Get("application"), strings.ToLower(q.Get("host")), q.Get(queryPath), q.Get("state")
	if !isLocalURL(target) {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in link is not valid")
		return
	}
	origin := p.originHost(r.Context(), host, app)
	if !stateShape.MatchString(state) || origin == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in link is not valid")
		return
	}
	caller, err := p.Authenticate(r)
	switch {
	case errors.Is(err, ephttp.ErrForbidden):
		httpjson.WriteError(w, http.StatusForbidden, "this account does not reach this application")
		return
	case err != nil || caller == nil:
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
		http.Redirect(w, r, epformpage.LoginURL(r.URL.RequestURI()), http.StatusFound) // #nosec G710 -- the portal's fixed login path; the request URI is only its URL-encoded return_to value
		return
	}
	code, err := epform.SignViewerFor(p.handoffKey(), handoffScope+app+":"+state, epform.Viewer{
		UserID: caller.UserID, Email: caller.Email, AuthType: caller.AuthType, Admin: caller.Admin, Persona: caller.Persona,
	}, p.now(), handoffTTL)
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "this deployment cannot sign a viewer in")
		return
	}
	scheme := "http"
	if p.Secure {
		scheme = schemeHTTPS
	}
	back := url.URL{Scheme: scheme, Host: origin, Path: pathAuth, RawQuery: url.Values{queryCode: {code}, queryPath: {target}}.Encode()}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, back.String(), http.StatusFound) // #nosec G710 -- origin is read from stored records, and target passed isLocalURL above
}

// completeHandoff trades a code for the origin's viewer cookie. A code opens
// only for this application, only in the browser holding the state it was
// issued for, and only once.
func (p *Pages) completeHandoff(w http.ResponseWriter, r *http.Request, app string) {
	w.Header().Set(hdrCache, noStore)
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	code, target := q.Get(queryCode), q.Get(queryPath)
	state, err := r.Cookie(p.cookieName(cookieState))
	if err != nil || code == "" || !isLocalURL(target) {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in could not be completed; open the page again")
		return
	}
	v, err := epform.OpenViewer(p.handoffKey(), handoffScope+app+":"+digest(state.Value), code, p.now())
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in could not be completed; open the page again")
		return
	}
	fresh, err := p.UseHandoff(r.Context(), digest(code), p.now().Add(2*handoffTTL))
	if err != nil || !fresh {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in was already used; open the page again")
		return
	}
	token, err := epform.SignViewer(p.viewerKey(), viewerScope+app, v, p.now())
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "this deployment cannot sign a viewer in")
		return
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the origins' scheme
		Name: p.cookieName(cookieViewer), Value: token, Path: pathSep, MaxAge: int(epform.ViewerTTL.Seconds()),
		HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
	})
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the origins' scheme
		Name: p.cookieName(cookieState), Value: "", Path: pathSep, MaxAge: -1, HttpOnly: true, Secure: p.Secure, SameSite: http.SameSiteLaxMode,
	})
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, target, http.StatusFound) // #nosec G710 -- target passed isLocalURL above, so it stays on this origin
}

// static serves one of the application's assets at the version its release
// pins, under a sandbox policy: as a subresource it loads normally, and opened
// directly it runs nothing on the origin.
func (p *Pages) static(w http.ResponseWriter, r *http.Request, entry *appEntry, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "a static file answers GET and HEAD", http.StatusMethodNotAllowed)
		return
	}
	as := entry.assets[name]
	if as == nil {
		http.NotFound(w, r)
		return
	}
	cacheKey := fmt.Sprintf("asset/%s/%d", as.ID, as.CurrentVersion)
	body, err := p.gateway.bytesFrom(r.Context(), as.S3Bucket, cacheKey, as.S3Key)
	if err != nil {
		p.gateway.unreadable(w, r, err)
		return
	}
	h := w.Header()
	h.Set(hdrType, as.ContentType)
	h.Set("Content-Security-Policy", staticPolicy)
	h.Set(hdrCache, staticCache)
	h.Set("ETag", fmt.Sprintf(`"%s-%d"`, as.ID, as.CurrentVersion))
	serveBytes(w, r, body)
}

// siteEndpoint answers an application's endpoint on the host of a site
// environment that is a part of it (#2604), run as the person the environment
// let in, or as nobody on a public environment. Every request that names the
// viewer must come from this origin, whatever its method: every site and
// environment under the pages domain is one site to a browser, so a sibling's
// page could otherwise fire this environment's endpoints as its viewer.
func (p *Pages) siteEndpoint(w http.ResponseWriter, r *http.Request, appName string, viewer *epform.Viewer, access *Access) {
	entry := p.application(r.Context(), appName)
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "the page could not be served", http.StatusInternalServerError)
		return
	}
	name, rest, found := strings.Cut(strings.TrimPrefix(r.URL.Path, prefixEP), "/")
	if found {
		rest = pathSep + rest
	}
	p.Server.ServePage(w, r, ephttp.PageRoute{
		Application: entry.app.ID, Name: name, Rest: rest, Nonce: base64.RawStdEncoding.EncodeToString(raw[:]),
		Caller: func(req *http.Request) (*eprun.Caller, error) { return siteCaller(req, viewer, access) },
		SignIn: func(rw http.ResponseWriter, _ *http.Request) {
			httpjson.WriteError(rw, http.StatusUnauthorized, "this endpoint needs a signed-in person, and this site is public; make the environment private to sign people in")
		},
		// The application's own cookies (#2555) on the site's address too, so a
		// site calling its application's JSON endpoints keeps a session: the
		// same prefix and the same refusal of a cross-origin write as on the
		// application's page origin.
		CookiePrefix: p.cookiePrefix(), Secure: p.Secure,
		CookiesWithheld: r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r),
	})
}

// siteCaller is who a site environment's endpoint runs as. Administrator is
// read from the live persona, never from the cookie.
func siteCaller(r *http.Request, viewer *epform.Viewer, access *Access) (*eprun.Caller, error) {
	if viewer == nil {
		return nil, nil //nolint:nilnil // nobody
	}
	if !fromThisOrigin(r) {
		return nil, ephttp.ErrForbidden
	}
	admin := access != nil && access.IsAdmin != nil && access.IsAdmin(viewer.Persona)
	return &eprun.Caller{UserID: viewer.UserID, Email: viewer.Email, AuthType: "browser_session", Admin: admin, Persona: viewer.Persona}, nil
}

// fromThisOrigin reports whether the browser says the request came from this
// origin, or was typed or bookmarked by the person.
func fromThisOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		return sameOrigin(r)
	}
	return false
}
