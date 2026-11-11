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
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// The routes a site's own address keeps for itself. They are answered before
// any file, so a release holding one of these paths cannot stand in for them.
const (
	pathSiteAuth    = "/_auth"
	pathSiteSignIn  = "/_signin"
	pathSiteSignOut = "/_signout"
	// portalLogoutPath is the portal's own sign-out, where "Use another account"
	// ends up.
	portalLogoutPath = "/portal/auth/logout"
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

// AccessStore reads what decides whether someone may open an environment.
type AccessStore interface {
	SiteAccess(ctx context.Context, siteID, env, email, persona string) (*site.Access, error)
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
	// Roles are the roles the session holds, which the persona is worked out
	// from again on every request.
	Roles []string
	// Name is the display name the directory holds, "" for none.
	Name string
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
	// PersonaFor works out the persona roles resolve to in the live registry;
	// nil keeps the persona the person signed in with.
	PersonaFor func(roles []string) string
	// FormKey is what the two token keys derive from; nil signs nobody in.
	FormKey     []byte
	PortalURL   string
	PagesDomain func() string
	// PublicPort is the port the origins answer on, "" for the scheme's own.
	PublicPort string
	Secure     bool
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// resolve is what a hostname serves, set by the gateway; the handoff
	// reads where it may send someone through it.
	resolve func(ctx context.Context, host string) (*site.Route, error)
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

// allow answers a request to a private environment and reports whether it
// may go on, with the viewer it may go on as. Every path and method comes
// through here, and nothing it writes holds any of the release.
func (a *Access) allow(w http.ResponseWriter, r *http.Request, route *site.Route) (epform.Viewer, bool) {
	h := w.Header()
	h.Set(hdrRobots, noIndex)
	h.Set(hdrNosniff, nosniff)
	switch r.URL.Path {
	case pathSiteAuth:
		a.completeHandoff(w, r, route)
		return epform.Viewer{}, false
	case pathSiteSignIn:
		a.signIn(w, r, route)
		return epform.Viewer{}, false
	case pathSiteSignOut:
		a.signOut(w, r)
		return epform.Viewer{}, false
	}
	v, ok := a.openViewer(r, route)
	if !ok {
		a.noSignIn(w, r, route)
		return epform.Viewer{}, false
	}
	v.Persona = a.livePersona(v)
	verdict, err := a.decide(r.Context(), route, v)
	if err != nil {
		slog.ErrorContext(r.Context(), "sites: reading access failed", logKeyError, logsan.SanitizeForLog(err.Error()))
		h.Set(hdrCache, noStore)
		http.Error(w, "this site could not be read", http.StatusServiceUnavailable)
		return epform.Viewer{}, false
	}
	if verdict != verdictOpen {
		a.page(w, r, http.StatusForbidden, noAccessView{
			Host: site.NormalizeHost(r.Host), Email: v.Email, Unverified: verdict == verdictUnverified,
		})
		return epform.Viewer{}, false
	}
	h.Add(hdrVary, "Cookie")
	// Set before anything is answered, so a redirect, a 404 and a refused
	// method are held to it as a file is.
	h.Set(hdrCache, cachePrivate)
	return v, true
}

// siteViewerScopeFor binds a sign-in to one site and one hostname, so a token taken
// from one environment's address opens no other.
func siteViewerScopeFor(siteID, host string) string { return siteViewerScope + siteID + ":" + host }

func (a *Access) openViewer(r *http.Request, route *site.Route) (epform.Viewer, bool) {
	c, err := r.Cookie(a.cookieName(cookieSiteViewer))
	if err != nil {
		return epform.Viewer{}, false
	}
	v, err := epform.OpenViewer(a.viewerKey(), siteViewerScopeFor(route.SiteID, site.NormalizeHost(r.Host)), c.Value, a.now())
	return v, err == nil
}

// livePersona is the persona the viewer's roles resolve to now, so a change
// to a persona applies on their next request.
func (a *Access) livePersona(v epform.Viewer) string {
	if a.PersonaFor == nil {
		return v.Persona
	}
	return a.PersonaFor(v.Roles)
}

type verdict int

const (
	verdictRefused verdict = iota
	verdictOpen
	verdictUnverified
)

// decide reads, fresh, whether the person may open the environment.
// Administrator, external and persona grants are read against v.Persona,
// worked out from the live registry, never from the cookie.
func (a *Access) decide(ctx context.Context, route *site.Route, v epform.Viewer) (verdict, error) {
	if a.IsAdmin != nil && a.IsAdmin(v.Persona) {
		return verdictOpen, nil
	}
	access, err := a.Store.SiteAccess(ctx, route.SiteID, route.Environment, v.Email, v.Persona)
	if err != nil {
		return verdictRefused, err //nolint:wrapcheck // logged by the caller
	}
	if access.ByPersona {
		return verdictOpen, nil
	}
	if access.InternalView && a.IsExternal != nil && v.Persona != "" && !a.IsExternal(v.Persona) {
		return verdictOpen, nil
	}
	return byGrant(access, v), nil
}

// byGrant is the verdict of a grant to the person's email, or ownership.
func byGrant(access *site.Access, v epform.Viewer) verdict {
	matched := access.ByEmail || (v.Email != "" && strings.EqualFold(v.Email, access.OwnerEmail))
	switch {
	case !matched:
		return verdictRefused
	case v.EmailUnverified:
		return verdictUnverified
	}
	return verdictOpen
}

// noSignIn answers a request that carries no valid sign-in for this site. A GET
// or HEAD goes straight into the sign-in, so someone already signed in to the
// portal never stops here. Any other method cannot be carried across a redirect
// and gets the page.
func (a *Access) noSignIn(w http.ResponseWriter, r *http.Request, route *site.Route) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		a.startSignIn(w, r, route, r.URL.RequestURI())
		return
	}
	a.page(w, r, http.StatusUnauthorized, signInView{Host: site.NormalizeHost(r.Host), Target: r.URL.RequestURI()})
}

// signIn is the /_signin route: the sign-in, back to the path the query names.
func (a *Access) signIn(w http.ResponseWriter, r *http.Request, route *site.Route) {
	a.startSignIn(w, r, route, r.URL.Query().Get(queryPath))
}

// startSignIn starts the handoff: a state cookie on this hostname and a
// redirect to the portal naming only its digest. A target that would leave the
// site returns to its root.
func (a *Access) startSignIn(w http.ResponseWriter, r *http.Request, route *site.Route, target string) {
	h := w.Header()
	h.Set(hdrCache, noStore)
	h.Set(hdrReferer, noReferrer)
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
	q := url.Values{"site": {route.Site}, queryHost: {site.NormalizeHost(r.Host)}, queryPath: {target}, "state": {digest(state)}}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, strings.TrimSuffix(a.PortalURL, pathSep)+SiteHandoffPath+"?"+q.Encode(), http.StatusFound)
}

// Handoff is the portal route: it reads the person's portal session and sends
// them back to the hostname the sign-in started on, with a one-time code. The
// hostname in the request only chooses: it must be one the store says serves
// a private environment of the named site, on the pages domain or an added
// domain, or nothing is sent anywhere. Having a session is all it needs:
// whether the person may open the environment is decided on every request
// there, which is what lets an external person with no Sites area in.
func (a *Access) Handoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(hdrCache, noStore)
	w.Header().Set(hdrReferer, noReferrer)
	q := r.URL.Query()
	name, target, state := q.Get("site"), q.Get(queryPath), q.Get("state")
	if !isLocalURL(target) || !stateShape.MatchString(state) {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in link is not valid")
		return
	}
	host, route := a.handoffHost(r.Context(), name, q.Get(queryHost))
	if route == nil {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in link is not valid")
		return
	}
	viewer, err := a.Session(r)
	if err != nil || viewer == nil {
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
		http.Redirect(w, r, epformpage.LoginURL(r.URL.RequestURI()), http.StatusFound) // #nosec G710 -- the portal's fixed login path; the request URI is only its URL-encoded return_to value
		return
	}
	code, err := epform.SignViewerFor(a.handoffKey(), siteHandoffScopeFor(route.SiteID, host, state), epform.Viewer{
		UserID: viewer.UserID, Email: viewer.Email, Persona: viewer.Persona, EmailUnverified: viewer.EmailUnverified,
		Roles: viewer.Roles, Name: viewer.Name,
	}, a.now(), handoffTTL)
	if err != nil {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "this deployment cannot sign anyone in to a site")
		return
	}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect
	http.Redirect(w, r, a.backTo(host, code, target), http.StatusFound) // #nosec G710 -- the host resolved, through the store, to a private environment of the named site, and target passed isLocalURL above
}

// handoffHost is the hostname a sign-in may return to and what it serves, or
// nil when the hostname is not a private environment of the named site.
func (a *Access) handoffHost(ctx context.Context, siteName, host string) (string, *site.Route) {
	host = site.NormalizeHost(host)
	if a.resolve == nil || siteName == "" || site.ValidateHostname(host) != nil {
		return "", nil
	}
	route, err := a.resolve(ctx, host)
	if err != nil || route == nil || route.Application != "" || !route.Private || route.Site != siteName {
		return "", nil
	}
	return host, route
}

// siteHandoffScopeFor binds a code to the site, the hostname and the browser that
// started the sign-in.
func siteHandoffScopeFor(siteID, host, state string) string {
	return siteHandoffScope + siteID + ":" + host + ":" + state
}

func (a *Access) backTo(host, code, target string) string {
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
	host := site.NormalizeHost(r.Host)
	v, err := epform.OpenViewer(a.handoffKey(), siteHandoffScopeFor(route.SiteID, host, digest(state.Value)), code, a.now())
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in could not be completed; open the page again")
		return
	}
	fresh, err := a.Store.UseHandoff(r.Context(), digest(code), a.now().Add(2*handoffTTL))
	if err != nil || !fresh {
		httpjson.WriteError(w, http.StatusBadRequest, "this sign-in was already used; open the page again")
		return
	}
	token, err := epform.SignViewerFor(a.viewerKey(), siteViewerScopeFor(route.SiteID, host), v, a.now(), siteViewerTTL)
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

// signOut forgets this hostname's sign-in, for "Use another account", and
// answers a page that goes on to the portal's sign-out. It is not a redirect:
// the form posting here is held to form-action 'self', and Chromium applies
// that to where a form submission is redirected.
func (a *Access) signOut(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(hdrCache, noStore)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "signing out takes a POST", http.StatusMethodNotAllowed)
		return
	}
	a.setCookie(w, cookieSiteViewer, "", -1)
	a.page(w, r, http.StatusOK, signedOutView{Next: strings.TrimSuffix(a.PortalURL, pathSep) + portalLogoutPath})
}

// signInView, noAccessView and signedOutView are the only pages a site's own
// address answers to someone it does not open for.
type signInView struct{ Host, Target string }

// signedOutView names where the page goes next, read from configuration and
// never from the request.
type signedOutView struct{ Next string }

type noAccessView struct {
	Host, Email string
	Unverified  bool
}

var (
	signInPage = template.Must(template.New("signin").Parse(pageHead + `<title>Sign in to open this site</title>` + pageOpen +
		`<h1>Sign in to open this site</h1><p class="m">{{.Host}}</p>` +
		`<p>This site is private. Sign in with your platform account; you come back here afterwards.</p>` +
		`<a class="go" href="` + pathSiteSignIn + `?path={{.Target}}">Sign in</a>` + pageClose))
	noAccessPage = template.Must(template.New("noaccess").Parse(pageHead + `<title>No access to this site</title>` + pageOpen +
		`<h1>You don't have access to this site</h1><p class="m">{{.Host}}</p>` +
		`{{if .Unverified}}<p>You are signed in as {{.Email}}, and your sign-in provider has not verified that email address. Ask your administrator to verify it, then sign in again.</p>` +
		`{{else}}<p>You are signed in as {{.Email}}, and this address is not shared with you. Ask the person who sent you the link to share it with this account.</p>{{end}}` +
		`<form method="post" action="` + pathSiteSignOut + `"><button>Use another account</button></form>` + pageClose))
	signedOutPage = template.Must(template.New("signedout").Parse(pageHead + `<meta http-equiv="refresh" content="0;url={{.Next}}"><title>Signing out</title>` + pageOpen +
		`<h1>Signing out</h1><p>This site has forgotten your sign-in.</p><a class="go" href="{{.Next}}">Continue</a>` + pageClose))
)

const (
	pageHead = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex">` +
		`<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:16px;box-sizing:border-box;font:14px/20px system-ui,sans-serif;background:#fff;color:#0f172a}` +
		`main{width:100%;max-width:420px;padding:24px;border:1px solid #e2e8f0;border-radius:8px;box-sizing:border-box}h1{font-size:18px;line-height:24px;font-weight:600;margin:0 0 16px}p{margin:0 0 16px;color:#64748b;overflow-wrap:anywhere}.m{font-family:ui-monospace,monospace}` +
		`button,.go{display:block;width:100%;height:36px;padding:0;border:0;border-radius:6px;background:#2563eb;color:#fff;font:inherit;font-weight:500;line-height:36px;text-align:center;text-decoration:none;cursor:pointer}` +
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
	if err := pageTemplate(view).Execute(w, view); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.WarnContext(r.Context(), "sites: writing a sign-in page failed", logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// pageTemplate is the page a view is written with.
func pageTemplate(view any) *template.Template {
	switch view.(type) {
	case noAccessView:
		return noAccessPage
	case signedOutView:
		return signedOutPage
	case notFoundView:
		return notFoundPage
	default:
		return signInPage
	}
}

// writeSiteViewer tells an environment's script who is signed in: the person
// a private environment let in, or nobody on a public one. It never carries
// the cookie or anything signed.
func writeSiteViewer(w http.ResponseWriter, v *epform.Viewer) {
	w.Header().Set(hdrCache, noStore)
	if v == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, "nobody is signed in on this site")
		return
	}
	w.Header().Set(hdrType, "application/json")
	_ = wirejson.Encode(w, map[string]any{"email": v.Email, "user_id": v.UserID, "persona": v.Persona, "name": v.Name})
}

// notPublished is the answer for a hostname that serves nothing: a site's main
// address given to no environment, or an environment that does not exist. It
// says nothing about what the site has.
func notPublished(w http.ResponseWriter, r *http.Request, host string) {
	h := w.Header()
	h.Set(hdrRobots, noIndex)
	h.Set(hdrNosniff, nosniff)
	(*Access)(nil).page(w, r, http.StatusNotFound, notFoundView{Host: host})
}

type notFoundView struct{ Host string }

var notFoundPage = template.Must(template.New("notfound").Parse(pageHead + `<title>Nothing is published here</title>` + pageOpen +
	`<h1>Nothing is published here</h1><p class="m">{{.Host}}</p>` +
	`<p>Check the address, or ask whoever sent it for the current link.</p>` + pageClose))
