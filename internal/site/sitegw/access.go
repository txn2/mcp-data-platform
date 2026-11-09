package sitegw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epformpage"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// The routes a site's own address keeps for itself. They are answered before
// any file, so a release holding one of these paths cannot stand in for them.
const (
	pathSiteAuth    = "/_auth"
	pathSiteSignIn  = "/_signin"
	pathSiteSignOut = "/_signout"
	// SiteHandoffPath is the portal route that hands a signed-in person to a
	// site's own address.
	SiteHandoffPath = "/sites/handoff"
)

const (
	siteViewerKeyLabel  = "site-viewer-v1"
	siteHandoffKeyLabel = "site-handoff-v1"
	cookieSiteViewer    = "site_viewer"
	cookieSiteState     = "site_state"
	siteViewerScope     = "site:"
	siteHandoffScope    = "site-handoff:"
	// siteViewerTTL is how long a sign-in lasts. Grants are read on every
	// request whatever this is; this bounds how long a role change at the
	// identity provider takes to be seen.
	siteViewerTTL = time.Hour

	hdrRobots  = "X-Robots-Tag"
	noIndex    = "noindex"
	hdrCSP     = "Content-Security-Policy"
	hdrReferer = "Referrer-Policy"
	noReferrer = "no-referrer"
	pagePolicy = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	htmlType   = "text/html; charset=utf-8"

	// CachePrivate makes every reuse of a file come back and be checked, so
	// someone whose access was removed cannot keep reading from a cache.
	cachePrivate = "private, no-cache"
)

// AccessStore reads what decides whether someone may open a site.
type AccessStore interface {
	SiteAccess(ctx context.Context, siteID, email string) (*site.Access, error)
	RouteOwn(ctx context.Context, siteName string) (*site.Route, error)
	UseHandoff(ctx context.Context, codeHash string, expires time.Time) (bool, error)
}

// SiteViewer is who a portal browser session belongs to.
type SiteViewer struct {
	UserID string
	Email  string
	// Persona is the persona their roles resolve to.
	Persona string
	// EmailUnverified is true when the identity provider said it has not
	// verified Email.
	EmailUnverified bool
}

// Access guards every site's own address: a sign-in that hands a portal session
// to this one hostname, and a check on every request of who may open it.
type Access struct {
	Store AccessStore
	// Session reads the portal's browser session and nothing else, so an API
	// key never signs anyone in to a site. Nil with no error is nobody.
	Session func(r *http.Request) (*SiteViewer, error)
	// IsAdmin and IsExternal read the live persona registry.
	IsAdmin    func(persona string) bool
	IsExternal func(persona string) bool
	// FormKey is what the two token keys derive from; nil signs nobody in.
	FormKey     []byte
	PortalURL   string
	PagesDomain func() string
	// PublicPort is the port the origins answer on, "" for the scheme's own.
	PublicPort string
	Secure     bool
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (a *Access) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Access) cookieName(base string) string {
	if a.Secure {
		return securePrefix + base
	}
	return base
}

func (a *Access) viewerKey() []byte {
	if len(a.FormKey) == 0 {
		return nil
	}
	return shareguest.DeriveKey(a.FormKey, siteViewerKeyLabel)
}

func (a *Access) handoffKey() []byte {
	if len(a.FormKey) == 0 {
		return nil
	}
	return shareguest.DeriveKey(a.FormKey, siteHandoffKeyLabel)
}

// allow answers a request to a site's own address and reports whether it may
// go on to the site's files. Every path and method comes through here, and
// nothing it writes holds any of the release.
func (a *Access) allow(w http.ResponseWriter, r *http.Request, route *site.Route) bool {
	h := w.Header()
	h.Set(hdrRobots, noIndex)
	h.Set(hdrNosniff, nosniff)
	switch r.URL.Path {
	case pathSiteAuth:
		a.completeHandoff(w, r, route)
		return false
	case pathSiteSignIn:
		a.signIn(w, r, route)
		return false
	case pathSiteSignOut:
		a.signOut(w, r)
		return false
	}
	v, ok := a.openViewer(r, route)
	if !ok {
		a.page(w, r, http.StatusUnauthorized, signInView{Host: site.NormalizeHost(r.Host), Target: r.URL.RequestURI()})
		return false
	}
	verdict, err := a.decide(r.Context(), route, v)
	if err != nil {
		slog.ErrorContext(r.Context(), "sites: reading access failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		h.Set(hdrCache, noStore)
		http.Error(w, "this site could not be read", http.StatusServiceUnavailable)
		return false
	}
	if verdict != verdictOpen {
		a.page(w, r, http.StatusForbidden, noAccessView{
			Host: site.NormalizeHost(r.Host), Email: v.Email, Unverified: verdict == verdictUnverified,
		})
		return false
	}
	h.Add(hdrVary, "Cookie")
	// Set before anything is answered, so a redirect, a 404 and a refused
	// method are held to it as a file is.
	h.Set(hdrCache, cachePrivate)
	return true
}

func (a *Access) openViewer(r *http.Request, route *site.Route) (epform.Viewer, bool) {
	c, err := r.Cookie(a.cookieName(cookieSiteViewer))
	if err != nil {
		return epform.Viewer{}, false
	}
	v, err := epform.OpenViewer(a.viewerKey(), siteViewerScope+route.SiteID, c.Value, a.now())
	return v, err == nil
}

type verdict int

const (
	verdictRefused verdict = iota
	verdictOpen
	verdictUnverified
)

// decide reads, fresh, whether the person may open the site. Administrator and
// external are read from the live persona, never from the cookie.
func (a *Access) decide(ctx context.Context, route *site.Route, v epform.Viewer) (verdict, error) {
	if a.IsAdmin != nil && a.IsAdmin(v.Persona) {
		return verdictOpen, nil
	}
	access, err := a.Store.SiteAccess(ctx, route.SiteID, v.Email)
	if err != nil {
		return verdictRefused, err //nolint:wrapcheck // logged by the caller
	}
	if access.InternalView && a.IsExternal != nil && !a.IsExternal(v.Persona) {
		return verdictOpen, nil
	}
	return byGrant(access, v), nil
}

// byGrant is the verdict of the person's own grant or ownership.
func byGrant(access *site.Access, v epform.Viewer) verdict {
	matched := access.Level != "" || (v.Email != "" && strings.EqualFold(v.Email, access.OwnerEmail))
	switch {
	case !matched:
		return verdictRefused
	case v.EmailUnverified:
		return verdictUnverified
	}
	return verdictOpen
}

// signIn starts the handoff: a state cookie on this hostname and a redirect
// to the portal naming only its digest.
func (a *Access) signIn(w http.ResponseWriter, r *http.Request, route *site.Route) {
	w.Header().Set(hdrCache, noStore)
	target := r.URL.Query().Get(queryPath)
	if !isLocalURL(target) {
		target = pathSep
	}
	if a.PortalURL == "" || len(a.FormKey) == 0 {
		http.Error(w, "this deployment cannot sign anyone in to a site", http.StatusServiceUnavailable)
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "the sign-in could not be started", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(raw[:])
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the origins' scheme
		Name: a.cookieName(cookieSiteState), Value: state, Path: pathSep, MaxAge: stateTTLSeconds,
		HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{"site": {route.Site}, queryPath: {target}, "state": {digest(state)}}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, strings.TrimSuffix(a.PortalURL, pathSep)+SiteHandoffPath+"?"+q.Encode(), http.StatusFound)
}

// Handoff is the portal route: it reads the person's portal session and sends
// them back to the site's own address, read from the stored site and never from
// the request, with a one-time code. Having a session is all it needs: whether
// the person may open the site is decided on every request there, which is what
// lets an external person with no Sites area in.
func (a *Access) Handoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(hdrCache, noStore)
	w.Header().Set(hdrReferer, noReferrer)
	q := r.URL.Query()
	name, target, state := q.Get("site"), q.Get(queryPath), q.Get("state")
	if !isLocalURL(target) || !stateShape.MatchString(state) {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in link is not valid")
		return
	}
	domain := a.PagesDomain()
	route, err := a.Store.RouteOwn(r.Context(), name)
	if err != nil || domain == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in link is not valid")
		return
	}
	viewer, err := a.Session(r)
	if err != nil || viewer == nil {
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
		http.Redirect(w, r, epformpage.LoginURL(r.URL.RequestURI()), http.StatusFound) // #nosec G710 -- the portal's fixed login path; the request URI is only its URL-encoded return_to value
		return
	}
	code, err := epform.SignViewerFor(a.handoffKey(), siteHandoffScope+route.SiteID+":"+state, epform.Viewer{
		UserID: viewer.UserID, Email: viewer.Email, Persona: viewer.Persona, EmailUnverified: viewer.EmailUnverified,
	}, a.now(), handoffTTL)
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "this deployment cannot sign anyone in to a site")
		return
	}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, a.backTo(route.Site, domain, code, target), http.StatusFound) // #nosec G710 -- the host is built from the stored site name and the configured domain, and target passed isLocalURL above
}

func (a *Access) backTo(siteName, domain, code, target string) string {
	host := siteName + "." + domain
	if a.PublicPort != "" {
		host += ":" + a.PublicPort
	}
	scheme := "http"
	if a.Secure {
		scheme = schemeHTTPS
	}
	back := url.URL{Scheme: scheme, Host: host, Path: pathSiteAuth, RawQuery: url.Values{queryCode: {code}, queryPath: {target}}.Encode()}
	return back.String()
}

// completeHandoff trades a code for this hostname's cookie. A code opens only
// for this site, only in the browser holding the state it was issued for, and
// only once.
func (a *Access) completeHandoff(w http.ResponseWriter, r *http.Request, route *site.Route) {
	h := w.Header()
	h.Set(hdrCache, noStore)
	h.Set(hdrReferer, noReferrer)
	q := r.URL.Query()
	code, target := q.Get(queryCode), q.Get(queryPath)
	state, err := r.Cookie(a.cookieName(cookieSiteState))
	if err != nil || code == "" || !isLocalURL(target) {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in could not be completed; open the page again")
		return
	}
	v, err := epform.OpenViewer(a.handoffKey(), siteHandoffScope+route.SiteID+":"+digest(state.Value), code, a.now())
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in could not be completed; open the page again")
		return
	}
	fresh, err := a.Store.UseHandoff(r.Context(), digest(code), a.now().Add(2*handoffTTL))
	if err != nil || !fresh {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in was already used; open the page again")
		return
	}
	token, err := epform.SignViewerFor(a.viewerKey(), siteViewerScope+route.SiteID, v, a.now(), siteViewerTTL)
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "this deployment cannot sign anyone in to a site")
		return
	}
	a.setCookie(w, cookieSiteViewer, token, int(siteViewerTTL.Seconds()))
	a.setCookie(w, cookieSiteState, "", -1)
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, target, http.StatusFound) // #nosec G710 -- target passed isLocalURL above, so it stays on this origin
}

func (a *Access) setCookie(w http.ResponseWriter, base, value string, maxAge int) {
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the origins' scheme
		Name: a.cookieName(base), Value: value, Path: pathSep, MaxAge: maxAge, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// signOut forgets this hostname's sign-in and offers the portal's sign-in, for
// "Use another account".
func (a *Access) signOut(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(hdrCache, noStore)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "signing out takes a POST", http.StatusMethodNotAllowed)
		return
	}
	a.setCookie(w, cookieSiteViewer, "", -1)
	http.Redirect(w, r, strings.TrimSuffix(a.PortalURL, pathSep)+"/portal/auth/logout", http.StatusSeeOther)
}

// signInView and noAccessView are the only pages a site's own address answers
// to someone it does not open for.
type signInView struct{ Host, Target string }

type noAccessView struct {
	Host, Email string
	Unverified  bool
}

var (
	signInPage = template.Must(template.New("signin").Parse(pageHead + `<title>Sign in to view this preview</title>` + pageOpen +
		`<h1>Sign in to view this preview</h1><p class="m">{{.Host}}</p>` +
		`<p>You need an account that the site's owner has given access.</p>` +
		`<form method="get" action="` + pathSiteSignIn + `"><input type="hidden" name="path" value="{{.Target}}"><button>Sign in</button></form>` + pageClose))
	noAccessPage = template.Must(template.New("noaccess").Parse(pageHead + `<title>No access to this preview</title>` + pageOpen +
		`<h1>You don't have access to this preview</h1><p class="m">{{.Host}}</p>` +
		`{{if .Unverified}}<p>You are signed in as {{.Email}}, and your sign-in provider has not verified that email address. Ask your administrator to verify it, then sign in again.</p>` +
		`{{else}}<p>You are signed in as {{.Email}}. Ask the person who sent you this link to give this account access.</p>{{end}}` +
		`<form method="post" action="` + pathSiteSignOut + `"><button>Use another account</button></form>` + pageClose))
)

const (
	pageHead = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex">` +
		`<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:16px;box-sizing:border-box;font:14px/20px system-ui,sans-serif;background:#fff;color:#0f172a}` +
		`main{width:100%;max-width:420px;padding:24px;border:1px solid #e2e8f0;border-radius:8px;box-sizing:border-box}h1{font-size:18px;line-height:24px;font-weight:600;margin:0 0 16px}p{margin:0 0 16px;color:#64748b;overflow-wrap:anywhere}.m{font-family:ui-monospace,monospace}` +
		`button{width:100%;height:36px;border:0;border-radius:6px;background:#2563eb;color:#fff;font:inherit;font-weight:500;cursor:pointer}` +
		`@media (prefers-color-scheme:dark){body{background:#020617;color:#f8fafc}main{border-color:#1e293b}p{color:#94a3b8}}</style>`
	pageOpen  = `</head><body><main>`
	pageClose = `</main></body></html>`
)

func (*Access) page(w http.ResponseWriter, r *http.Request, status int, view any) {
	h := w.Header()
	h.Set(hdrType, htmlType)
	h.Set(hdrCache, noStore)
	h.Set(hdrCSP, pagePolicy)
	h.Set(hdrReferer, noReferrer)
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	tmpl := signInPage
	if _, ok := view.(noAccessView); ok {
		tmpl = noAccessPage
	}
	if err := tmpl.Execute(w, view); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.WarnContext(r.Context(), "sites: writing a sign-in page failed", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}
