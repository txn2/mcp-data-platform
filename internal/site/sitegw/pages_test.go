package sitegw

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epformpage"
	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/site"
)

const (
	appShop    = "shop"
	appOther   = "other"
	hostShop   = "shop.pages.example.com"
	hostOther  = "other.pages.example.com"
	hostMapped = "shop.client.example"
	portalURL  = "https://portal.example.com"
	assetBkt   = "assets"
	cspHeader  = "Content-Security-Policy"
	viewerUser = "ann"
	viewerMail = "ann@example.com"
	stateValue = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type memApps struct {
	byName map[string]*application.Application
	err    error
	reads  int
}

func appsNamed(names ...string) map[string]*application.Application {
	out := map[string]*application.Application{}
	for _, n := range names {
		out[n] = &application.Application{ID: "app_" + n, Name: n}
	}
	return out
}

func (a *memApps) GetByName(_ context.Context, name string) (*application.Application, error) {
	a.reads++
	if a.err != nil {
		return nil, a.err
	}
	if app, ok := a.byName[name]; ok {
		return app, nil
	}
	return nil, application.ErrNotFound
}

type memAssets struct {
	byID map[string]*portaldomain.Asset
	err  map[string]error
}

func (a *memAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if err := a.err[id]; err != nil {
		return nil, err
	}
	return a.byID[id], nil
}

type memVersions struct {
	versions map[string]*portaldomain.AssetVersion
	err      error
}

func (v *memVersions) GetByVersion(_ context.Context, assetID string, version int) (*portaldomain.AssetVersion, error) {
	if v.err != nil {
		return nil, v.err
	}
	return v.versions[assetID+"/"+strconv.Itoa(version)], nil
}

// pageServer records each route it is handed and answers through fn.
type pageServer struct {
	routes []ephttp.PageRoute
	fn     func(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute)
}

func (s *pageServer) ServePage(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute) {
	s.routes = append(s.routes, route)
	if s.fn != nil {
		s.fn(w, r, route)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pageHarness drives an application's page origin through the gateway, with
// the portal's handoff route beside it.
type pageHarness struct {
	*harness
	apps     *memApps
	assets   *memAssets
	versions *memVersions
	server   *pageServer
	pages    *Pages
	clock    time.Time
	// used is the record of handoff codes, by hash.
	used    map[string]time.Time
	useErr  error
	caller  *eprun.Caller
	authErr error
}

type pageOption func(*Pages)

func newPageHarness(opts ...pageOption) *pageHarness {
	ph := &pageHarness{
		apps:     &memApps{byName: appsNamed(appShop, appOther)},
		assets:   &memAssets{byID: map[string]*portaldomain.Asset{}, err: map[string]error{}},
		versions: &memVersions{versions: map[string]*portaldomain.AssetVersion{}},
		server:   &pageServer{},
		clock:    time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
		used:     map[string]time.Time{},
		caller:   &eprun.Caller{UserID: viewerUser, Email: viewerMail, AuthType: "browser_session", Persona: "analyst"},
	}
	ph.pages = &Pages{
		Apps: ph.apps, Assets: ph.assets, Versions: ph.versions, Server: ph.server,
		Authenticate: func(*http.Request) (*eprun.Caller, error) { return ph.caller, ph.authErr },
		RouteHost: func(_ context.Context, host string) (*site.Route, error) {
			if host == hostMapped {
				return &site.Route{Application: appShop, Hostname: host}, nil
			}
			return nil, site.ErrNoDomain
		},
		UseHandoff: func(_ context.Context, codeHash string, expires time.Time) (bool, error) {
			if ph.useErr != nil {
				return false, ph.useErr
			}
			if _, seen := ph.used[codeHash]; seen {
				return false, nil
			}
			ph.used[codeHash] = expires
			return true, nil
		},
		FormKey: []byte("0123456789abcdef0123456789abcdef"), PortalURL: portalURL, PagesDomain: func() string { return testPagesDomain },
		Now: func() time.Time { return ph.clock },
	}
	for _, opt := range opts {
		opt(ph.pages)
	}
	ph.harness = newHarness(Deps{Pages: ph.pages})
	return ph
}

func secure(p *Pages) { p.Secure = true }

func (ph *pageHarness) expireApps() { ph.clock = ph.clock.Add(2 * appTTL) }

func (ph *pageHarness) addAsset(id, name string, parts *[]application.Part, body string) *portaldomain.Asset {
	a := &portaldomain.Asset{
		ID: id, Name: name, ContentType: ctCSS, S3Bucket: assetBkt, S3Key: "assets/" + id + "/v1", CurrentVersion: 1,
	}
	ph.assets.byID[id] = a
	ph.objects.bodies[objectName(assetBkt, a.S3Key)] = []byte(body)
	*parts = append(*parts, application.Part{Kind: application.KindAsset, ID: id})
	ph.apps.byName[appShop].Parts = *parts
	return a
}

func TestPages_RoutesOfAPageOrigin(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		endpoint string
		rest     string
	}{
		{name: "the root is the endpoint named after the application", target: "/", endpoint: appShop, rest: ""},
		{name: "a path under the root is that endpoint's rest", target: "/cart/7", endpoint: appShop, rest: "/cart/7"},
		{name: "a trailing slash is the same path", target: "/cart/", endpoint: appShop, rest: "/cart"},
		{name: "another endpoint by name", target: "/endpoints/orders", endpoint: "orders", rest: ""},
		{name: "another endpoint and its path", target: "/endpoints/orders/7/items", endpoint: "orders", rest: "/7/items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			rec := ph.get(hostShop, tt.target)
			assert.Equal(t, http.StatusNoContent, rec.Code)
			assert.Zero(t, ph.nextHits)
			require.Len(t, ph.server.routes, 1)
			route := ph.server.routes[0]
			assert.Equal(t, "app_shop", route.Application)
			assert.Equal(t, tt.endpoint, route.Name)
			assert.Equal(t, tt.rest, route.Rest)
			assert.NotEmpty(t, route.Nonce)
		})
	}
}

func TestPages_EveryResponseHasItsOwnNonce(t *testing.T) {
	ph := newPageHarness()
	ph.get(hostShop, "/")
	ph.get(hostShop, "/")
	require.Len(t, ph.server.routes, 2)
	assert.NotEqual(t, ph.server.routes[0].Nonce, ph.server.routes[1].Nonce)
}

func TestPages_ReservedRoutesNeverReachAnEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		target string
		status int
		body   string
	}{
		{name: "the handoff completion", target: "/_auth", status: http.StatusBadRequest, body: "could not be completed"},
		{name: "who is signed in", target: "/_viewer", status: http.StatusUnauthorized, body: "nobody is signed in"},
		{name: "a static file with no such asset", target: "/_static/nope.css", status: http.StatusNotFound, body: "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			rec := ph.get(hostShop, tt.target)
			assert.Equal(t, tt.status, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.body)
			assert.Empty(t, ph.server.routes)
			assert.Equal(t, nosniff, rec.Header().Get(hdrNosniff))
		})
	}
}

func TestPages_AnUnknownApplicationIs404(t *testing.T) {
	ph := newPageHarness()
	rec := ph.get("nope.pages.example.com", "/")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, ph.server.routes)
	assert.Zero(t, ph.nextHits)
}

func TestPages_AnApplicationThatCannotBeReadIs404(t *testing.T) {
	ph := newPageHarness()
	ph.apps.err = errors.New("database is down")
	rec := ph.get(hostShop, "/")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "database is down")
}

func TestPages_AnApplicationIsHeldAMomentByTheInjectedClock(t *testing.T) {
	ph := newPageHarness()
	ph.get(hostShop, "/")
	reads := ph.apps.reads
	ph.get(hostShop, "/")
	assert.Equal(t, reads, ph.apps.reads, "inside the moment")
	ph.expireApps()
	ph.get(hostShop, "/")
	assert.Equal(t, reads+1, ph.apps.reads, "past it")
}

func TestPages_TheTableIsDroppedAtItsBound(t *testing.T) {
	ph := newPageHarness()
	ph.pages.mu.Lock()
	ph.pages.appCache = map[string]appEntry{}
	for i := range maxRoutes {
		ph.pages.appCache["app"+strconv.Itoa(i)] = appEntry{}
	}
	ph.pages.mu.Unlock()
	ph.get(hostShop, "/")
	ph.pages.mu.Lock()
	defer ph.pages.mu.Unlock()
	assert.Len(t, ph.pages.appCache, 1)
}

func TestPages_TwoAssetsSharingANameAnswerToNeitherNameButEachToItsID(t *testing.T) {
	ph := newPageHarness()
	var parts []application.Part
	ph.addAsset("as_1", "site.css", &parts, "first")
	ph.addAsset("as_2", "site.css", &parts, "second")
	ph.addAsset("as_3", "unique.css", &parts, "third")

	assert.Equal(t, http.StatusNotFound, ph.get(hostShop, "/_static/site.css").Code)
	assert.Equal(t, "first", ph.get(hostShop, "/_static/as_1").Body.String())
	assert.Equal(t, "second", ph.get(hostShop, "/_static/as_2").Body.String())
	assert.Equal(t, "third", ph.get(hostShop, "/_static/unique.css").Body.String())
}

func TestPages_LoadAssetsSkipsWhatCannotBeServed(t *testing.T) {
	ph := newPageHarness()
	parts := make([]application.Part, 0, 2)
	gone := time.Now()
	ph.addAsset("as_del", "deleted.css", &parts, "deleted").DeletedAt = &gone
	ph.addAsset("as_ok", "ok.css", &parts, "ok")
	ph.addAsset("as_err", "err.css", &parts, "err")
	ph.assets.err["as_err"] = errors.New("database is down")
	held := ph.addAsset("as_held", "held.css", &parts, "held")
	held.ReleasedVersion, held.CurrentVersion = 2, 3
	parts = append(parts, application.Part{Kind: application.KindScript, ID: "as_ok"}, application.Part{Kind: application.KindAsset, ID: "as_absent"})
	ph.apps.byName[appShop].Parts = parts

	assert.Equal(t, http.StatusNotFound, ph.get(hostShop, "/_static/deleted.css").Code)
	assert.Equal(t, http.StatusNotFound, ph.get(hostShop, "/_static/err.css").Code)
	assert.Equal(t, http.StatusNotFound, ph.get(hostShop, "/_static/held.css").Code, "a held version that cannot be read is not replaced by the newest")
	assert.Equal(t, "ok", ph.get(hostShop, "/_static/ok.css").Body.String())
}

func TestPages_StaticCarriesTheSandboxPolicy(t *testing.T) {
	ph := newPageHarness()
	var parts []application.Part
	ph.addAsset("as_1", "site.css", &parts, "body{}")

	rec := ph.get(hostShop, "/_static/site.css")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())
	assert.Equal(t, staticPolicy, rec.Header().Get(cspHeader))
	assert.Contains(t, rec.Header().Get(cspHeader), "sandbox")
	assert.Equal(t, ctCSS, rec.Header().Get(hdrType))
	assert.Equal(t, staticCache, rec.Header().Get(hdrCache))
	etag := rec.Header().Get(headerETag)
	assert.Equal(t, `"as_1-1"`, etag)

	notModified := ph.do(http.MethodGet, hostShop, "/_static/site.css", http.Header{"If-None-Match": {etag}})
	assert.Equal(t, http.StatusNotModified, notModified.Code)
}

func TestPages_StaticAnswersOnlyGetAndHead(t *testing.T) {
	ph := newPageHarness()
	var parts []application.Part
	ph.addAsset("as_1", "site.css", &parts, "body{}")
	rec := ph.do(http.MethodPost, hostShop, "/_static/site.css", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
}

func TestPages_AStaticFileThatCannotBeReadIs503(t *testing.T) {
	ph := newPageHarness()
	var parts []application.Part
	ph.addAsset("as_1", "site.css", &parts, "body{}")
	ph.objects.getErr = errors.New("bucket is gone")
	assert.Equal(t, http.StatusServiceUnavailable, ph.get(hostShop, "/_static/site.css").Code)
}

func TestPages_AServedAssetIsTheVersionItsReleasePins(t *testing.T) {
	ph := newPageHarness()
	var parts []application.Part
	a := ph.addAsset("as_1", "site.css", &parts, "newest")
	a.CurrentVersion, a.ReleasedVersion = 5, 2
	ph.versions.versions["as_1/2"] = &portaldomain.AssetVersion{Version: 2, S3Key: "assets/as_1/v2", S3Bucket: assetBkt, ContentType: "text/plain"}
	ph.objects.bodies[objectName(assetBkt, "assets/as_1/v2")] = []byte("pinned")

	rec := ph.get(hostShop, "/_static/site.css")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "pinned", rec.Body.String())
	assert.Equal(t, `"as_1-2"`, rec.Header().Get(headerETag))
	assert.Equal(t, "text/plain", rec.Header().Get(hdrType))
	assert.Equal(t, []string{objectName(assetBkt, "assets/as_1/v2")}, ph.objects.gets)
}

// signIn runs the first leg: a browser with no viewer opens a page and is sent
// to the portal holding a state cookie.
func (ph *pageHarness) signIn(t *testing.T, host, target string) (*http.Cookie, *url.URL) {
	t.Helper()
	ph.server.fn = func(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute) { route.SignIn(w, r) }
	rec := ph.get(host, target)
	ph.server.fn = nil
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	state := cookieNamed(t, rec, ph.pages.cookieName(cookieState))
	loc, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	return state, loc
}

// handoff runs the portal's leg.
func (ph *pageHarness) handoff(query url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, portalURL+HandoffPath+"?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	ph.pages.Handoff(rec, req)
	return rec
}

func handoffQuery(app, host, path, state string) url.Values {
	return url.Values{"application": {app}, "host": {host}, queryPath: {path}, "state": {state}}
}

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	require.Failf(t, "cookie not set", "no cookie named %s among %v", name, rec.Result().Cookies())
	return nil
}

func withCookies(req *http.Request, cookies ...*http.Cookie) {
	for _, c := range cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value}) // #nosec G124 -- a request's cookie: name and value are all it sends
	}
}

func (ph *pageHarness) send(host, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+host+target, nil)
	withCookies(req, cookies...)
	rec := httptest.NewRecorder()
	ph.handler.ServeHTTP(rec, req)
	return rec
}

// code runs both legs and returns the code and the state cookie it opens with.
func (ph *pageHarness) code(t *testing.T) (string, *http.Cookie) {
	t.Helper()
	state, loc := ph.signIn(t, hostShop, "/")
	rec := ph.handoff(loc.Query())
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	back, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	return back.Query().Get(queryCode), state
}

func authPath(code, target string) string {
	return pathAuth + "?" + url.Values{queryCode: {code}, queryPath: {target}}.Encode()
}

func TestHandoff_EndToEnd(t *testing.T) {
	ph := newPageHarness()
	state, loc := ph.signIn(t, hostShop, "/cart?item=7")

	assert.Equal(t, "portal.example.com", loc.Host)
	assert.Equal(t, HandoffPath, loc.Path)
	q := loc.Query()
	assert.Equal(t, appShop, q.Get("application"))
	assert.Equal(t, hostShop, q.Get("host"))
	assert.Equal(t, "/cart?item=7", q.Get(queryPath))
	assert.Equal(t, digest(state.Value), q.Get("state"))
	assert.NotContains(t, loc.String(), state.Value, "the state itself is never in a URL")
	assert.True(t, state.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, state.SameSite)
	assert.Equal(t, stateTTLSeconds, state.MaxAge)

	rec := ph.handoff(q)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get(hdrCache))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	back, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	assert.Equal(t, "http", back.Scheme)
	assert.Equal(t, hostShop, back.Host)
	assert.Equal(t, pathAuth, back.Path)

	done := ph.send(hostShop, back.RequestURI(), state)
	require.Equal(t, http.StatusFound, done.Code, done.Body.String())
	assert.Equal(t, "/cart?item=7", done.Header().Get(headerLoc))
	assert.Equal(t, "no-store", done.Header().Get(hdrCache))

	viewerCookie := cookieNamed(t, done, cookieViewer)
	assert.True(t, viewerCookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, viewerCookie.SameSite)
	assert.Equal(t, "/", viewerCookie.Path)
	assert.Equal(t, int(epform.ViewerTTL.Seconds()), viewerCookie.MaxAge)
	assert.False(t, viewerCookie.Secure)

	cleared := cookieNamed(t, done, cookieState)
	assert.Empty(t, cleared.Value)
	assert.Negative(t, cleared.MaxAge)

	who := ph.send(hostShop, pathViewer, viewerCookie)
	require.Equal(t, http.StatusOK, who.Code)
	assert.Equal(t, "no-store", who.Header().Get(hdrCache))
	var body map[string]string
	require.NoError(t, json.Unmarshal(who.Body.Bytes(), &body))
	assert.Equal(t, map[string]string{"email": viewerMail, "user_id": viewerUser, "persona": "analyst"}, body)
}

func TestHandoff_ACodeIsRecordedByHashWithAnExpiry(t *testing.T) {
	ph := newPageHarness()
	code, state := ph.code(t)
	require.Equal(t, http.StatusFound, ph.send(hostShop, authPath(code, "/"), state).Code)

	require.Len(t, ph.used, 1)
	expires, recorded := ph.used[digest(code)]
	require.True(t, recorded, "the record is the code's hash, never the code")
	assert.Equal(t, ph.clock.Add(2*handoffTTL), expires)
}

func TestHandoff_ACodeReplayedIsRefused(t *testing.T) {
	ph := newPageHarness()
	code, state := ph.code(t)
	require.Equal(t, http.StatusFound, ph.send(hostShop, authPath(code, "/"), state).Code)

	rec := ph.send(hostShop, authPath(code, "/"), state)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "already used")
	assert.Empty(t, rec.Result().Cookies(), "no viewer cookie is set")
}

func TestHandoff_ACodeForAnotherApplicationIsRefused(t *testing.T) {
	ph := newPageHarness()
	code, state := ph.code(t)
	rec := ph.send(hostOther, authPath(code, "/"), state)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, rec.Result().Cookies())
	assert.Empty(t, ph.used, "a code that did not open is not spent")
}

func TestHandoff_ACodeWithAnotherStateCookieIsRefused(t *testing.T) {
	ph := newPageHarness()
	code, _ := ph.code(t)
	rec := ph.send(hostShop, authPath(code, "/"), &http.Cookie{Name: cookieState, Value: "another-browsers-state"}) // #nosec G124 -- a request's cookie: name and value are all it sends
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, rec.Result().Cookies())
	assert.Empty(t, ph.used)
}

func TestHandoff_NoStateCookieOrNoCodeIsRefused(t *testing.T) {
	ph := newPageHarness()
	code, state := ph.code(t)
	assert.Equal(t, http.StatusBadRequest, ph.send(hostShop, authPath(code, "/")).Code)
	assert.Equal(t, http.StatusBadRequest, ph.send(hostShop, authPath("", "/"), state).Code)
}

func TestHandoff_AnExpiredCodeIsRefused(t *testing.T) {
	ph := newPageHarness()
	code, state := ph.code(t)
	ph.clock = ph.clock.Add(handoffTTL + 2*time.Second)
	rec := ph.send(hostShop, authPath(code, "/"), state)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, ph.used)
}

func TestHandoff_ARecordThatCannotBeWrittenIsRefused(t *testing.T) {
	ph := newPageHarness()
	code, state := ph.code(t)
	ph.useErr = errors.New("database is down")
	rec := ph.send(hostShop, authPath(code, "/"), state)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, rec.Result().Cookies())
}

var externalPaths = []struct {
	name, target string
}{
	{name: "a protocol-relative URL", target: "//evil.example"},
	{name: "an absolute URL", target: "http://evil.example"},
	{name: "a backslash after the slash", target: `/\evil.example`},
	{name: "a backslash inside", target: `/x\y`},
	{name: "a newline", target: "/x\ny"},
	{name: "a relative path", target: "evil.example"},
	{name: "nothing", target: ""},
}

func TestHandoff_AnExternalPathIsRefusedAtThePortal(t *testing.T) {
	for _, tt := range externalPaths {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			rec := ph.handoff(handoffQuery(appShop, hostShop, tt.target, stateValue))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, rec.Header().Get(headerLoc))
		})
	}
}

func TestHandoff_AnExternalPathIsRefusedOnThePageOrigin(t *testing.T) {
	for _, tt := range externalPaths {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			code, state := ph.code(t)
			rec := ph.send(hostShop, authPath(code, tt.target), state)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Empty(t, rec.Header().Get(headerLoc))
			assert.Empty(t, rec.Result().Cookies())
			assert.Empty(t, ph.used, "the code is not spent on a refused path")
		})
	}
}

func TestHandoff_TheHostMustServeTheApplication(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		app    string
		status int
		opt    pageOption
	}{
		{name: "the application's own label", host: hostShop, app: appShop, status: http.StatusFound},
		{name: "a port on it", host: hostShop + ":8443", app: appShop, status: http.StatusFound},
		{name: "upper case", host: "SHOP.pages.example.com", app: appShop, status: http.StatusFound},
		{name: "a verified mapping to the application", host: hostMapped, app: appShop, status: http.StatusFound},
		{name: "a stranger's host", host: "evil.example", app: appShop, status: http.StatusBadRequest},
		{name: "another application's label", host: hostOther, app: appShop, status: http.StatusBadRequest},
		{name: "a verified mapping to another application", host: hostMapped, app: appOther, status: http.StatusBadRequest},
		{name: "a suffix of the label's name", host: "evilshop.pages.example.com", app: appShop, status: http.StatusBadRequest},
		{name: "no mapping lookup available", host: hostMapped, app: appShop, status: http.StatusBadRequest, opt: func(p *Pages) { p.RouteHost = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []pageOption
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			ph := newPageHarness(opts...)
			rec := ph.handoff(handoffQuery(tt.app, tt.host, "/", stateValue))
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			if tt.status == http.StatusBadRequest {
				assert.Empty(t, rec.Header().Get(headerLoc))
			}
		})
	}
}

func TestHandoff_AStateOfAnotherShapeIsRefused(t *testing.T) {
	for _, state := range []string{"", "short", strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		t.Run(state, func(t *testing.T) {
			ph := newPageHarness()
			assert.Equal(t, http.StatusBadRequest, ph.handoff(handoffQuery(appShop, hostShop, "/", state)).Code)
		})
	}
}

func TestHandoff_APersonNotSignedInIsSentToTheLogin(t *testing.T) {
	tests := []struct {
		name    string
		authErr error
	}{
		{name: "nobody"},
		{name: "a session that could not be read", authErr: errors.New("expired")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			ph.caller, ph.authErr = nil, tt.authErr
			query := handoffQuery(appShop, hostShop, "/cart", stateValue)
			rec := ph.handoff(query)
			assert.Equal(t, http.StatusFound, rec.Code)
			assert.Equal(t, epformpage.LoginURL(HandoffPath+"?"+query.Encode()), rec.Header().Get(headerLoc))
		})
	}
}

func TestHandoff_ErrForbiddenAnswers403(t *testing.T) {
	ph := newPageHarness()
	ph.caller, ph.authErr = nil, ephttp.ErrForbidden
	rec := ph.handoff(handoffQuery(appShop, hostShop, "/", stateValue))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get(headerLoc))
}

func TestHandoff_NoSigningKeyCannotIssueACode(t *testing.T) {
	ph := newPageHarness(func(p *Pages) { p.FormKey = nil })
	assert.Equal(t, http.StatusServiceUnavailable, ph.handoff(handoffQuery(appShop, hostShop, "/", stateValue)).Code)
}

func TestSignIn_ADeploymentThatCannotSignAViewerIn(t *testing.T) {
	tests := []struct {
		name string
		opt  pageOption
	}{
		{name: "no signing key", opt: func(p *Pages) { p.FormKey = nil }},
		{name: "no portal address", opt: func(p *Pages) { p.PortalURL = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness(tt.opt)
			ph.server.fn = func(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute) { route.SignIn(w, r) }
			rec := ph.get(hostShop, "/")
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Empty(t, rec.Result().Cookies())
		})
	}
}

func TestSignIn_APortalAddressWithATrailingSlash(t *testing.T) {
	ph := newPageHarness(func(p *Pages) { p.PortalURL = portalURL + "/" })
	_, loc := ph.signIn(t, hostShop, "/")
	assert.Equal(t, HandoffPath, loc.Path)
}

func TestHandoff_WithSecureTheCookiesCarryTheHostPrefix(t *testing.T) {
	ph := newPageHarness(secure)
	state, loc := ph.signIn(t, hostShop, "/")
	assert.Equal(t, securePrefix+cookieState, state.Name)
	assert.True(t, state.Secure)
	assert.Equal(t, "/", state.Path)
	assert.Empty(t, state.Domain)

	rec := ph.handoff(loc.Query())
	require.Equal(t, http.StatusFound, rec.Code)
	back, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	assert.Equal(t, schemeHTTPS, back.Scheme)

	done := ph.send(hostShop, back.RequestURI(), state)
	require.Equal(t, http.StatusFound, done.Code, done.Body.String())
	viewerCookie := cookieNamed(t, done, securePrefix+cookieViewer)
	assert.True(t, viewerCookie.Secure)
	assert.True(t, viewerCookie.HttpOnly)
	assert.Equal(t, "/", viewerCookie.Path)
	assert.Empty(t, viewerCookie.Domain)
	cleared := cookieNamed(t, done, securePrefix+cookieState)
	assert.True(t, cleared.Secure)
	assert.Negative(t, cleared.MaxAge)

	who := ph.send(hostShop, pathViewer, viewerCookie)
	assert.Equal(t, http.StatusOK, who.Code)
}

func TestHandoff_AVerifiedMappingCompletesOnItsOwnHost(t *testing.T) {
	ph := newPageHarness()
	ph.store.hosts[hostMapped] = &site.Route{Application: appShop, Hostname: hostMapped}
	ph.store.added = append(ph.store.added, hostMapped)
	ph.gateway.Invalidate(hostMapped)

	state, loc := ph.signIn(t, hostMapped, "/")
	assert.Equal(t, hostMapped, loc.Query().Get("host"))
	rec := ph.handoff(loc.Query())
	require.Equal(t, http.StatusFound, rec.Code)
	back, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	assert.Equal(t, hostMapped, back.Host)

	done := ph.send(hostMapped, back.RequestURI(), state)
	assert.Equal(t, http.StatusFound, done.Code, done.Body.String())
}

func viewerToken(t *testing.T, ph *pageHarness, app string) *http.Cookie {
	t.Helper()
	token, err := epform.SignViewer(ph.pages.viewerKey(), viewerScope+app, epform.Viewer{UserID: viewerUser, Email: viewerMail, Persona: "analyst"}, ph.clock)
	require.NoError(t, err)
	return &http.Cookie{Name: ph.pages.cookieName(cookieViewer), Value: token} // #nosec G124 -- a request's cookie: name and value are all it sends
}

func TestCaller(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		header  http.Header
		wantErr error
		nobody  bool
	}{
		{name: "a POST with neither Origin nor Sec-Fetch-Site is refused", method: http.MethodPost, wantErr: ephttp.ErrForbidden},
		{name: "a POST from another page origin is refused", method: http.MethodPost, header: http.Header{"Origin": {"http://other.pages.example.com"}}, wantErr: ephttp.ErrForbidden},
		{name: "a POST from an opaque origin is refused", method: http.MethodPost, header: http.Header{"Origin": {"null"}}, wantErr: ephttp.ErrForbidden},
		{name: "a POST the browser calls cross-site is refused", method: http.MethodPost, header: http.Header{"Sec-Fetch-Site": {"same-site"}}, wantErr: ephttp.ErrForbidden},
		{name: "a PUT with neither is refused", method: http.MethodPut, wantErr: ephttp.ErrForbidden},
		{name: "a POST with a same-origin Origin passes", method: http.MethodPost, header: http.Header{"Origin": {"http://" + hostShop}}},
		{name: "a POST the browser calls same-origin passes", method: http.MethodPost, header: http.Header{"Sec-Fetch-Site": {"same-origin"}}},
		{name: "a GET needs neither", method: http.MethodGet},
		{name: "a HEAD needs neither", method: http.MethodHead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			req := httptest.NewRequestWithContext(context.Background(), tt.method, "http://"+hostShop+"/", nil)
			maps.Copy(req.Header, tt.header)
			withCookies(req, viewerToken(t, ph, appShop))

			caller, err := ph.pages.caller(req, appShop)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, caller)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, caller)
			assert.Equal(t, viewerMail, caller.Email)
			assert.Equal(t, viewerUser, caller.UserID)
			assert.Equal(t, "analyst", caller.Persona)
		})
	}
}

func TestCaller_WhoIsNobody(t *testing.T) {
	tests := []struct {
		name   string
		method string
		cookie func(t *testing.T, ph *pageHarness) *http.Cookie
		setup  func(ph *pageHarness)
	}{
		{name: "no cookie, on a GET", method: http.MethodGet},
		{name: "no cookie, on a POST is not a refusal", method: http.MethodPost},
		{
			name: "a token signed for another application opens as nobody", method: http.MethodGet,
			cookie: func(t *testing.T, ph *pageHarness) *http.Cookie { t.Helper(); return viewerToken(t, ph, appOther) },
		},
		{
			name: "a token signed with the handoff key opens as nobody", method: http.MethodGet,
			cookie: func(t *testing.T, ph *pageHarness) *http.Cookie {
				t.Helper()
				token, err := epform.SignViewer(ph.pages.handoffKey(), viewerScope+appShop, epform.Viewer{UserID: viewerUser, Email: viewerMail}, ph.clock)
				require.NoError(t, err)
				return &http.Cookie{Name: ph.pages.cookieName(cookieViewer), Value: token} // #nosec G124 -- a request's cookie: name and value are all it sends
			},
		},
		{
			name: "a garbled token opens as nobody", method: http.MethodGet,
			cookie: func(t *testing.T, ph *pageHarness) *http.Cookie {
				t.Helper()
				return &http.Cookie{Name: ph.pages.cookieName(cookieViewer), Value: "not.a-token"} // #nosec G124 -- a request's cookie: name and value are all it sends
			},
		},
		{
			name: "an expired token opens as nobody", method: http.MethodGet,
			cookie: func(t *testing.T, ph *pageHarness) *http.Cookie { t.Helper(); return viewerToken(t, ph, appShop) },
			setup:  func(ph *pageHarness) { ph.clock = ph.clock.Add(epform.ViewerTTL + time.Minute) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPageHarness()
			req := httptest.NewRequestWithContext(context.Background(), tt.method, "http://"+hostShop+"/", nil)
			if tt.cookie != nil {
				withCookies(req, tt.cookie(t, ph))
			}
			if tt.setup != nil {
				tt.setup(ph)
			}
			caller, err := ph.pages.caller(req, appShop)
			require.NoError(t, err)
			assert.Nil(t, caller)
		})
	}
}

func TestPages_AnEndpointsCallerIsTheOriginsViewer(t *testing.T) {
	ph := newPageHarness()
	var got *eprun.Caller
	ph.server.fn = func(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute) {
		caller, err := route.Caller(r)
		if err == nil {
			got = caller
		}
		w.WriteHeader(http.StatusNoContent)
	}
	ph.send(hostShop, "/", viewerToken(t, ph, appShop))
	require.NotNil(t, got)
	assert.Equal(t, viewerMail, got.Email)
}

func TestViewer_NoKeyMeansNobody(t *testing.T) {
	ph := newPageHarness(func(p *Pages) { p.FormKey = nil })
	rec := ph.send(hostShop, pathViewer, &http.Cookie{Name: cookieViewer, Value: "anything"}) // #nosec G124 -- a request's cookie: name and value are all it sends
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get(hdrCache))
}

func TestLocalPath(t *testing.T) {
	assert.True(t, isLocalURL("/a/b?c=d"))
	assert.True(t, isLocalURL("/"))
	for _, p := range []string{"", "a", "//a", `/a\b`, "/a\rb", "/a\nb", "/a\tb", "https://a", `/\evil.example`, `/\\evil.example`} {
		assert.False(t, isLocalURL(p), "%q", p)
	}
}

// The host a person is sent back to comes from the stored records and the
// configured port. The request only selects the record: its port is not
// repeated, and a host naming no record, or holding a userinfo, selects none.
func TestOriginHost_IsReadFromStoredRecords(t *testing.T) {
	ph := newPageHarness()
	ph.pages.PublicPort = "8443"
	ph.store.hosts[hostMapped] = &site.Route{Application: appShop, Hostname: hostMapped}
	pageHost := appShop + "." + ph.pages.PagesDomain()
	ctx := context.Background()

	assert.Equal(t, pageHost+":8443", ph.pages.originHost(ctx, pageHost+":99", appShop))
	assert.Equal(t, hostMapped+":8443", ph.pages.originHost(ctx, hostMapped, appShop))
	assert.Empty(t, ph.pages.originHost(ctx, "evil.example", appShop))
	assert.Empty(t, ph.pages.originHost(ctx, pageHost+"@evil.example", appShop))
	assert.Empty(t, ph.pages.originHost(ctx, "other."+ph.pages.PagesDomain(), appShop))
}
