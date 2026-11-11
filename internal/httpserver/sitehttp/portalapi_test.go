package sitehttp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

const (
	siteID      = "site_1"
	siteName    = "marketing"
	base        = "/api/v1/portal/sites"
	siteBase    = base + "/" + siteName
	envProd     = "prod"
	pagesDomain = "pages.example.net"
	internalErr = "this could not be done; try again"
	secretText  = "10.1.2.3"
)

func callerOf(ctx context.Context) string {
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		return pc.UserID + "/" + pc.UserEmail + "/" + pc.PersonaName
	}
	return ""
}

var (
	userOwner    = &portal.User{UserID: "u-owner", Email: "jane@example.com", Roles: []string{"analyst"}}
	userManager  = &portal.User{UserID: "u-mo", Email: "mo@example.com", Roles: []string{"analyst"}}
	userViewer   = &portal.User{UserID: "u-vi", Email: "vi@client.example", Roles: []string{"guest"}}
	userStranger = &portal.User{UserID: "u-joe", Email: "joe@example.com", Roles: []string{"analyst"}}
	userAdmin    = &portal.User{UserID: "u-admin", Email: "admin@example.com", Roles: []string{"admin"}}
)

type fixture struct {
	store    *memStore
	objects  *memObjects
	audit    *memAudit
	sharer   *memSharer
	sources  *memSources
	crawler  *memCrawler
	rt       *sitewire.Runtime
	mux      *http.ServeMux
	external map[string]bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store: newMem(), objects: &memObjects{put: map[string][]byte{}}, audit: &memAudit{}, sharer: &memSharer{},
		sources:  &memSources{files: []sitesvc.InputFile{{Path: "index.html", Body: []byte("<p>home</p>")}}},
		crawler:  &memCrawler{result: &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: "index.html", Body: []byte("copied")}}, Pages: 2}},
		external: map[string]bool{},
	}
	f.store.sites[siteID] = &site.Site{
		ID: siteID, Name: siteName, OwnerEmail: userOwner.Email, CreatedBy: userOwner.Email, MainEnvironment: site.EnvDev,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: "stage"}, {Name: envProd, RequiresApproval: true}},
	}
	f.store.grants[siteID] = map[string]site.Grant{
		userManager.Email: {Email: userManager.Email, Level: site.LevelManage},
		userViewer.Email:  {Email: userViewer.Email, Level: site.LevelView},
	}
	cfg := siteconfig.Config{PagesDomain: pagesDomain}
	live := siteconfig.NewLive(cfg)
	svc := &sitesvc.Service{
		Store: f.store, Objects: f.objects, Bucket: "portal", Config: cfg, Live: live,
		Apps: fakeApps{"shop": {ID: "app_shop", Name: "shop"}}, Approvals: &fakeApprovals{},
		PortalHost: "portal.example.com", ReservedHosts: []string{"mcp.example.org"}, PublicScheme: "https",
		External: func(_ context.Context, email string) bool { return f.external[email] },
	}
	f.rt = &sitewire.Runtime{
		Service: svc, Config: cfg, Live: live, Sources: f.sources, Crawler: f.crawler, PortalURL: "https://portal.example.com",
	}
	f.mux = http.NewServeMux()
	(&portalAPI{d: PortalDeps{
		Runtime: f.rt, Audit: f.audit, Sharer: f.sharer,
		IsAdmin:  func(roles []string) bool { return len(roles) == 1 && roles[0] == "admin" },
		Personas: func() []string { return []string{"finance-team", "web-team"} },
		Persona: func(roles []string) *portal.PersonaInfo {
			if len(roles) == 0 {
				return nil
			}
			info := &portal.PersonaInfo{Name: roles[0]}
			if roles[0] == "analyst" {
				info.Tools = []string{"manage_site", "trino_query"}
			}
			return info
		},
	}}).register(f.mux)
	return f
}

// release files a numbered release and, when env is named, points it there.
func (f *fixture) release(n int, env string) string {
	id := "srel_" + strconv.Itoa(n)
	r := site.Release{ID: id, SiteID: siteID, Number: n, ManifestSHA256: "digest", Redirects: []site.Redirect{}}
	f.store.releases[siteID] = append([]site.Release{r}, f.store.releases[siteID]...)
	f.store.files[id] = []site.File{{Path: "index.html", SHA256: "d" + id, ContentType: "text/html", Size: 4}}
	if env != "" {
		e := f.store.sites[siteID].Environment(env)
		f.store.deployments[siteID] = append([]site.Deployment{{Environment: env, Action: site.ActionPublish, From: e.Release, To: n}}, f.store.deployments[siteID]...)
		e.Release = n
	}
	return id
}

func (f *fixture) do(t *testing.T, method, target string, u *portal.User, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	switch v := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(v))
	default:
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, target, reader)
	if u != nil {
		req = req.WithContext(portal.ContextWithUser(req.Context(), u))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func awaitJob(t *testing.T, f *fixture) site.Job {
	t.Helper()
	select {
	case j := <-f.store.finished:
		return j
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the job never finished")
		return site.Job{}
	}
}

// route is one request a test sends.
type route struct {
	method, target string
	body           any
}

func (f *fixture) statusIs(t *testing.T, want int, r route, u *portal.User) *httptest.ResponseRecorder {
	t.Helper()
	rec := f.do(t, r.method, r.target, u, r.body)
	require.Equal(t, want, rec.Code, rec.Body.String())
	return rec
}

func TestPortalRegistrar(t *testing.T) {
	assert.Nil(t, PortalRegistrar(PortalDeps{}), "no runtime, no routes")

	f := newFixture(t)
	reg := PortalRegistrar(PortalDeps{Runtime: f.rt})
	require.NotNil(t, reg)
	mux := http.NewServeMux()
	reg(mux)

	req := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), userOwner), http.MethodGet, base, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// Every route that says who is asking refuses when nobody is.
func TestPortal_NoUserIs401(t *testing.T) {
	routes := []struct{ method, target string }{
		{http.MethodGet, base},
		{http.MethodPost, base},
		{http.MethodGet, siteBase},
		{http.MethodPatch, siteBase},
		{http.MethodDelete, siteBase},
		{http.MethodPost, siteBase + "/publish"},
		{http.MethodPost, siteBase + "/copy"},
		{http.MethodPost, siteBase + "/jobs/x/stop"},
		{http.MethodPost, siteBase + "/promote"},
		{http.MethodPost, siteBase + "/rollback"},
		{http.MethodPut, siteBase + "/environments/qa"},
		{http.MethodPost, siteBase + "/domains"},
		{http.MethodDelete, siteBase + "/domains/a.example.org"},
		{http.MethodGet, siteBase + "/access"},
		{http.MethodPut, siteBase + "/access"},
		{http.MethodDelete, siteBase + "/access?email=a@b.example"},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.target, func(t *testing.T) {
			f := newFixture(t)
			rec := f.do(t, r.method, r.target, nil, map[string]any{})
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Zero(t, f.audit.count(), "nothing was recorded")
		})
	}
}

// fail answers each error from the service with its status and a fixed
// message, never the error's own text.
func TestFail(t *testing.T) {
	cases := []struct {
		err    error
		status int
		text   string
	}{
		{&sitesvc.RefusedError{Msg: "why not"}, http.StatusUnprocessableEntity, "why not"},
		{site.ErrNotAllowed, http.StatusForbidden, "you do not have permission"},
		{site.ErrNotFound, http.StatusNotFound, "no such site"},
		{site.ErrNoEnvironment, http.StatusNotFound, "no such environment"},
		{site.ErrNoRelease, http.StatusNotFound, "no such release"},
		{site.ErrNoDomain, http.StatusNotFound, "no such domain"},
		{site.ErrNoGrant, http.StatusNotFound, "no access"},
		{site.ErrNoJob, http.StatusNotFound, "no such job"},
		{site.ErrNameTaken, http.StatusConflict, "a site with that name"},
		{site.ErrDomainTaken, http.StatusConflict, "already in use"},
		{site.ErrStale, http.StatusConflict, "moved this environment first"},
		{errBoom, http.StatusInternalServerError, internalErr},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			fail(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil), tc.err)
			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.text)
			assert.NotContains(t, rec.Body.String(), secretText, "what the store said stays in the log")
		})
	}
}

// Writes the service refuses, through the routes, with their real status.
func TestPortal_WritesRefused(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		user   *portal.User
		body   any
		status int
		want   string
	}{
		{"update, stranger", http.MethodPatch, siteBase, userStranger, map[string]any{"description": "x"}, 403, "permission"},
		{"update, manager's description", http.MethodPatch, siteBase, userManager, map[string]any{"description": "x"}, 403, "permission"},
		{"update, unknown site", http.MethodPatch, base + "/nope", userAdmin, map[string]any{"description": "x"}, 404, "no such site"},
		{"update, owner not an email", http.MethodPatch, siteBase, userAdmin, map[string]any{"owner_email": "jane"}, 422, "an email address"},
		{"update, bad JSON", http.MethodPatch, siteBase, userOwner, "{", 400, "not valid JSON"},
		{"delete, owner", http.MethodDelete, siteBase, userOwner, nil, 403, "permission"},
		{"delete, unknown", http.MethodDelete, base + "/nope", userAdmin, nil, 404, "no such site"},
		{"promote, stranger", http.MethodPost, siteBase + "/promote", userStranger, map[string]any{"to": "stage", "release": 1}, 403, "permission"},
		{"promote, unknown environment", http.MethodPost, siteBase + "/promote", userOwner, map[string]any{"to": "qa", "release": 1}, 404, "no such environment"},
		{"promote, unknown release", http.MethodPost, siteBase + "/promote", userOwner, map[string]any{"to": "stage", "release": 9}, 404, "no such release"},
		{"promote, bad JSON", http.MethodPost, siteBase + "/promote", userOwner, "{", 400, "not valid JSON"},
		{"rollback, no earlier release", http.MethodPost, siteBase + "/rollback", userOwner, map[string]any{"environment": "dev"}, 422, "no earlier release"},
		{"rollback, stranger", http.MethodPost, siteBase + "/rollback", userStranger, map[string]any{"environment": "default"}, 403, "permission"},
		{"rollback, unknown environment", http.MethodPost, siteBase + "/rollback", userOwner, map[string]any{"environment": "qa"}, 404, "no such environment"},
		{"rollback, bad JSON", http.MethodPost, siteBase + "/rollback", userOwner, "{", 400, "not valid JSON"},
		{"environment, stranger", http.MethodPut, siteBase + "/environments/qa", userStranger, map[string]any{"requires_approval": true}, 403, "permission"},
		{"environment, bad name", http.MethodPut, siteBase + "/environments/Bad%20Name", userOwner, map[string]any{"requires_approval": true}, 422, "environment name"},
		{"environment, owner takes the rule off", http.MethodPut, siteBase + "/environments/prod", userOwner, map[string]any{"requires_approval": false}, 403, "permission"},
		{"environment, bad JSON", http.MethodPut, siteBase + "/environments/qa", userOwner, "{", 400, "not valid JSON"},
		{"environment, a limit that is not one", http.MethodPut, siteBase + "/environments/prod", userOwner, map[string]any{"keep_releases": "ten"}, 400, "keep_releases"},
		{"environment, a manager changing the limit", http.MethodPut, siteBase + "/environments/prod", userManager, map[string]any{"keep_releases": 3}, 403, "permission"},
		{"domain, stranger", http.MethodPost, siteBase + "/domains", userStranger, map[string]any{"hostname": "a.example.org", "environment": "x"}, 403, "permission"},
		{"domain, viewer", http.MethodPost, siteBase + "/domains", userViewer, map[string]any{"hostname": "a.example.org", "environment": "x"}, 403, "permission"},
		{"domain, reserved host", http.MethodPost, siteBase + "/domains", userOwner, map[string]any{"hostname": "mcp.example.org", "environment": "x"}, 422, "answers on itself"},
		{"domain, the portal", http.MethodPost, siteBase + "/domains", userOwner, map[string]any{"hostname": "portal.example.com", "environment": "x"}, 422, "answers on itself"},
		{"domain, the pages domain", http.MethodPost, siteBase + "/domains", userOwner, map[string]any{"hostname": "a.pages.example.net", "environment": "x"}, 422, "pages domain"},
		{"domain, dev", http.MethodPost, siteBase + "/domains", userOwner, map[string]any{"hostname": "a.example.org", "environment": "dev"}, 422, "cannot have a domain"},
		{"domain, dev by its old name", http.MethodPost, siteBase + "/domains", userOwner, map[string]any{"hostname": "a.example.org", "environment": "default"}, 422, "cannot have a domain"},
		{"domain, unknown site", http.MethodPost, base + "/nope/domains", userOwner, map[string]any{"hostname": "a.example.org", "environment": "x"}, 404, "no such site"},
		{"domain, bad JSON", http.MethodPost, siteBase + "/domains", userOwner, "{", 400, "not valid JSON"},
		{"access, stranger", http.MethodGet, siteBase + "/access", userStranger, nil, 403, "permission"},
		{"access, viewer", http.MethodGet, siteBase + "/access", userViewer, nil, 403, "permission"},
		{"access, unknown site", http.MethodGet, base + "/nope/access", userOwner, nil, 404, "no such site"},
		{"grant, stranger", http.MethodPut, siteBase + "/access", userStranger, map[string]any{"email": "x@example.com", "level": "view"}, 403, "permission"},
		{"grant, manager adding a manager", http.MethodPut, siteBase + "/access", userManager, map[string]any{"email": "x@example.com", "level": "manage"}, 403, "permission"},
		{"grant, bad level", http.MethodPut, siteBase + "/access", userOwner, map[string]any{"email": "x@example.com", "level": "owner"}, 422, "level:"},
		{"grant, not an email", http.MethodPut, siteBase + "/access", userOwner, map[string]any{"email": "nobody", "level": "view"}, 422, "email"},
		{"grant, unknown site", http.MethodPut, base + "/nope/access", userOwner, map[string]any{"email": "x@example.com", "level": "view"}, 404, "no such site"},
		{"grant, bad JSON", http.MethodPut, siteBase + "/access", userOwner, "{", 400, "not valid JSON"},
		{"revoke, stranger", http.MethodDelete, siteBase + "/access?email=vi@client.example", userStranger, nil, 403, "permission"},
		{"revoke, manager on a manager", http.MethodDelete, siteBase + "/access?email=mo@example.com", userManager, nil, 403, "permission"},
		{"revoke, no such grant", http.MethodDelete, siteBase + "/access?email=none@example.com", userOwner, nil, 404, "no access"},
		{"revoke, no email", http.MethodDelete, siteBase + "/access", userOwner, nil, 400, "email or persona is required"},
		{"revoke, email and persona", http.MethodDelete, siteBase + "/access?email=vi@client.example&persona=finance", userOwner, nil, 422, "an email or a persona"},
		{"grant, email and persona", http.MethodPut, siteBase + "/access", userOwner, map[string]any{"email": "x@example.com", "persona": "finance", "level": "view"}, 422, "not both"},
		{"grant, a persona to manage", http.MethodPut, siteBase + "/access", userOwner, map[string]any{"persona": "finance", "level": "manage"}, 422, "manage is given to a person"},
		{"grant, an environment the site lacks", http.MethodPut, siteBase + "/access", userOwner, map[string]any{"email": "x@example.com", "level": "view", "environments": []string{"qa"}}, 404, "no such environment"},
		{"environment, a manager making it public", http.MethodPut, siteBase + "/environments/stage", userManager, map[string]any{"public": true}, 403, "permission"},
		{"environment, a public dev", http.MethodPut, siteBase + "/environments/dev", userOwner, map[string]any{"public": true}, 422, "always private"},
		{"environment, bad JSON", http.MethodPut, siteBase + "/environments/stage", userOwner, "{", 400, "not valid JSON"},
		{"main address, a manager", http.MethodPut, siteBase + "/main-environment", userManager, map[string]any{"environment": "stage"}, 403, "permission"},
		{"main address, no such environment", http.MethodPut, siteBase + "/main-environment", userOwner, map[string]any{"environment": "qa"}, 404, "no such environment"},
		{"main address, bad JSON", http.MethodPut, siteBase + "/main-environment", userOwner, "{", 400, "not valid JSON"},
		{"grant, no email", http.MethodPut, siteBase + "/access", userOwner, map[string]any{"level": "view"}, 422, "email"},
		{"revoke, unknown site", http.MethodDelete, base + "/nope/access?email=x@example.com", userOwner, nil, 404, "no such site"},
		{"stop, stranger", http.MethodPost, siteBase + "/jobs/sjob_1/stop", userStranger, nil, 403, "permission"},
		{"stop, unknown job", http.MethodPost, siteBase + "/jobs/none/stop", userOwner, nil, 404, "no such job"},
		{"publish, stranger", http.MethodPost, siteBase + "/publish", userStranger, map[string]any{"archive": "a"}, 403, "permission"},
		{"publish, bad JSON", http.MethodPost, siteBase + "/publish", userOwner, "{", 400, "not valid JSON"},
		{"publish, no source", http.MethodPost, siteBase + "/publish", userOwner, map[string]any{}, 422, "one source"},
		{"publish, two sources", http.MethodPost, siteBase + "/publish", userOwner, map[string]any{"archive": "a", "folder": "b"}, 422, "one source"},
		{"publish, an environment people use", http.MethodPost, siteBase + "/publish", userOwner, map[string]any{"archive": "a", "environment": "prod"}, 422, `environment \"prod\" is read-only`},
		{"publish, any environment but dev", http.MethodPost, siteBase + "/publish", userOwner, map[string]any{"archive": "a", "environment": "qa"}, 422, "is read-only"},
		{"publish, unknown site", http.MethodPost, base + "/nope/publish", userOwner, map[string]any{"archive": "a"}, 404, "no such site"},
		{"copy, stranger", http.MethodPost, siteBase + "/copy", userStranger, map[string]any{"url": "https://a.example/"}, 403, "permission"},
		{"copy, bad JSON", http.MethodPost, siteBase + "/copy", userOwner, "{", 400, "not valid JSON"},
		{"copy, any environment but dev", http.MethodPost, siteBase + "/copy", userOwner, map[string]any{"url": "https://a.example/", "environment": "qa"}, 422, "is read-only"},
		{"create, bad name", http.MethodPost, base, userOwner, map[string]any{"name": "Bad Name"}, 422, "site name"},
		{"create, bad JSON", http.MethodPost, base, userOwner, "{", 400, "not valid JSON"},
		{"create, taken", http.MethodPost, base, userOwner, map[string]any{"name": siteName}, 409, "a site with that name"},
		{"create, an application's name", http.MethodPost, base, userOwner, map[string]any{"name": "shop"}, 422, "name of an application"},
		{"create, a persona without manage_site", http.MethodPost, base, userViewer, map[string]any{"name": "blog"}, 403, "does not include"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.jobs["sjob_1"] = &site.Job{ID: "sjob_1", SiteID: siteID, Status: site.JobRunning, UpdatedAt: time.Now()}
			rec := f.do(t, tc.method, tc.target, tc.user, tc.body)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}
}

// Reads that find nothing, and store failures that say nothing of what broke.
func TestPortal_ReadsThatFail(t *testing.T) {
	missing := []struct{ name, target, want string }{
		{"get", base + "/nope", "no such site"},
		{"releases", base + "/nope/releases", "no such site"},
		{"release", base + "/nope/releases/1", "no such site"},
		{"compare", base + "/nope/compare?to=1", "no such site"},
		{"deployments", base + "/nope/deployments", "no such site"},
		{"jobs", base + "/nope/jobs", "no such site"},
		{"job", base + "/nope/jobs/x", "no such site"},
		{"no such release", siteBase + "/releases/9", "no such release"},
		{"no such job", siteBase + "/jobs/none", "no such job"},
		{"compare a release that does not exist", siteBase + "/compare?to=9", "no such release"},
	}
	for _, tc := range missing {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.statusIs(t, http.StatusNotFound, route{http.MethodGet, tc.target, nil}, userOwner)
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}

	badRequests := []struct{ name, target, want string }{
		{"a release number that is not one", siteBase + "/releases/one", "a release number"},
		{"to that is not a number", siteBase + "/compare?to=x", "to:"},
		{"to absent", siteBase + "/compare", "to:"},
		{"from that is not a number", siteBase + "/compare?to=1&from=x", "from:"},
	}
	for _, tc := range badRequests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.statusIs(t, http.StatusBadRequest, route{http.MethodGet, tc.target, nil}, userOwner)
			assert.Contains(t, rec.Body.String(), tc.want)
		})
	}

	failures := []struct {
		name    string
		target  string
		breakIt func(*memStore)
	}{
		{"list, sites", base, func(m *memStore) { m.failList = errBoom }},
		{"list, domains", base, func(m *memStore) { m.failDomains = errBoom }},
		{"list, permissions", base, func(m *memStore) { m.failGrant = errBoom }},
		{"get, site", siteBase, func(m *memStore) { m.failGet = errBoom }},
		{"get, domains", siteBase, func(m *memStore) { m.failSiteDomains = errBoom }},
		{"get, permissions", siteBase, func(m *memStore) { m.failGrant = errBoom }},
		{"releases, site", siteBase + "/releases", func(m *memStore) { m.failGet = errBoom }},
		{"releases, list", siteBase + "/releases", func(m *memStore) { m.failReleases = errBoom }},
		{"release, files", siteBase + "/releases/1", func(m *memStore) { m.failFiles = errBoom }},
		{"deployments", siteBase + "/deployments", func(m *memStore) { m.failDeployments = errBoom }},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.release(1, site.EnvDev)
			tc.breakIt(f.store)
			rec := f.statusIs(t, http.StatusInternalServerError, route{http.MethodGet, tc.target, nil}, userStranger)
			assert.Contains(t, rec.Body.String(), internalErr)
			assert.NotContains(t, rec.Body.String(), secretText)
		})
	}
}

func TestPortal_ListAndGet(t *testing.T) {
	f := newFixture(t)
	f.store.domains["www.client.example"] = &site.Domain{Hostname: "www.client.example", SiteID: siteID, Environment: envProd}

	got := decodeBody[SiteList](t, f.statusIs(t, 200, route{http.MethodGet, base, nil}, userOwner))
	require.Len(t, got.Sites, 1)
	assert.Equal(t, 1, got.Total)
	assert.Equal(t, pagesDomain, got.PagesDomain)
	assert.Equal(t, "https://marketing.pages.example.net", got.Sites[0].Address)
	assert.Equal(t, sitesvc.Permissions{Manage: true, Own: true}, got.Sites[0].Permissions)
	require.Len(t, got.Sites[0].Domains, 1)
	assert.Equal(t, siteconfig.DefaultMaxFiles, got.Limits.MaxFiles)
	assert.Equal(t, siteconfig.DefaultCrawlPages, got.Limits.CrawlMaxPages)
	assert.True(t, got.CanCreate, "the persona holds manage_site")
	assert.False(t, got.Admin)

	viewer := decodeBody[SiteList](t, f.statusIs(t, 200, route{http.MethodGet, base, nil}, userViewer))
	assert.Equal(t, sitesvc.Permissions{}, viewer.Sites[0].Permissions)
	assert.False(t, viewer.CanCreate, "no manage_site, no creating")

	admin := decodeBody[SiteList](t, f.statusIs(t, 200, route{http.MethodGet, base, nil}, userAdmin))
	assert.Equal(t, sitesvc.Permissions{Manage: true, Own: true, Delete: true}, admin.Sites[0].Permissions)
	assert.True(t, admin.CanCreate)
	assert.True(t, admin.Admin)

	one := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userManager))
	assert.Equal(t, siteName, one.Name)
	assert.Equal(t, sitesvc.Permissions{Manage: true}, one.Permissions)
	require.Len(t, one.Domains, 1)
	byID := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, base + "/" + siteID, nil}, userManager))
	assert.Equal(t, siteName, byID.Name, "a site is read by id too")
}

func TestPortal_EmptyListsAreNeverNull(t *testing.T) {
	f := newFixture(t)
	delete(f.store.sites, siteID)
	body := f.statusIs(t, 200, route{http.MethodGet, base, nil}, userOwner).Body.String()
	assert.Contains(t, body, `"sites":[]`)
	assert.NotContains(t, body, "null")

	f = newFixture(t)
	body = f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userOwner).Body.String()
	assert.Contains(t, body, `"domains":[]`)
}

func TestPortal_AnUnverifiedEmailMatchesNoOneThroughTheRoutes(t *testing.T) {
	unverifiedOwner := &portal.User{UserID: "u-owner", Email: userOwner.Email, Roles: []string{"analyst"}, Claims: map[string]any{claimUnverif: false}}

	f := newFixture(t)
	view := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, unverifiedOwner))
	assert.Equal(t, sitesvc.Permissions{}, view.Permissions)

	f.statusIs(t, http.StatusForbidden, route{http.MethodGet, siteBase + "/access", nil}, unverifiedOwner)
	f.statusIs(t, http.StatusForbidden, route{http.MethodPost, siteBase + "/promote", map[string]any{"to": "stage", "release": 1}}, unverifiedOwner)

	verified := &portal.User{UserID: "u-owner", Email: userOwner.Email, Roles: []string{"analyst"}, Claims: map[string]any{claimUnverif: true}}
	f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/access", nil}, verified)
}

func TestPortal_CreateAndDelete(t *testing.T) {
	f := newFixture(t)
	created := decodeBody[SiteView](t, f.statusIs(t, http.StatusCreated, route{http.MethodPost, base, map[string]any{"name": "blog", "description": "d"}}, userOwner))
	assert.Equal(t, "blog", created.Name)
	assert.Equal(t, userOwner.Email, created.OwnerEmail)
	assert.Equal(t, "https://blog.pages.example.net", created.Address)
	require.Len(t, created.Environments, 1)
	assert.Equal(t, site.EnvDev, created.Environments[0].Name)

	ev := f.audit.last()
	assert.Equal(t, "manage_site", ev.ToolName)
	assert.Equal(t, userOwner.Email, ev.UserEmail)
	assert.Equal(t, "analyst", ev.Persona)
	assert.Equal(t, "create", ev.Parameters["command"])
	assert.True(t, ev.Success)
	assert.Equal(t, "portal", ev.Source)

	f.statusIs(t, http.StatusNoContent, route{http.MethodDelete, base + "/blog", nil}, userAdmin)
	assert.Len(t, f.store.sites, 1)
}

func TestPortal_CreateAFailureIsAuditedWithoutAnswering(t *testing.T) {
	f := newFixture(t)
	f.store.failCreate = errBoom
	f.audit.err = errBoom
	rec := f.statusIs(t, http.StatusInternalServerError, route{http.MethodPost, base, map[string]any{"name": "blog"}}, userOwner)
	assert.NotContains(t, rec.Body.String(), secretText)
	ev := f.audit.last()
	assert.False(t, ev.Success)
	assert.Contains(t, ev.ErrorMessage, secretText, "the audit row carries it; the answer does not")
}

func TestPortal_Update(t *testing.T) {
	f := newFixture(t)
	got := decodeBody[site.Site](t, f.statusIs(t, 200, route{http.MethodPatch, siteBase, map[string]any{"description": "new words"}}, userOwner))
	assert.Equal(t, "new words", got.Description)

	got = decodeBody[site.Site](t, f.statusIs(t, 200, route{http.MethodPatch, siteBase, map[string]any{"owner_email": "jane@example.com"}}, userOwner))
	assert.Equal(t, "new words", got.Description, "a field left out is left alone")

	got = decodeBody[site.Site](t, f.statusIs(t, 200, route{http.MethodPatch, siteBase, map[string]any{"owner_email": "new@example.com"}}, userAdmin))
	assert.Equal(t, "new@example.com", got.OwnerEmail)
}

func TestPortal_ReleasesAndDeployments(t *testing.T) {
	f := newFixture(t)
	f.release(1, "")
	f.release(2, site.EnvDev)

	list := decodeBody[ReleaseList](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/releases", nil}, userViewer))
	require.Len(t, list.Releases, 2)
	assert.Equal(t, 2, list.Releases[0].Number)
	assert.Equal(t, []string{site.EnvDev}, list.Releases[0].ServedIn)
	assert.Equal(t, []string{}, list.Releases[1].ServedIn, "none is [], never null")

	detail := decodeBody[ReleaseDetail](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/releases/2", nil}, userViewer))
	require.Len(t, detail.Files, 1)
	assert.Equal(t, "index.html", detail.Files[0].Path)

	history := decodeBody[DeploymentList](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/deployments?environment=dev", nil}, userViewer))
	assert.Len(t, history.Deployments, 1)
	none := decodeBody[DeploymentList](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/deployments?environment=stage", nil}, userViewer))
	assert.Equal(t, []site.Deployment{}, none.Deployments)
}

func TestPortal_Compare(t *testing.T) {
	f := newFixture(t)
	idOne := f.release(1, "")
	idTwo := f.release(2, "")
	f.store.files[idOne] = []site.File{{Path: "a.html", SHA256: "before"}, {Path: "gone.html", SHA256: "g"}}
	f.store.files[idTwo] = []site.File{{Path: "a.html", SHA256: "after"}, {Path: "new.html", SHA256: "n"}}

	got := decodeBody[sitesvc.Comparison](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/compare?from=1&to=2", nil}, userViewer))
	assert.Equal(t, []string{"a.html"}, got.Changed)
	assert.Equal(t, []string{"new.html"}, got.Added)
	assert.Equal(t, []string{"gone.html"}, got.Removed)

	first := decodeBody[sitesvc.Comparison](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/compare?to=1", nil}, userViewer))
	assert.Zero(t, first.From, "no from compares against nothing")
	assert.Len(t, first.Added, 2)
}

func TestPortal_PublishRunsApartAndAsTheCaller(t *testing.T) {
	f := newFixture(t)
	rec := f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/publish", map[string]any{"archive": "mcp:resource:site.zip", "note": "go"}}, userManager)
	job := decodeBody[site.Job](t, rec)
	assert.Equal(t, site.JobPublish, job.Kind)
	assert.Equal(t, "mcp:resource:site.zip", job.Source)

	done := awaitJob(t, f)
	assert.Equal(t, site.JobDone, done.Status)
	assert.Equal(t, 1, done.Release)
	assert.Equal(t, "mcp:resource:site.zip", f.sources.archive)
	assert.Equal(t, []string{"u-mo/mo@example.com/analyst"}, f.sources.whoSaw(), "the file is read as the caller, so nobody publishes what they could not open")
	assert.Equal(t, "publish", f.audit.last().Parameters["command"])

	f.store.mu.Lock()
	assert.Equal(t, 1, f.store.sites[siteID].Environment(site.EnvDev).Release)
	f.store.mu.Unlock()
}

func TestPortal_PublishFromAFolderNamesItsLibrary(t *testing.T) {
	f := newFixture(t)
	f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/publish", map[string]any{"folder": "web", "scope": "persona", "scope_id": "analyst"}}, userOwner)
	awaitJob(t, f)
	assert.Equal(t, []string{"persona", "analyst", "web"}, f.sources.scope)
}

func TestPortal_PublishWithNoManagedResources(t *testing.T) {
	f := newFixture(t)
	f.rt.Sources = nil
	rec := f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPost, siteBase + "/publish", map[string]any{"archive": "a"}}, userOwner)
	assert.Contains(t, rec.Body.String(), "no managed resources")
}

func TestPortal_CopyKeepsNoSecretOfTheAddress(t *testing.T) {
	f := newFixture(t)
	address := "https://user:s3cret@old.example.org/start?token=t0ken#frag"
	rec := f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/copy", map[string]any{"url": address}}, userOwner)
	job := decodeBody[site.Job](t, rec)
	assert.Equal(t, "crawl https://old.example.org/start", job.Source)
	assert.NotContains(t, rec.Body.String(), "s3cret")
	assert.NotContains(t, rec.Body.String(), "t0ken")

	done := awaitJob(t, f)
	assert.Equal(t, site.JobDone, done.Status)
	assert.Equal(t, 2, done.Pages)
	assert.Equal(t, address, f.crawler.request().Start, "the crawler is given what was asked for")

	ev := f.audit.last()
	raw, err := json.Marshal(ev.Parameters)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "s3cret", "the audit row holds no credentials")
	assert.NotContains(t, string(raw), "t0ken", "and no query")
}

func TestPortal_CopyBounds(t *testing.T) {
	cases := []struct {
		name      string
		body      map[string]any
		wantPages int
		wantBytes int64
	}{
		{"unset is the deployment's", map[string]any{}, siteconfig.DefaultCrawlPages, siteconfig.DefaultCrawlBytes},
		{"lower is honored", map[string]any{"max_pages": 5, "max_bytes": 1000}, 5, 1000},
		{"higher is capped", map[string]any{"max_pages": siteconfig.DefaultCrawlPages + 1, "max_bytes": siteconfig.DefaultCrawlBytes + 1}, siteconfig.DefaultCrawlPages, siteconfig.DefaultCrawlBytes},
		{"negative is the deployment's", map[string]any{"max_pages": -1, "max_bytes": -1}, siteconfig.DefaultCrawlPages, siteconfig.DefaultCrawlBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.body["url"] = "https://old.example.org/"
			f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/copy", tc.body}, userOwner)
			awaitJob(t, f)
			assert.Equal(t, tc.wantPages, f.crawler.request().MaxPages)
			assert.Equal(t, tc.wantBytes, f.crawler.request().MaxBytes)
		})
	}
}

func TestPortal_CopyFailuresAndAbsence(t *testing.T) {
	t.Run("no crawler", func(t *testing.T) {
		f := newFixture(t)
		f.rt.Crawler = nil
		rec := f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPost, siteBase + "/copy", map[string]any{"url": "https://a.example/"}}, userOwner)
		assert.Contains(t, rec.Body.String(), "cannot copy")
	})
	t.Run("a crawl that fails says so on the job, in general terms", func(t *testing.T) {
		f := newFixture(t)
		f.crawler.err = errBoom
		f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/copy", map[string]any{"url": "https://a.example/"}}, userOwner)
		done := awaitJob(t, f)
		assert.Equal(t, site.JobFailed, done.Status)
		assert.NotContains(t, done.Error, secretText)
	})
	t.Run("a refusal is said on the job", func(t *testing.T) {
		f := newFixture(t)
		f.crawler.err = &sitesvc.RefusedError{Msg: "url: an http(s) address"}
		f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/copy", map[string]any{"url": "ftp://x"}}, userOwner)
		assert.Equal(t, "url: an http(s) address", awaitJob(t, f).Error)
	})
}

func TestPortal_JobsListAndStop(t *testing.T) {
	f := newFixture(t)
	f.store.jobs["sjob_live"] = &site.Job{ID: "sjob_live", SiteID: siteID, Environment: site.EnvDev, Status: site.JobRunning, UpdatedAt: time.Now()}
	f.store.jobs["sjob_other"] = &site.Job{ID: "sjob_other", SiteID: "site_2", Status: site.JobRunning, UpdatedAt: time.Now()}

	list := decodeBody[JobList](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/jobs", nil}, userViewer))
	require.Len(t, list.Jobs, 1)
	assert.Equal(t, "sjob_live", list.Jobs[0].ID)

	one := decodeBody[site.Job](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/jobs/sjob_live", nil}, userViewer))
	assert.Equal(t, "sjob_live", one.ID)

	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, siteBase + "/jobs/sjob_other", nil}, userViewer)
	f.statusIs(t, http.StatusNotFound, route{http.MethodPost, siteBase + "/jobs/sjob_other/stop", nil}, userOwner)
	assert.Equal(t, site.JobRunning, f.store.jobs["sjob_other"].Status, "a route naming one site never stops another's job")

	f.statusIs(t, http.StatusNoContent, route{http.MethodPost, siteBase + "/jobs/sjob_live/stop", nil}, userManager)
	assert.Equal(t, site.JobStopped, f.store.jobs["sjob_live"].Status)
}

func TestPortal_PromoteRollbackAndEnvironment(t *testing.T) {
	f := newFixture(t)
	f.release(1, site.EnvDev)

	got := decodeBody[sitesvc.Promoted](t, f.statusIs(t, 200, route{http.MethodPost, siteBase + "/promote", map[string]any{"from": site.EnvDev, "to": "stage"}}, userManager))
	assert.Equal(t, sitesvc.StatusPromoted, got.Status)
	assert.Equal(t, "promote", f.audit.last().Parameters["command"])

	waiting := decodeBody[sitesvc.Promoted](t, f.statusIs(t, 200, route{http.MethodPost, siteBase + "/promote", map[string]any{"from": site.EnvDev, "to": envProd}}, userManager))
	assert.Equal(t, sitesvc.StatusAwaitingApproval, waiting.Status)
	require.NotNil(t, waiting.Approval)

	f.release(2, site.EnvDev)
	rolled := decodeBody[sitesvc.Promoted](t, f.statusIs(t, 200, route{http.MethodPost, siteBase + "/rollback", map[string]any{"environment": site.EnvDev}}, userOwner))
	assert.Equal(t, 1, rolled.Release)

	f.store.failPoint = site.ErrStale
	f.statusIs(t, http.StatusConflict, route{http.MethodPost, siteBase + "/promote", map[string]any{"release": 2, "to": "stage"}}, userManager)
	f.statusIs(t, http.StatusConflict, route{http.MethodPost, siteBase + "/rollback", map[string]any{"environment": site.EnvDev, "release": 2}}, userOwner)

	env := decodeBody[EnvironmentResponse](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/environments/qa", map[string]any{"requires_approval": true}}, userOwner))
	require.NotNil(t, env.Site.Environment("qa"))
	assert.True(t, env.Site.Environment("qa").RequiresApproval)
	assert.False(t, env.Site.Environment("qa").Public, "a new environment is private")
	assert.Equal(t, sitesvc.StatusDone, env.Status)
	assert.Equal(t, "https://marketing--qa.pages.example.net", env.Site.Addresses["qa"])
}

// Making an environment public is the owner's; where it requires approval it
// waits in Approvals and goes through once approved (#2604).
func TestPortal_MakeAnEnvironmentPublic(t *testing.T) {
	f := newFixture(t)
	done := decodeBody[EnvironmentResponse](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/environments/stage", map[string]any{"public": true}}, userOwner))
	assert.Equal(t, sitesvc.StatusDone, done.Status)
	assert.True(t, f.store.sites[siteID].Environment("stage").Public)

	waiting := decodeBody[EnvironmentResponse](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/environments/" + envProd, map[string]any{"public": true, "reason": "launch"}}, userOwner))
	assert.Equal(t, sitesvc.StatusAwaitingApproval, waiting.Status)
	require.NotNil(t, waiting.Approval)
	assert.Equal(t, approval.PurposeSitePublic, waiting.Approval.Purpose)
	assert.False(t, f.store.sites[siteID].Environment(envProd).Public, "prod stays private while it waits")

	fa, ok := f.rt.Service.Approvals.(*fakeApprovals)
	require.True(t, ok)
	fa.approved = true
	finished := decodeBody[EnvironmentResponse](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/environments/" + envProd, map[string]any{"public": true}}, userOwner))
	assert.Equal(t, sitesvc.StatusDone, finished.Status, "the same request goes through once approved")
	assert.True(t, f.store.sites[siteID].Environment(envProd).Public)

	private := decodeBody[EnvironmentResponse](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/environments/stage", map[string]any{"public": false}}, userManager))
	assert.False(t, private.Site.Environment("stage").Public, "anyone who manages the site makes an environment private")
}

func TestPortal_MainEnvironment(t *testing.T) {
	f := newFixture(t)
	got := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/main-environment", map[string]any{"environment": envProd}}, userOwner))
	assert.Equal(t, envProd, got.MainEnvironment)
	assert.Equal(t, "https://marketing.pages.example.net", got.Address)
	assert.Equal(t, "https://marketing.pages.example.net", got.Addresses[envProd], "prod answers at the main address")
	assert.Equal(t, "https://marketing--dev.pages.example.net", got.Addresses[site.EnvDev], "dev falls back to <site>--dev")

	none := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/main-environment", map[string]any{"environment": ""}}, userAdmin))
	assert.Empty(t, none.MainEnvironment)
	assert.Empty(t, none.Address, "a main address given to none is no address")
}

// A persona is shared view of named environments; the access list offers every
// persona to share with, and a persona grant is removed by name.
func TestPortal_PersonaGrants(t *testing.T) {
	f := newFixture(t)
	list := decodeBody[AccessList](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/access", nil}, userManager))
	assert.Equal(t, []string{"finance-team", "web-team"}, list.Personas)

	got := decodeBody[site.Grant](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/access", map[string]any{"persona": "finance-team", "environments": []string{envProd, "default"}}}, userManager))
	assert.Equal(t, "finance-team", got.Persona)
	assert.Equal(t, site.LevelView, got.Level, "a persona is given view")
	assert.Equal(t, []string{envProd, site.EnvDev}, got.Environments, "the old name of dev is read as dev")

	f.statusIs(t, 200, route{http.MethodPut, siteBase + "/access", map[string]any{"persona": "finance-team", "level": "view", "notify": true}}, userOwner)
	assert.Empty(t, f.sharer.sent, "a persona has no address to email")

	f.statusIs(t, http.StatusNoContent, route{http.MethodDelete, siteBase + "/access?persona=finance-team", nil}, userManager)
	assert.NotContains(t, f.store.grants[siteID], "persona:finance-team")
	assert.Equal(t, "finance-team", f.audit.last().Parameters["persona"])

	mux := http.NewServeMux()
	(&portalAPI{d: PortalDeps{Runtime: f.rt, IsAdmin: func([]string) bool { return false }, Persona: func([]string) *portal.PersonaInfo { return &portal.PersonaInfo{Name: "analyst"} }}}).register(mux)
	req := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), userOwner), http.MethodGet, siteBase+"/access", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, []string{}, decodeBody[AccessList](t, rec).Personas, "no persona list is [], never null")
}

// Someone who does not manage the site sees the environments they can open
// and no other, and only those environments' domains.
func TestPortal_AViewerSeesOnlyWhatOpensForThem(t *testing.T) {
	f := newFixture(t)
	f.store.grants[siteID][userViewer.Email] = site.Grant{Email: userViewer.Email, Level: site.LevelView, Environments: []string{"stage"}}
	f.store.sites[siteID].Environment(envProd).Public = true
	f.store.domains["www.client.example"] = &site.Domain{Hostname: "www.client.example", SiteID: siteID, Environment: envProd}
	f.store.domains["dev.client.example"] = &site.Domain{Hostname: "dev.client.example", SiteID: siteID, Environment: site.EnvDev}

	got := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userViewer))
	names := make([]string, 0, len(got.Environments))
	for _, e := range got.Environments {
		names = append(names, e.Name)
	}
	assert.Equal(t, []string{"stage", envProd}, names, "stage is shared with them and prod is public; dev is not theirs")
	require.Len(t, got.Domains, 1)
	assert.Equal(t, "www.client.example", got.Domains[0].Hostname)
	_, hasDev := got.Addresses[site.EnvDev]
	assert.False(t, hasDev, "no address for an environment they cannot see")

	stranger := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userStranger))
	require.Len(t, stranger.Environments, 1)
	assert.Equal(t, envProd, stranger.Environments[0].Name, "a stranger sees only what is public")

	managed := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userManager))
	assert.Len(t, managed.Environments, 3, "someone who manages the site sees every environment")

	f.store.failGrant = errBoom
	f.statusIs(t, http.StatusInternalServerError, route{http.MethodGet, siteBase, nil}, userViewer)
}

func TestPortal_Domains(t *testing.T) {
	f := newFixture(t)
	rec := f.statusIs(t, http.StatusCreated, route{http.MethodPost, siteBase + "/domains", map[string]any{
		"hostname": "WWW.Client.Example.", "environment": "qa", "requires_approval": true,
	}}, userManager)
	d := decodeBody[site.Domain](t, rec)
	assert.Equal(t, "www.client.example", d.Hostname, "as a request's Host names it")
	assert.Equal(t, "qa", d.Environment)
	assert.True(t, f.store.sites[siteID].Environment("qa").RequiresApproval, "the request's environment was created")
	assert.Equal(t, "add_domain", f.audit.last().Parameters["command"])

	f.statusIs(t, http.StatusConflict, route{http.MethodPost, siteBase + "/domains", map[string]any{"hostname": "www.client.example", "environment": "qa"}}, userManager)

	f.store.sites["site_2"] = &site.Site{ID: "site_2", Name: "other", OwnerEmail: userStranger.Email}
	f.store.domains["other.client.example"] = &site.Domain{Hostname: "other.client.example", SiteID: "site_2", Environment: "x"}
	cases := []struct {
		name, target string
		user         *portal.User
		status       int
	}{
		{"a domain of another site, named on this one", siteBase + "/domains/other.client.example", userOwner, 404},
		{"a domain that does not exist", siteBase + "/domains/none.client.example", userOwner, 404},
		{"an unknown site", base + "/nope/domains/www.client.example", userOwner, 404},
		{"someone who does not manage it", siteBase + "/domains/www.client.example", userStranger, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.store.domains)
			f.statusIs(t, tc.status, route{http.MethodDelete, tc.target, nil}, tc.user)
			assert.Len(t, f.store.domains, before)
		})
	}

	f.statusIs(t, http.StatusNoContent, route{http.MethodDelete, siteBase + "/domains/www.client.example", nil}, userAdmin)
	assert.NotContains(t, f.store.domains, "www.client.example")
	assert.Equal(t, "remove_domain", f.audit.last().Parameters["command"])
}

func TestPortal_AccessGrantRevoke(t *testing.T) {
	f := newFixture(t)
	list := decodeBody[AccessList](t, f.statusIs(t, 200, route{http.MethodGet, siteBase + "/access", nil}, userManager))
	assert.Equal(t, userOwner.Email, list.Owner)
	require.Len(t, list.Grants, 2)

	got := decodeBody[site.Grant](t, f.statusIs(t, 200, route{http.MethodPut, siteBase + "/access", map[string]any{"email": "Pat@Client.Example", "level": "view"}}, userManager))
	assert.Equal(t, "pat@client.example", got.Email)
	assert.Equal(t, userManager.Email, got.GrantedBy)
	assert.False(t, got.CreatedAt.IsZero(), "the response carries the time the row was written")
	assert.Empty(t, f.sharer.sent, "no email unless asked")
	assert.Equal(t, "grant", f.audit.last().Parameters["command"])

	f.statusIs(t, http.StatusNoContent, route{http.MethodDelete, siteBase + "/access?email=pat@client.example", nil}, userManager)
	assert.NotContains(t, f.store.grants[siteID], "pat@client.example")
	assert.Equal(t, "revoke", f.audit.last().Parameters["command"])

	f.external["ext@client.example"] = true
	rec := f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPut, siteBase + "/access", map[string]any{"email": "ext@client.example", "level": "manage"}}, userOwner)
	assert.Contains(t, rec.Body.String(), "internal")
	f.statusIs(t, http.StatusOK, route{http.MethodPut, siteBase + "/access", map[string]any{"email": "ext@client.example", "level": "view"}}, userOwner)
}

func TestPortal_GrantNotifiesWhenAsked(t *testing.T) {
	t.Run("the person is sent the site's address", func(t *testing.T) {
		f := newFixture(t)
		f.statusIs(t, 200, route{http.MethodPut, siteBase + "/access", map[string]any{"email": "pat@client.example", "level": "view", "notify": true}}, userOwner)
		require.Equal(t, []string{"pat@client.example/share"}, f.sharer.sent)
		assert.Equal(t, "https://marketing.pages.example.net", f.sharer.body[0].Link, "the first private environment the grant opens, dev on the main address")
		assert.Equal(t, siteName, f.sharer.body[0].ItemTitle)
		assert.Equal(t, userOwner.Email, f.sharer.body[0].Actor)
	})
	t.Run("a failed email leaves the grant standing", func(t *testing.T) {
		f := newFixture(t)
		f.sharer.err = errBoom
		f.statusIs(t, 200, route{http.MethodPut, siteBase + "/access", map[string]any{"email": "pat@client.example", "level": "view", "notify": true}}, userOwner)
		assert.Contains(t, f.store.grants[siteID], "pat@client.example")
	})
	t.Run("a canceled request's email is not logged as a failure", func(t *testing.T) {
		f := newFixture(t)
		f.sharer.err = context.Canceled
		f.statusIs(t, 200, route{http.MethodPut, siteBase + "/access", map[string]any{"email": "pat@client.example", "level": "view", "notify": true}}, userOwner)
	})
	t.Run("no sharer sends nothing", func(t *testing.T) {
		f := newFixture(t)
		mux := http.NewServeMux()
		(&portalAPI{d: PortalDeps{Runtime: f.rt, IsAdmin: func([]string) bool { return false }}}).register(mux)
		req := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), userOwner), http.MethodPut, siteBase+"/access", strings.NewReader(`{"email":"x@example.com","level":"view","notify":true}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "and no audit logger records nothing")
	})
	t.Run("the site gone before the email sends nothing", func(t *testing.T) {
		f := newFixture(t)
		f.store.mu.Lock()
		delete(f.store.sites, siteID)
		f.store.sites[siteID] = &site.Site{ID: siteID, Name: "marketing", OwnerEmail: userOwner.Email}
		f.store.mu.Unlock()
		// share reads the site itself; once it is gone it sends nothing.
		a := &portalAPI{d: PortalDeps{Runtime: f.rt, Sharer: f.sharer}}
		a.share(context.Background(), &caller{user: userOwner}, "nope", &site.Grant{Email: "x@example.com"})
		assert.Empty(t, f.sharer.sent)
	})
}

func TestApplicationAddresses_NoSuchApplication(t *testing.T) {
	f := newFixture(t)
	f.rt.Apps = noApps(t)
	assert.Equal(t, http.StatusNotFound, f.do(t, http.MethodGet, "/api/v1/portal/application-addresses/nope", userOwner, nil).Code)
}

// noApps is an application store with no applications in it.
func noApps(t *testing.T) *appstore.Store {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT .+ FROM applications WHERE name").WillReturnError(sql.ErrNoRows)
	return appstore.New(db)
}

func TestApplicationAddresses_AnswersWhereItIs(t *testing.T) {
	f := newFixture(t)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	f.rt.Apps, f.rt.Store = appstore.New(db), sitestore.New(db)
	now := time.Now()
	mock.ExpectQuery("SELECT .+ FROM applications WHERE name").WillReturnRows(sqlmock.NewRows([]string{
		"id", "name", "description", "owner_email", "connection", "schema_name", "current_release",
		"created_by", "created_at", "updated_at", "schema_adopted_version", "schema_added_by", "schema_added_at",
	}).AddRow("app_1", "shop", "", "jane@example.com", "", "", 0, "jane@example.com", now, now, 0, "", nil))
	mock.ExpectQuery("SELECT kind, part_id").WillReturnRows(sqlmock.NewRows([]string{"kind", "part_id", "added_by", "added_at"}))
	mock.ExpectQuery("ORDER BY d.hostname").WillReturnRows(sqlmock.NewRows([]string{
		"hostname", "site_id", "name", "environment", "application_id", "application", "created_by", "created_at",
	}).AddRow("shop.client.example", "", "", "", "app_1", "shop", "a", now).
		AddRow("www.client.example", "site_1", "marketing", "prod", "", "", "a", now))

	got := decodeBody[ApplicationAddresses](t, f.statusIs(t, 200, route{http.MethodGet, "/api/v1/portal/application-addresses/shop", nil}, userOwner))
	assert.Equal(t, "https://shop.pages.example.net", got.PageOrigin)
	assert.Equal(t, []string{"shop.client.example"}, got.Domains, "only the domains mapped to it")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLowered(t *testing.T) {
	assert.Equal(t, 5, lowered(5, 10))
	assert.Equal(t, 10, lowered(10, 10))
	assert.Equal(t, 10, lowered(11, 10))
	assert.Equal(t, 10, lowered(0, 10))
	assert.Equal(t, int64(10), lowered(int64(-1), int64(10)))
}

func TestWriteJSON_EmptyListsAreArrays(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, AccessList{Grants: []site.Grant{}})
	assert.Contains(t, rec.Body.String(), `"grants":[]`)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// A manager adding a domain never makes an environment public (#2604).
func TestPortal_AManagersDomainNeverMakesAnythingPublic(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, siteBase+"/domains", userManager, map[string]any{"hostname": "a.example.org", "environment": "qa", "public": true})
	if rec.Code == http.StatusCreated {
		require.NotNil(t, f.store.sites[siteID].Environment("qa"))
		assert.False(t, f.store.sites[siteID].Environment("qa").Public, "the environment it made is private")
		return
	}
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Nil(t, f.store.sites[siteID].Environment("qa"))
}

func TestShareLink_TheFirstPrivateEnvironmentTheGrantOpens(t *testing.T) {
	f := newFixture(t)
	svc := f.rt.Service
	st := &site.Site{ID: siteID, Name: siteName, MainEnvironment: site.EnvDev, Environments: []site.Environment{
		{Name: site.EnvDev}, {Name: "stage"}, {Name: envProd, Public: true},
	}}
	ctx := context.Background()
	assert.Equal(t, "https://marketing--stage.pages.example.net", shareLink(ctx, svc, st, &site.Grant{Environments: []string{"stage"}}))
	assert.Equal(t, "https://marketing.pages.example.net", shareLink(ctx, svc, st, &site.Grant{}), "every private environment: dev, on the main address")
	assert.Equal(t, "https://marketing.pages.example.net", shareLink(ctx, svc, st, &site.Grant{Environments: []string{envProd}}),
		"a grant naming only a public environment falls back to the main address")
}

func TestPortal_EnvironmentAnswersFailWhenTheDomainsCannotBeRead(t *testing.T) {
	f := newFixture(t)
	f.store.failSiteDomains = errBoom
	f.statusIs(t, http.StatusInternalServerError, route{http.MethodPut, siteBase + "/main-environment", map[string]any{"environment": envProd}}, userOwner)
	f.statusIs(t, http.StatusInternalServerError, route{http.MethodPut, siteBase + "/environments/stage", map[string]any{"internal_view": true}}, userOwner)
}

// The owner sets an environment's release limit through the portal as a
// number, and returns it to the deployment's default (#2602).
func TestPortal_TheOwnerSetsTheReleaseLimit(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPut, siteBase+"/environments/prod", userOwner, map[string]any{"keep_releases": 3})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	e := f.store.sites[siteID].Environment("prod")
	require.NotNil(t, e.KeepReleases)
	assert.Equal(t, 3, *e.KeepReleases)
	assert.Contains(t, rec.Body.String(), `"keep_releases":3`)

	rec = f.do(t, http.MethodPut, siteBase+"/environments/prod", userOwner, map[string]any{"keep_releases": "default"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, f.store.sites[siteID].Environment("prod").KeepReleases)
}

// A release's view says what keeps it, and is [] rather than null where
// nothing does.
func TestPortal_ReleasesSayWhatKeepsThem(t *testing.T) {
	f := newFixture(t)
	f.store.releases[siteID] = []site.Release{{ID: "srel_1", SiteID: siteID, Number: 1, Pruned: true}}
	rec := f.do(t, http.MethodGet, siteBase+"/releases", userOwner, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `"kept_by":null`)
	assert.Contains(t, rec.Body.String(), `"kept_by":[]`, "the routes' test runs without the store, so nothing reads as kept")
	assert.Contains(t, rec.Body.String(), `"pruned":true`)
	rec = f.do(t, http.MethodGet, siteBase+"/releases/1", userOwner, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"kept_by":[]`)
}
