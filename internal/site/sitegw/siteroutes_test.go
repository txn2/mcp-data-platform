package sitegw

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/site"
)

const publicEnvHost = "marketing--prod.pages.example.com"

// siteRoutes is a site whose private dev (the main address) and public prod
// are parts of the shop application, served with the access guard and the
// page server beside each other (#2604).
type siteRoutes struct {
	*accessHarness
	server *pageServer
	// callers is what each endpoint call's Caller answered.
	callers []*eprun.Caller
	errs    []error
}

func newSiteRoutes(t *testing.T) *siteRoutes {
	t.Helper()
	sr := &siteRoutes{accessHarness: newAccessHarness(), server: &pageServer{}}
	sr.server.fn = func(w http.ResponseWriter, r *http.Request, route ephttp.PageRoute) {
		c, err := route.Caller(r)
		sr.callers, sr.errs = append(sr.callers, c), append(sr.errs, err)
		if c == nil && err == nil {
			route.SignIn(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	pages := &Pages{Apps: &memApps{byName: appsNamed(appShop)}, Server: sr.server, PagesDomain: func() string { return testPagesDomain }}
	routes := sr.store.own
	sr.harness = newHarness(Deps{Access: sr.access, Pages: pages})
	sr.db.store = sr.store
	sr.store.own = routes
	dev := sr.store.own["marketing"]
	dev.PartOf = appShop
	rel := dev.Release
	sr.store.own["marketing--prod"] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "prod", Release: rel, PartOf: appShop}
	return sr
}

func (sr *siteRoutes) call(host, target string, header http.Header, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+host+target, nil)
	maps.Copy(req.Header, header)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	sr.handler.ServeHTTP(rec, req)
	return rec
}

func TestSiteViewer_APrivateEnvironmentSaysWhoSignedIn(t *testing.T) {
	sr := newSiteRoutes(t)
	v := viewerOf(ownerMail)
	v.Name = "Jane Doe"
	cookie := sr.cookieFor(v)
	rec := sr.open(pathViewer, cookie)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, noStore, rec.Header().Get(hdrCache))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "no other origin may read who is signed in")
	var got map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, map[string]string{"email": ownerMail, "user_id": v.UserID, "persona": personaUser, "name": "Jane Doe"}, got)
	assert.NotContains(t, rec.Body.String(), cookie.Value, "the answer never carries the signed cookie")
}

func TestSiteViewer_NobodyOnAPublicEnvironment(t *testing.T) {
	sr := newSiteRoutes(t)
	rec := sr.send(http.MethodGet, publicEnvHost, pathViewer, sr.cookieFor(viewerOf(ownerMail)))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "a public environment has no viewer, whatever cookie arrives")
	assert.Contains(t, rec.Body.String(), "nobody is signed in")
}

func TestSiteViewer_APrivateEnvironmentSignedOutSignsIn(t *testing.T) {
	sr := newSiteRoutes(t)
	rec := sr.open(pathViewer)
	assert.Equal(t, http.StatusFound, rec.Code, "the guard answers before /_viewer")
	assert.Zero(t, sr.server.routes)
}

// A site environment that is a part of an application answers that
// application's endpoints as the person it let in, and only to its own
// origin: a sibling under the pages domain is the same site to a browser.
func TestSiteEndpoint_RunsAsTheViewerFromThisOriginOnly(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		ok     bool
	}{
		{"same origin", http.Header{"Sec-Fetch-Site": {"same-origin"}}, true},
		{"typed or bookmarked", http.Header{"Sec-Fetch-Site": {"none"}}, true},
		{"a sibling site", http.Header{"Sec-Fetch-Site": {"same-site"}}, false},
		{"another site", http.Header{"Sec-Fetch-Site": {"cross-site"}}, false},
		{"no fetch metadata, this origin", http.Header{"Origin": {"http://" + testOwnHost}}, true},
		{"no fetch metadata, a sibling's origin", http.Header{"Origin": {"http://blog.pages.example.com"}}, false},
		{"no fetch metadata and no origin", http.Header{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sr := newSiteRoutes(t)
			rec := sr.call(testOwnHost, "/endpoints/greet/a/b", tc.header, sr.cookieFor(viewerOf(ownerMail)))
			require.Len(t, sr.server.routes, 1, rec.Body.String())
			route := sr.server.routes[0]
			assert.Equal(t, "app_"+appShop, route.Application)
			assert.Equal(t, "greet", route.Name)
			assert.Equal(t, "/a/b", route.Rest)
			assert.NotEmpty(t, route.Nonce)
			if tc.ok {
				require.NoError(t, sr.errs[0])
				require.NotNil(t, sr.callers[0])
				assert.Equal(t, ownerMail, sr.callers[0].Email)
				assert.Equal(t, personaUser, sr.callers[0].Persona)
				assert.False(t, sr.callers[0].Admin)
				return
			}
			require.ErrorIs(t, sr.errs[0], ephttp.ErrForbidden, "the viewer is never lent to another origin")
			assert.Nil(t, sr.callers[0])
		})
	}
}

func TestSiteEndpoint_AdministratorIsTheLivePersona(t *testing.T) {
	sr := newSiteRoutes(t)
	v := viewerOf(ownerMail)
	v.Persona, v.Admin = personaAdmin, false
	sr.call(testOwnHost, "/endpoints/greet", http.Header{"Sec-Fetch-Site": {"same-origin"}}, sr.cookieFor(v))
	require.Len(t, sr.callers, 1)
	require.NotNil(t, sr.callers[0])
	assert.True(t, sr.callers[0].Admin, "read from the persona registry")

	sr = newSiteRoutes(t)
	v = viewerOf(ownerMail)
	v.Admin = true
	sr.call(testOwnHost, "/endpoints/greet", http.Header{"Sec-Fetch-Site": {"same-origin"}}, sr.cookieFor(v))
	require.Len(t, sr.callers, 1)
	assert.False(t, sr.callers[0].Admin, "never from the cookie")
}

func TestSiteEndpoint_APublicEnvironmentRunsAsNobody(t *testing.T) {
	sr := newSiteRoutes(t)
	rec := sr.call(publicEnvHost, "/endpoints/greet", http.Header{"Sec-Fetch-Site": {"same-origin"}}, sr.cookieFor(viewerOf(ownerMail)))
	require.Len(t, sr.callers, 1)
	assert.Nil(t, sr.callers[0])
	require.NoError(t, sr.errs[0])
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "an endpoint that needs someone has no sign-in on a public environment")
	assert.Contains(t, rec.Body.String(), "make the environment private")
}

func TestSiteEndpoint_OnlyForAPartOfAnApplication(t *testing.T) {
	sr := newSiteRoutes(t)
	sr.store.own["marketing"].PartOf = ""
	sr.call(testOwnHost, "/endpoints/greet", http.Header{"Sec-Fetch-Site": {"same-origin"}}, sr.cookieFor(viewerOf(ownerMail)))
	assert.Empty(t, sr.server.routes, "a site that is no part answers /endpoints/ from its files")

	sr = newSiteRoutes(t)
	sr.store.own["marketing"].PartOf = "gone"
	rec := sr.call(testOwnHost, "/endpoints/greet", http.Header{"Sec-Fetch-Site": {"same-origin"}}, sr.cookieFor(viewerOf(ownerMail)))
	assert.Equal(t, http.StatusNotFound, rec.Code, "an application that no longer exists answers nothing")
	assert.Empty(t, sr.server.routes)
}

// A persona grant follows the persona the viewer's roles resolve to now, not
// the one they signed in with (#2604).
func TestAccess_APersonaGrantReadsTheLivePersona(t *testing.T) {
	ah := newAccessHarness(func(a *Access) {
		a.PersonaFor = func(roles []string) string {
			if slices.Contains(roles, "fin") {
				return "finance-team"
			}
			return ""
		}
	})
	ah.db.grants[testSiteID+"/persona:finance-team"] = site.LevelView
	v := epform.Viewer{UserID: "u-x", Email: "x@example.com", Persona: "finance-team", Roles: []string{"fin"}}
	assert.Equal(t, http.StatusOK, ah.open("/", ah.cookieFor(v)).Code, "a member of the persona opens it")

	v.Roles = []string{"sales"}
	assert.Equal(t, http.StatusForbidden, ah.open("/", ah.cookieFor(v)).Code,
		"moved out of the persona, refused on the next request though the cookie still names it")
}

func TestAccess_ANarrowedPersonaGrantOpensOnlyItsEnvironment(t *testing.T) {
	ah := newAccessHarness()
	ah.db.grants[testSiteID+"/persona:"+personaUser] = "view:prod"
	assert.Equal(t, http.StatusForbidden, ah.open("/", ah.cookieFor(viewerOf("x@example.com"))).Code, "the grant names prod, and this is dev")

	ah.db.grants[testSiteID+"/persona:"+personaUser] = "view:" + site.EnvDev
	assert.Equal(t, http.StatusOK, ah.open("/", ah.cookieFor(viewerOf("x@example.com"))).Code)
}

func TestAccess_InternalViewNeedsAPersona(t *testing.T) {
	ah := newAccessHarness()
	ah.db.access[testSiteID].InternalView = true
	v := viewerOf("x@example.com")
	v.Persona = ""
	assert.Equal(t, http.StatusForbidden, ah.open("/", ah.cookieFor(v)).Code, "someone with no persona is not internal")
}

func TestNotPublished_SaysNothingAboutTheSite(t *testing.T) {
	h := newHarness(Deps{})
	rec := h.get("docs.pages.example.com", "/secret/path")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Nothing is published here")
	assert.Contains(t, rec.Body.String(), "docs.pages.example.com")
	assert.NotContains(t, rec.Body.String(), "/secret/path")
	assert.Equal(t, noIndex, rec.Header().Get(hdrRobots))

	head := h.do(http.MethodHead, "docs.pages.example.com", "/", nil)
	assert.Equal(t, http.StatusNotFound, head.Code)
	assert.Empty(t, head.Body.String())
}

// A site calling its application's endpoints keeps the application's cookies
// (#2555): the same prefix as the application's own address, and a write that
// does not prove it came from the site's own address is handed on with its
// cookies withheld.
func TestSiteEndpoint_HandsTheEndpointTheApplicationsCookieRule(t *testing.T) {
	sr := newSiteRoutes(t)
	sr.call(publicEnvHost, "/endpoints/cart", http.Header{"Sec-Fetch-Site": {"same-origin"}})
	require.Len(t, sr.server.routes, 1)
	route := sr.server.routes[0]
	assert.Equal(t, eprun.CookiePrefixPlain, route.CookiePrefix, "the harness serves plain http")
	assert.False(t, route.CookiesWithheld, "a write from the site's own address carries its cookies")

	sr.call(publicEnvHost, "/endpoints/cart", http.Header{"Origin": {"https://elsewhere.example"}})
	require.Len(t, sr.server.routes, 2)
	assert.True(t, sr.server.routes[1].CookiesWithheld, "a cross-origin write carries none")
}
