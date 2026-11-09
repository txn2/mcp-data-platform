package sitegw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

const (
	ownerMail    = "jane@example.com"
	viewerAddr   = "vi@client.example"
	managerAddr  = "mo@example.com"
	strangerAddr = "joe@example.com"
	otherSiteID  = "site_2"
	otherHost    = "blog.pages.example.com"
	secretBody   = "RELEASE-CONTENT-7f3a"
	personaUser  = "analyst"
	personaAdmin = "admin"
	personaExt   = "client"
	goodState    = "state-value-held-by-this-browser"
)

// memAccess is what decides who may open a site, by site id and email.
type memAccess struct {
	store   *memStore
	access  map[string]*site.Access // by site id
	grants  map[string]string       // by site id + "/" + email
	reads   int
	err     error
	used    map[string]time.Time
	useErr  error
	asked   []string
	expires time.Time
}

func (m *memAccess) SiteAccess(_ context.Context, siteID, email string) (*site.Access, error) {
	m.reads++
	m.asked = append(m.asked, siteID+"/"+email)
	if m.err != nil {
		return nil, m.err
	}
	base, ok := m.access[siteID]
	if !ok {
		return nil, site.ErrNotFound
	}
	out := *base
	out.Level = m.grants[siteID+"/"+email]
	return &out, nil
}

func (m *memAccess) RouteOwn(ctx context.Context, siteName string) (*site.Route, error) {
	return m.store.RouteOwn(ctx, siteName)
}

func (m *memAccess) UseHandoff(_ context.Context, codeHash string, expires time.Time) (bool, error) {
	if m.useErr != nil {
		return false, m.useErr
	}
	if _, seen := m.used[codeHash]; seen {
		return false, nil
	}
	m.used[codeHash] = expires
	m.expires = expires
	return true, nil
}

// accessHarness is a private site's own address behind the real guard, inside
// the real gateway, with a second private site beside it.
type accessHarness struct {
	*harness
	db         *memAccess
	access     *Access
	clock      time.Time
	viewer     *SiteViewer
	sessionErr error
	pages      string
	admins     map[string]bool
	externals  map[string]bool
}

type accessOption func(*Access)

func newAccessHarness(opts ...accessOption) *accessHarness {
	ah := &accessHarness{
		clock:     time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
		pages:     testPagesDomain,
		admins:    map[string]bool{personaAdmin: true},
		externals: map[string]bool{personaExt: true},
		viewer:    &SiteViewer{UserID: "u1", Email: ownerMail, Persona: personaUser},
	}
	ah.db = &memAccess{
		access: map[string]*site.Access{
			testSiteID:  {OwnerEmail: ownerMail},
			otherSiteID: {OwnerEmail: "someone@example.com"},
		},
		grants: map[string]string{
			testSiteID + "/" + viewerAddr:  site.LevelView,
			testSiteID + "/" + managerAddr: site.LevelManage,
		},
		used: map[string]time.Time{},
	}
	ah.access = &Access{
		Store:       ah.db,
		FormKey:     []byte("0123456789abcdef0123456789abcdef"),
		PortalURL:   portalURL,
		PagesDomain: func() string { return ah.pages },
		IsAdmin:     func(p string) bool { return ah.admins[p] },
		IsExternal:  func(p string) bool { return ah.externals[p] },
		Session:     func(*http.Request) (*SiteViewer, error) { return ah.viewer, ah.sessionErr },
		Now:         func() time.Time { return ah.clock },
	}
	for _, opt := range opts {
		opt(ah.access)
	}
	ah.harness = newHarness(Deps{Access: ah.access})
	ah.db.store = ah.store

	rel := ah.addRelease(1, []site.Redirect{{From: "/old", To: "/new-" + secretBody, Status: http.StatusMovedPermanently}},
		testFile{path: indexFile, body: "<p>" + secretBody + "</p>", contentType: ctHTML},
		testFile{path: "style.css", body: "body{/*" + secretBody + "*/}", contentType: ctCSS},
		testFile{path: "site.3f2a9c1b.js", body: "// " + secretBody, contentType: "text/javascript"},
		testFile{path: "_auth", body: secretBody + " in a file named _auth", contentType: ctHTML},
		testFile{path: "_signin", body: secretBody + " in a file named _signin", contentType: ctHTML},
		testFile{path: "_signout", body: secretBody + " in a file named _signout", contentType: ctHTML},
		testFile{path: notFound, body: secretBody + " missing", contentType: ctHTML},
	)
	ah.store.own["marketing"] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: site.EnvDefault, Release: rel, Private: true}
	ah.store.own["blog"] = &site.Route{SiteID: otherSiteID, Site: "blog", Environment: site.EnvDefault, Release: rel, Private: true}
	return ah
}

func secureAccess(a *Access) { a.Secure = true }

func (ah *accessHarness) signed(siteID string, v epform.Viewer, ttl time.Duration) string {
	token, err := epform.SignViewerFor(ah.access.viewerKey(), siteViewerScope+siteID, v, ah.clock, ttl)
	if err != nil {
		panic(err)
	}
	return token
}

func (ah *accessHarness) cookieFor(v epform.Viewer) *http.Cookie {
	return ah.rawCookie(ah.signed(testSiteID, v, siteViewerTTL))
}

func (ah *accessHarness) rawCookie(value string) *http.Cookie {
	return &http.Cookie{Name: ah.access.cookieName(cookieSiteViewer), Value: value} // #nosec G124 -- a request's cookie: name and value are all it sends
}

func viewerOf(email string) epform.Viewer {
	return epform.Viewer{UserID: "u-" + email, Email: email, Persona: personaUser}
}

func (ah *accessHarness) send(method, host, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, "http://"+host+target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	ah.handler.ServeHTTP(rec, req)
	return rec
}

func (ah *accessHarness) open(target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return ah.send(http.MethodGet, testOwnHost, target, cookies...)
}

// assertNoRelease is the promise every refusal keeps: nothing of the release,
// in the body or in any header, redirect targets included.
func assertNoRelease(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.NotContains(t, rec.Body.String(), secretBody, "a refusal's body holds none of the release")
	for name, values := range rec.Header() {
		for _, v := range values {
			assert.NotContains(t, v, secretBody, "header %s", name)
		}
	}
}

func assertRefusalHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, noIndex, rec.Header().Get(hdrRobots))
	assert.Equal(t, nosniff, rec.Header().Get(hdrNosniff))
	assert.Equal(t, noStore, rec.Header().Get(hdrCache))
	assert.Equal(t, noReferrer, rec.Header().Get(hdrReferer))
	assert.Equal(t, pagePolicy, rec.Header().Get(hdrCSP))
	assert.Equal(t, htmlType, rec.Header().Get(hdrType))
}

func TestAccess_NoSignInIs401OnEveryPathAndMethod(t *testing.T) {
	paths := []string{"/", "/style.css", "/site.3f2a9c1b.js", "/missing", "/old", "/index.html", "/a/b/c?x=1"}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete} {
		for _, target := range paths {
			t.Run(method+" "+target, func(t *testing.T) {
				ah := newAccessHarness()
				rec := ah.send(method, testOwnHost, target)
				assert.Equal(t, http.StatusUnauthorized, rec.Code)
				assertRefusalHeaders(t, rec)
				assertNoRelease(t, rec)
				assert.Empty(t, rec.Header().Get(headerLoc), "a refusal never redirects into the release")
				assert.Empty(t, rec.Header().Get(headerETag))
				if method == http.MethodHead {
					assert.Empty(t, rec.Body.String(), "a HEAD has no body")
				}
				assert.Zero(t, ah.objects.gets, "the release was never read")
			})
		}
	}
}

func TestAccess_TheSignInPageCarriesTheAddressEscaped(t *testing.T) {
	ah := newAccessHarness()
	rec := ah.open(`/a?x="><script>alert(1)</script>`)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Sign in to view this preview")
	assert.Contains(t, body, testOwnHost)
	assert.Contains(t, body, `name="path"`)
	assert.NotContains(t, body, "<script>alert", "the requested address is escaped")
}

func TestAccess_ACookieThatIsNotGoodIs401(t *testing.T) {
	bytesOf := func(s string) []byte { return []byte(s) }
	flip := func(s string) string {
		b := bytesOf(s)
		last := len(b) - 1
		if b[last] == 'A' {
			b[last] = 'B'
		} else {
			b[last] = 'A'
		}
		return string(b)
	}
	cases := []struct {
		name   string
		cookie func(ah *accessHarness) *http.Cookie
	}{
		{"random garbage", func(ah *accessHarness) *http.Cookie { return ah.rawCookie("garbage") }},
		{"no dot", func(ah *accessHarness) *http.Cookie { return ah.rawCookie("notatoken") }},
		{"a tampered signature", func(ah *accessHarness) *http.Cookie {
			return ah.rawCookie(flip(ah.signed(testSiteID, viewerOf(ownerMail), siteViewerTTL)))
		}},
		{"a tampered payload", func(ah *accessHarness) *http.Cookie {
			token := ah.signed(testSiteID, viewerOf(strangerAddr), siteViewerTTL)
			other := ah.signed(testSiteID, viewerOf(ownerMail), siteViewerTTL)
			body, _, _ := strings.Cut(other, ".")
			_, sig, _ := strings.Cut(token, ".")
			return ah.rawCookie(body + "." + sig)
		}},
		{"signed for another site", func(ah *accessHarness) *http.Cookie {
			return ah.rawCookie(ah.signed(otherSiteID, viewerOf(ownerMail), siteViewerTTL))
		}},
		{"expired", func(ah *accessHarness) *http.Cookie {
			token := ah.signed(testSiteID, viewerOf(ownerMail), time.Minute)
			ah.clock = ah.clock.Add(2 * time.Minute)
			return ah.rawCookie(token)
		}},
		{"signed with another key", func(ah *accessHarness) *http.Cookie {
			key := shareguest.DeriveKey([]byte("a-different-signing-key-entirely"), siteViewerKeyLabel)
			token, err := epform.SignViewerFor(key, siteViewerScope+testSiteID, viewerOf(ownerMail), ah.clock, siteViewerTTL)
			require.NoError(t, err)
			return ah.rawCookie(token)
		}},
		{"a handoff code presented as the cookie", func(ah *accessHarness) *http.Cookie {
			token, err := epform.SignViewerFor(ah.access.handoffKey(), siteViewerScope+testSiteID, viewerOf(ownerMail), ah.clock, siteViewerTTL)
			require.NoError(t, err)
			return ah.rawCookie(token)
		}},
		{"a token naming nobody", func(ah *accessHarness) *http.Cookie {
			return ah.rawCookie(ah.signed(testSiteID, epform.Viewer{}, siteViewerTTL))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAccessHarness()
			rec := ah.open("/", tc.cookie(ah))
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assertNoRelease(t, rec)
			assert.Zero(t, ah.db.reads, "a bad cookie reads no access")
		})
	}
}

func TestAccess_ATokenForOneSiteIsRefusedOnAnother(t *testing.T) {
	ah := newAccessHarness()
	ownerOfMarketing := ah.cookieFor(viewerOf(ownerMail))
	require.Equal(t, http.StatusOK, ah.open("/", ownerOfMarketing).Code)

	rec := ah.send(http.MethodGet, otherHost, "/", ownerOfMarketing)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the same cookie does not reach another site's address")
	assertNoRelease(t, rec)
}

func TestAccess_NoKeySignsNobodyIn(t *testing.T) {
	ah := newAccessHarness(func(a *Access) { a.FormKey = nil })
	assert.Nil(t, ah.access.viewerKey())
	assert.Nil(t, ah.access.handoffKey())
	rec := ah.open("/", ah.rawCookie("anything.at-all"))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertNoRelease(t, rec)
}

func TestAccess_WhoMayOpen(t *testing.T) {
	cases := []struct {
		name       string
		viewer     epform.Viewer
		internal   bool
		wantStatus int
		wantText   string
	}{
		{name: "the owner", viewer: viewerOf(ownerMail), wantStatus: http.StatusOK},
		{name: "the owner in other capitals", viewer: viewerOf("JANE@Example.com"), wantStatus: http.StatusOK},
		{name: "someone with view", viewer: viewerOf(viewerAddr), wantStatus: http.StatusOK},
		{name: "someone who manages it", viewer: viewerOf(managerAddr), wantStatus: http.StatusOK},
		{name: "an administrator, by the live persona", viewer: epform.Viewer{UserID: "a", Email: strangerAddr, Persona: personaAdmin}, wantStatus: http.StatusOK},
		{name: "someone internal on a site open to everyone internal", viewer: viewerOf(strangerAddr), internal: true, wantStatus: http.StatusOK},

		{name: "someone else", viewer: viewerOf(strangerAddr), wantStatus: http.StatusForbidden, wantText: strangerAddr},
		{name: "someone external with no grant on an internal site", viewer: epform.Viewer{UserID: "e", Email: strangerAddr, Persona: personaExt}, internal: true, wantStatus: http.StatusForbidden, wantText: "Ask the person who sent you this link"},
		{name: "an empty email claiming nothing", viewer: epform.Viewer{UserID: "x", Persona: personaUser}, wantStatus: http.StatusForbidden},
		{name: "the owner's address, not verified", viewer: epform.Viewer{UserID: "o", Email: ownerMail, Persona: personaUser, EmailUnverified: true}, wantStatus: http.StatusForbidden, wantText: "has not verified"},
		{name: "a grant's address, not verified", viewer: epform.Viewer{UserID: "v", Email: viewerAddr, Persona: personaUser, EmailUnverified: true}, wantStatus: http.StatusForbidden, wantText: "has not verified"},
		{name: "someone else, not verified, reads as no access", viewer: epform.Viewer{UserID: "s", Email: strangerAddr, Persona: personaUser, EmailUnverified: true}, wantStatus: http.StatusForbidden, wantText: "Ask the person who sent you this link"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAccessHarness()
			ah.db.access[testSiteID].InternalView = tc.internal
			rec := ah.open("/", ah.cookieFor(tc.viewer))
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantStatus == http.StatusOK {
				assert.Contains(t, rec.Body.String(), secretBody)
				assert.Equal(t, cachePrivate, rec.Header().Get(hdrCache))
				assert.Equal(t, noIndex, rec.Header().Get(hdrRobots))
				assert.Contains(t, rec.Header().Values(hdrVary), "Cookie")
				return
			}
			assertRefusalHeaders(t, rec)
			assertNoRelease(t, rec)
			assert.Contains(t, rec.Body.String(), tc.wantText)
			assert.Contains(t, rec.Body.String(), "/_signout", "someone refused can sign in as someone else")
		})
	}
}

func TestAccess_ARefusalHoldsNoReleaseWhateverIsAsked(t *testing.T) {
	for _, target := range []string{"/style.css", "/missing", "/old", "/site.3f2a9c1b.js"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+target, func(t *testing.T) {
				ah := newAccessHarness()
				rec := ah.send(method, testOwnHost, target, ah.cookieFor(viewerOf(strangerAddr)))
				assert.Equal(t, http.StatusForbidden, rec.Code)
				assertNoRelease(t, rec)
				assert.Empty(t, rec.Header().Get(headerLoc))
				assert.Zero(t, ah.objects.gets, "the release was never read")
			})
		}
	}
}

func TestAccess_AdministratorAndExternalComeFromTheLivePersona(t *testing.T) {
	t.Run("the cookie's own admin flag means nothing", func(t *testing.T) {
		ah := newAccessHarness()
		v := epform.Viewer{UserID: "u", Email: strangerAddr, Persona: personaUser, Admin: true}
		assert.Equal(t, http.StatusForbidden, ah.open("/", ah.cookieFor(v)).Code)
	})
	t.Run("a persona no longer an administrator is refused on its next request", func(t *testing.T) {
		ah := newAccessHarness()
		cookie := ah.cookieFor(epform.Viewer{UserID: "a", Email: strangerAddr, Persona: personaAdmin})
		require.Equal(t, http.StatusOK, ah.open("/", cookie).Code)
		ah.admins[personaAdmin] = false
		assert.Equal(t, http.StatusForbidden, ah.open("/", cookie).Code)
	})
	t.Run("a persona made external loses an internal site on its next request", func(t *testing.T) {
		ah := newAccessHarness()
		ah.db.access[testSiteID].InternalView = true
		cookie := ah.cookieFor(viewerOf(strangerAddr))
		require.Equal(t, http.StatusOK, ah.open("/", cookie).Code)
		ah.externals[personaUser] = true
		assert.Equal(t, http.StatusForbidden, ah.open("/", cookie).Code)
	})
	t.Run("no administrator check admits no one as one", func(t *testing.T) {
		ah := newAccessHarness(func(a *Access) { a.IsAdmin = nil })
		assert.Equal(t, http.StatusForbidden, ah.open("/", ah.cookieFor(epform.Viewer{UserID: "a", Email: strangerAddr, Persona: personaAdmin})).Code)
	})
	t.Run("no external check treats everyone as external", func(t *testing.T) {
		ah := newAccessHarness(func(a *Access) { a.IsExternal = nil })
		ah.db.access[testSiteID].InternalView = true
		assert.Equal(t, http.StatusForbidden, ah.open("/", ah.cookieFor(viewerOf(strangerAddr))).Code)
	})
}

func TestAccess_IsReadOnEveryRequest(t *testing.T) {
	ah := newAccessHarness()
	cookie := ah.cookieFor(viewerOf(viewerAddr))
	require.Equal(t, http.StatusOK, ah.open("/", cookie).Code)
	require.Equal(t, http.StatusOK, ah.open("/", cookie).Code)
	assert.Equal(t, 2, ah.db.reads, "no answer is held between requests")
	assert.Equal(t, []string{testSiteID + "/" + viewerAddr, testSiteID + "/" + viewerAddr}, ah.db.asked)

	delete(ah.db.grants, testSiteID+"/"+viewerAddr)
	rec := ah.open("/", cookie)
	assert.Equal(t, http.StatusForbidden, rec.Code, "removing someone takes effect on their next request, with the same cookie")
	assertNoRelease(t, rec)
}

func TestAccess_AnAccessReadThatFailsIs503AndHoldsNothing(t *testing.T) {
	ah := newAccessHarness()
	cookie := ah.cookieFor(viewerOf(ownerMail))
	ah.db.err = errors.New("pq: password authentication failed at 10.1.2.3")
	rec := ah.open("/", cookie)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, noStore, rec.Header().Get(hdrCache))
	assert.NotContains(t, rec.Body.String(), "10.1.2.3")
	assertNoRelease(t, rec)
}

func TestAccess_ASiteThatWasDeletedReadsAsAFailureNotAnOpenDoor(t *testing.T) {
	ah := newAccessHarness()
	cookie := ah.cookieFor(viewerOf(ownerMail))
	delete(ah.db.access, testSiteID)
	rec := ah.open("/", cookie)
	assert.NotEqual(t, http.StatusOK, rec.Code)
	assertNoRelease(t, rec)
}

func TestAccess_FilesServedKeepNothingInACache(t *testing.T) {
	for _, target := range []string{"/", "/style.css", "/site.3f2a9c1b.js"} {
		t.Run(target, func(t *testing.T) {
			ah := newAccessHarness()
			rec := ah.open(target, ah.cookieFor(viewerOf(ownerMail)))
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, cachePrivate, rec.Header().Get(hdrCache), "a fingerprinted name is not cached for a year here")
			assert.Equal(t, noIndex, rec.Header().Get(hdrRobots))
		})
	}
	t.Run("HEAD", func(t *testing.T) {
		ah := newAccessHarness()
		rec := ah.send(http.MethodHead, testOwnHost, "/style.css", ah.cookieFor(viewerOf(ownerMail)))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, cachePrivate, rec.Header().Get(hdrCache))
		assert.Equal(t, noIndex, rec.Header().Get(hdrRobots))
	})
}

func TestAccess_AReleaseCannotHoldTheGuardsPaths(t *testing.T) {
	for _, target := range []string{pathSiteAuth, pathSiteSignIn, pathSiteSignOut} {
		t.Run(target, func(t *testing.T) {
			ah := newAccessHarness()
			rec := ah.open(target, ah.cookieFor(viewerOf(ownerMail)))
			assert.NotEqual(t, http.StatusOK, rec.Code)
			assertNoRelease(t, rec)
		})
	}
}

// startSignIn runs /_signin on the site and returns the state cookie and the
// portal address it redirected to.
func (ah *accessHarness) startSignIn(t *testing.T, target string) (*http.Cookie, *url.URL) {
	t.Helper()
	rec := ah.open("/_signin?path=" + url.QueryEscape(target))
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	var state *http.Cookie
	for _, c := range rec.Result().Cookies() { //nolint:bodyclose // a recorder's result has no body to close
		if c.Name == ah.access.cookieName(cookieSiteState) {
			state = c
		}
	}
	require.NotNil(t, state, "the state cookie is set on this hostname")
	loc, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	return &http.Cookie{Name: state.Name, Value: state.Value}, loc // #nosec G124 -- a request's cookie: name and value are all it sends
}

// runHandoff follows the portal route for loc and returns the /_auth address it
// sends the browser back to.
func (ah *accessHarness) runHandoff(t *testing.T, loc *url.URL) *url.URL {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, loc.String(), nil)
	ah.access.Handoff(rec, req)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	back, err := url.Parse(rec.Header().Get(headerLoc))
	require.NoError(t, err)
	return back
}

func TestAccess_SignInEndToEnd(t *testing.T) {
	ah := newAccessHarness()
	stateCookie, loc := ah.startSignIn(t, "/page?x=1")

	assert.Equal(t, "https", loc.Scheme)
	assert.Equal(t, "portal.example.com", loc.Host)
	assert.Equal(t, SiteHandoffPath, loc.Path)
	q := loc.Query()
	assert.Equal(t, "marketing", q.Get("site"))
	assert.Equal(t, "/page?x=1", q.Get(queryPath))
	assert.Equal(t, digest(stateCookie.Value), q.Get("state"), "the portal learns only the digest")
	assert.NotContains(t, loc.String(), stateCookie.Value)

	back := ah.runHandoff(t, loc)
	assert.Equal(t, "http", back.Scheme, "the scheme follows Secure")
	assert.Equal(t, testOwnHost, back.Host)
	assert.Equal(t, pathSiteAuth, back.Path)
	assert.Equal(t, "/page?x=1", back.Query().Get(queryPath))
	require.NotEmpty(t, back.Query().Get(queryCode))

	rec := ah.open(back.RequestURI(), stateCookie)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	assert.Equal(t, "/page?x=1", rec.Header().Get(headerLoc))
	assert.Equal(t, noStore, rec.Header().Get(hdrCache))
	assert.Equal(t, noReferrer, rec.Header().Get(hdrReferer))

	var viewer *http.Cookie
	cleared := false
	for _, c := range rec.Result().Cookies() { //nolint:bodyclose // a recorder's result has no body to close
		switch c.Name {
		case ah.access.cookieName(cookieSiteViewer):
			viewer = c
		case ah.access.cookieName(cookieSiteState):
			cleared = c.MaxAge < 0
		}
	}
	require.NotNil(t, viewer, "the code is traded for this hostname's cookie")
	assert.True(t, viewer.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, viewer.SameSite)
	assert.Equal(t, int(siteViewerTTL.Seconds()), viewer.MaxAge)
	assert.True(t, cleared, "the state cookie is spent")

	page := ah.open("/page", &http.Cookie{Name: viewer.Name, Value: viewer.Value}) // #nosec G124 -- a request's cookie: name and value are all it sends
	assert.NotEqual(t, http.StatusUnauthorized, page.Code, "the cookie opens the site")
	assert.Equal(t, http.StatusOK, ah.open("/", &http.Cookie{Name: viewer.Name, Value: viewer.Value}).Code) // #nosec G124
}

func TestSiteSignIn_RefusesToLeaveTheSite(t *testing.T) {
	for _, target := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, ""} {
		t.Run(target, func(t *testing.T) {
			ah := newAccessHarness()
			_, loc := ah.startSignIn(t, target)
			assert.Equal(t, pathSep, loc.Query().Get(queryPath), "anything not local returns to the site's root")
		})
	}
}

func TestSiteSignIn_Refusals(t *testing.T) {
	cases := []struct {
		name string
		opt  accessOption
	}{
		{"no portal", func(a *Access) { a.PortalURL = "" }},
		{"no signing key", func(a *Access) { a.FormKey = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAccessHarness(tc.opt)
			rec := ah.open("/_signin?path=/")
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
			assert.Equal(t, noStore, rec.Header().Get(hdrCache))
			assertNoRelease(t, rec)
		})
	}
}

func TestSiteSignIn_APortalAddressWithATrailingSlash(t *testing.T) {
	ah := newAccessHarness(func(a *Access) { a.PortalURL = portalURL + "/" })
	_, loc := ah.startSignIn(t, "/")
	assert.Equal(t, SiteHandoffPath, loc.Path, "no doubled slash")
}

func TestSecure_CookiesCarryTheHostPrefix(t *testing.T) {
	ah := newAccessHarness(secureAccess)
	assert.Equal(t, securePrefix+cookieSiteViewer, ah.access.cookieName(cookieSiteViewer))

	rec := ah.open("/_signin?path=/")
	require.Equal(t, http.StatusFound, rec.Code)
	cookies := rec.Result().Cookies() //nolint:bodyclose // a recorder's result has no body to close
	require.Len(t, cookies, 1)
	assert.True(t, strings.HasPrefix(cookies[0].Name, securePrefix))
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, pathSep, cookies[0].Path)
	assert.Empty(t, cookies[0].Domain)

	_, loc := ah.startSignIn(t, "/")
	back := ah.runHandoff(t, loc)
	assert.Equal(t, schemeHTTPS, back.Scheme, "the code goes back over https")
}

func TestSiteHandoff_TheAddressIsBuiltFromTheStoredSite(t *testing.T) {
	ah := newAccessHarness(func(a *Access) { a.PublicPort = "8443" })
	_, loc := ah.startSignIn(t, "/")
	q := loc.Query()
	q.Set("host", "evil.example") // nothing in the request names where it goes
	loc.RawQuery = q.Encode()
	back := ah.runHandoff(t, loc)
	assert.Equal(t, testOwnHost+":8443", back.Host)
}

func TestSiteHandoff_Refusals(t *testing.T) {
	good := func(t *testing.T, ah *accessHarness) url.Values {
		t.Helper()
		_, loc := ah.startSignIn(t, "/")
		return loc.Query()
	}
	cases := []struct {
		name   string
		change func(*accessHarness, url.Values)
	}{
		{"an external path", func(_ *accessHarness, q url.Values) { q.Set(queryPath, "https://evil.example/") }},
		{"a protocol-relative path", func(_ *accessHarness, q url.Values) { q.Set(queryPath, "//evil.example/") }},
		{"a state of another shape", func(_ *accessHarness, q url.Values) { q.Set("state", "short") }},
		{"no state", func(_ *accessHarness, q url.Values) { q.Del("state") }},
		{"a site that does not exist", func(_ *accessHarness, q url.Values) { q.Set("site", "nope") }},
		{"no pages domain", func(ah *accessHarness, _ url.Values) { ah.pages = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAccessHarness()
			q := good(t, ah)
			tc.change(ah, q)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, portalURL+SiteHandoffPath+"?"+q.Encode(), nil)
			ah.access.Handoff(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, noStore, rec.Header().Get(hdrCache))
			assert.Equal(t, noReferrer, rec.Header().Get(hdrReferer))
			assert.Empty(t, rec.Header().Get(headerLoc), "nothing is sent anywhere")
		})
	}
}

func TestSiteHandoff_SomeoneNotSignedInIsSentToTheLogin(t *testing.T) {
	cases := []struct {
		name string
		set  func(*accessHarness)
	}{
		{"no session", func(ah *accessHarness) { ah.viewer = nil }},
		{"a session that fails", func(ah *accessHarness) { ah.sessionErr = errors.New("expired") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAccessHarness()
			_, loc := ah.startSignIn(t, "/")
			tc.set(ah)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, loc.String(), nil)
			ah.access.Handoff(rec, req)
			assert.Equal(t, http.StatusFound, rec.Code)
			target := rec.Header().Get(headerLoc)
			assert.Contains(t, target, "return_to=")
			assert.NotContains(t, target, pathSiteAuth, "no code is issued")
		})
	}
}

func TestSiteHandoff_NoSigningKeyCannotIssueACode(t *testing.T) {
	ah := newAccessHarness()
	_, loc := ah.startSignIn(t, "/")
	ah.access.FormKey = nil
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, loc.String(), nil)
	ah.access.Handoff(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestSiteHandoff_TheCodeCarriesTheSession(t *testing.T) {
	ah := newAccessHarness()
	ah.viewer = &SiteViewer{UserID: "u9", Email: viewerAddr, Persona: personaExt, EmailUnverified: true}
	stateCookie, loc := ah.startSignIn(t, "/")
	back := ah.runHandoff(t, loc)
	code := back.Query().Get(queryCode)

	v, err := epform.OpenViewer(ah.access.handoffKey(), siteHandoffScope+testSiteID+":"+digest(stateCookie.Value), code, ah.clock)
	require.NoError(t, err)
	assert.Equal(t, epform.Viewer{UserID: "u9", Email: viewerAddr, Persona: personaExt, EmailUnverified: true}, v)
}

func TestSiteCompleteHandoff_Refusals(t *testing.T) {
	type flow struct {
		ah    *accessHarness
		state *http.Cookie
		back  *url.URL
	}
	start := func(t *testing.T) flow {
		t.Helper()
		ah := newAccessHarness()
		state, loc := ah.startSignIn(t, "/")
		return flow{ah: ah, state: state, back: ah.runHandoff(t, loc)}
	}
	viewerSet := func(rec *httptest.ResponseRecorder, ah *accessHarness) bool {
		for _, c := range rec.Result().Cookies() { //nolint:bodyclose // a recorder's result has no body to close
			if c.Name == ah.access.cookieName(cookieSiteViewer) && c.MaxAge > 0 {
				return true
			}
		}
		return false
	}

	t.Run("a replay is refused: a code works once", func(t *testing.T) {
		f := start(t)
		require.Equal(t, http.StatusFound, f.ah.open(f.back.RequestURI(), f.state).Code)
		require.Len(t, f.ah.db.used, 1)
		rec := f.ah.open(f.back.RequestURI(), f.state)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "already used")
		assert.False(t, viewerSet(rec, f.ah), "no second cookie")
	})
	t.Run("the record is by hash, and outlives the code", func(t *testing.T) {
		f := start(t)
		require.Equal(t, http.StatusFound, f.ah.open(f.back.RequestURI(), f.state).Code)
		code := f.back.Query().Get(queryCode)
		_, stored := f.ah.db.used[digest(code)]
		assert.True(t, stored, "stored as a digest")
		assert.NotContains(t, f.ah.db.used, code, "the code itself is never kept")
		assert.Equal(t, f.ah.clock.Add(2*handoffTTL), f.ah.db.expires)
	})

	cases := []struct {
		name string
		do   func(t *testing.T, f flow) *httptest.ResponseRecorder
	}{
		{"no state cookie: another browser's code", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			return f.ah.open(f.back.RequestURI())
		}},
		{"a state cookie that is not this browser's", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			return f.ah.open(f.back.RequestURI(), &http.Cookie{Name: f.state.Name, Value: "another-browsers-state"}) // #nosec G124
		}},
		{"no code", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			return f.ah.open("/_auth?path=/", f.state)
		}},
		{"an external target", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			q := f.back.Query()
			q.Set(queryPath, "https://evil.example/")
			return f.ah.open(pathSiteAuth+"?"+q.Encode(), f.state)
		}},
		{"a tampered code", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			q := f.back.Query()
			q.Set(queryCode, "x"+q.Get(queryCode))
			return f.ah.open(pathSiteAuth+"?"+q.Encode(), f.state)
		}},
		{"a code for another site", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			return f.ah.send(http.MethodGet, otherHost, f.back.RequestURI(), f.state)
		}},
		{"an expired code", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			f.ah.clock = f.ah.clock.Add(2 * handoffTTL)
			return f.ah.open(f.back.RequestURI(), f.state)
		}},
		{"a viewer cookie presented as a code", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			q := f.back.Query()
			q.Set(queryCode, f.ah.signed(testSiteID, viewerOf(ownerMail), siteViewerTTL))
			return f.ah.open(pathSiteAuth+"?"+q.Encode(), f.state)
		}},
		{"the record of used codes is down", func(_ *testing.T, f flow) *httptest.ResponseRecorder {
			f.ah.db.useErr = errors.New("db down")
			return f.ah.open(f.back.RequestURI(), f.state)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := start(t)
			rec := tc.do(t, f)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Equal(t, noStore, rec.Header().Get(hdrCache))
			assert.Equal(t, noReferrer, rec.Header().Get(hdrReferer))
			assert.False(t, viewerSet(rec, f.ah), "no cookie is set")
			assertNoRelease(t, rec)
		})
	}
}

func TestSiteSignOut(t *testing.T) {
	t.Run("a POST forgets this hostname's sign-in", func(t *testing.T) {
		ah := newAccessHarness()
		rec := ah.send(http.MethodPost, testOwnHost, pathSiteSignOut, ah.cookieFor(viewerOf(ownerMail)))
		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, portalURL+"/portal/auth/logout", rec.Header().Get(headerLoc))
		cookies := rec.Result().Cookies() //nolint:bodyclose // a recorder's result has no body to close
		require.Len(t, cookies, 1)
		assert.Equal(t, cookieSiteViewer, cookies[0].Name)
		assert.Negative(t, cookies[0].MaxAge)
		assert.Empty(t, cookies[0].Value)
	})
	t.Run("a GET changes nothing", func(t *testing.T) {
		ah := newAccessHarness()
		rec := ah.send(http.MethodGet, testOwnHost, pathSiteSignOut, ah.cookieFor(viewerOf(ownerMail)))
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
		assert.Empty(t, rec.Result().Cookies()) //nolint:bodyclose // a recorder's result has no body to close
	})
}

func TestAccessNow_DefaultsToTheClock(t *testing.T) {
	before := time.Now()
	got := (&Access{}).now()
	assert.False(t, got.Before(before))
}

type failingWriter struct {
	http.ResponseWriter
	header http.Header
}

func (f *failingWriter) Header() http.Header     { return f.header }
func (*failingWriter) WriteHeader(int)           {}
func (*failingWriter) Write([]byte) (int, error) { return 0, errors.New("the client hung up") }

func TestPage_AWriteThatFailsIsLogged(t *testing.T) {
	a := &Access{}
	w := &failingWriter{header: http.Header{}}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+testOwnHost+"/", nil)
	a.page(w, r, http.StatusUnauthorized, signInView{Host: testOwnHost, Target: "/"})
	assert.Equal(t, htmlType, w.header.Get(hdrType))
}

// Whatever a site's own address answers past the access check stays private, or
// a removed person could keep a redirect or a not-found page (#2559).
func TestAccess_EveryAnswerPastTheCheckIsPrivate(t *testing.T) {
	for _, tc := range []struct {
		name, method, target string
		status               int
	}{
		{"a redirect rule", http.MethodGet, "/old", http.StatusMovedPermanently},
		{"a path nothing answers", http.MethodGet, "/missing", http.StatusNotFound},
		{"a method a site does not answer", http.MethodPut, "/", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ah := newAccessHarness()
			rec := ah.send(tc.method, testOwnHost, tc.target, ah.cookieFor(viewerOf(ownerMail)))
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Equal(t, cachePrivate, rec.Header().Get(hdrCache))
		})
	}
}
