package sitetool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	adminEmail = "admin@example.com"
	testBucket = "site-bucket"
	siteName   = "marketing"
	indexName  = "index.html"
	testHost   = "www.client.example"
)

// memStore is a site.Store over maps.
type memStore struct {
	mu          sync.Mutex
	sites       map[string]*site.Site
	releases    map[string][]site.Release
	deployments map[string][]site.Deployment
	domains     map[string]*site.Domain
	err         error
}

func newMemStore() *memStore {
	return &memStore{
		sites: map[string]*site.Site{}, releases: map[string][]site.Release{},
		deployments: map[string][]site.Deployment{}, domains: map[string]*site.Domain{},
	}
}

func copySite(s *site.Site) *site.Site {
	out := *s
	out.Environments = slices.Clone(s.Environments)
	return &out
}

func (m *memStore) Create(_ context.Context, s *site.Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	for _, existing := range m.sites {
		if existing.Name == s.Name {
			return site.ErrNameTaken
		}
	}
	m.sites[s.ID] = copySite(s)
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sites[id]; ok {
		return copySite(s), nil
	}
	return nil, site.ErrNotFound
}

func (m *memStore) GetByName(_ context.Context, name string) (*site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	for _, s := range m.sites {
		if s.Name == name {
			return copySite(s), nil
		}
	}
	return nil, site.ErrNotFound
}

func (m *memStore) List(context.Context) ([]site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := make([]site.Site, 0, len(m.sites))
	for _, s := range m.sites {
		out = append(out, *copySite(s))
	}
	return out, nil
}

func (m *memStore) Update(_ context.Context, s *site.Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sites[s.ID] = copySite(s)
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sites, id)
	return nil
}

func (m *memStore) SetEnvironment(_ context.Context, siteID, name string, requiresApproval bool, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sites[siteID]
	if env := s.Environment(name); env != nil {
		env.RequiresApproval = requiresApproval
		return nil
	}
	s.Environments = append(s.Environments, site.Environment{Name: name, RequiresApproval: requiresApproval})
	return nil
}

func (m *memStore) RecordRelease(_ context.Context, r *site.Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Number = len(m.releases[r.SiteID]) + 1
	m.releases[r.SiteID] = slices.Insert(m.releases[r.SiteID], 0, *r)
	return nil
}

func (m *memStore) Release(_ context.Context, siteID string, number int) (*site.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.releases[siteID] {
		if r.Number == number {
			return &r, nil
		}
	}
	return nil, site.ErrNoRelease
}

func (*memStore) ReleaseByID(context.Context, string) (*site.Release, error) {
	return nil, site.ErrNoRelease
}

func (m *memStore) Releases(_ context.Context, siteID string) ([]site.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return m.releases[siteID], nil
}

func (*memStore) Files(context.Context, string) ([]site.File, error) { return nil, nil }

func (m *memStore) Point(_ context.Context, p site.Pointer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	env := m.sites[p.SiteID].Environment(p.Environment)
	if env.Release != p.After {
		return site.ErrStale
	}
	m.deployments[p.SiteID] = slices.Insert(m.deployments[p.SiteID], 0, site.Deployment{
		Environment: p.Environment, Action: p.Action, From: env.Release, To: p.To, Actor: p.Actor,
	})
	env.Release = p.To
	return nil
}

func (m *memStore) Deployments(_ context.Context, siteID, environment string, _ int) ([]site.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := []site.Deployment{}
	for _, d := range m.deployments[siteID] {
		if environment == "" || d.Environment == environment {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *memStore) AddDomain(_ context.Context, d *site.Domain) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.domains[d.Hostname]; taken {
		return site.ErrDomainTaken
	}
	out := *d
	m.domains[d.Hostname] = &out
	return nil
}

func (m *memStore) Domain(_ context.Context, hostname string) (*site.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.domains[hostname]; ok {
		out := *d
		return &out, nil
	}
	return nil, site.ErrNoDomain
}

func (m *memStore) Domains(context.Context) ([]site.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := []site.Domain{}
	for _, d := range m.domains {
		out = append(out, *d)
	}
	return out, nil
}

func (m *memStore) MarkVerified(_ context.Context, hostname, by string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.domains[hostname].VerifiedAt, m.domains[hostname].VerifiedBy = &now, by
	return nil
}

func (m *memStore) RemoveDomain(_ context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.domains, hostname)
	return nil
}

func (*memStore) RouteHost(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (*memStore) RoutePreview(context.Context, string, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

type memObjects struct {
	mu   sync.Mutex
	puts map[string][]byte
}

func (o *memObjects) DeleteObject(_ context.Context, _, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.puts, key)
	return nil
}

func (o *memObjects) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.puts[key] = data
	return nil
}

type allowVerifier struct{ err error }

func (v allowVerifier) Verify(context.Context, string, string) error { return v.err }

type memApprovals struct {
	selfApproved, submitted int
	approved                bool
}

func (a *memApprovals) SubmitRelease(context.Context, approval.Actor, approval.SubmitRelease) (*approval.Request, error) {
	a.submitted++
	return &approval.Request{ID: "apr_1"}, nil
}

func (a *memApprovals) ReleaseApproved(context.Context, string, string, string) bool {
	return a.approved
}

func (a *memApprovals) RecordReleaseSelfApproval(context.Context, approval.Actor, approval.SubmitRelease) error {
	a.selfApproved++
	return nil
}

type memApps struct{}

func (memApps) GetByName(_ context.Context, name string) (*application.Application, error) {
	if name == "shop" {
		return &application.Application{ID: "app_shop", Name: "shop"}, nil
	}
	return nil, application.ErrNotFound
}

type memSources struct {
	files       []sitesvc.InputFile
	err         error
	archive     string
	scope       []string
	folderCalls int
}

func (s *memSources) Archive(_ context.Context, ref string) ([]sitesvc.InputFile, error) {
	s.archive = ref
	return s.files, s.err
}

func (s *memSources) Folder(_ context.Context, scope, scopeID, path string) ([]sitesvc.InputFile, error) {
	s.folderCalls++
	s.scope = []string{scope, scopeID, path}
	return s.files, s.err
}

type memCrawler struct {
	result *sitecrawl.Result
	err    error
	asked  sitecrawl.Request
}

func (c *memCrawler) Crawl(_ context.Context, req sitecrawl.Request) (*sitecrawl.Result, error) {
	c.asked = req
	return c.result, c.err
}

type fixture struct {
	tool      *Tool
	store     *memStore
	objects   *memObjects
	approvals *memApprovals
	sources   *memSources
	crawler   *memCrawler
}

func newFixture() *fixture {
	f := &fixture{
		store: newMemStore(), objects: &memObjects{puts: map[string][]byte{}}, approvals: &memApprovals{},
		sources: &memSources{}, crawler: &memCrawler{},
	}
	svc := &sitesvc.Service{
		Store: f.store, Objects: f.objects, Bucket: testBucket, Apps: memApps{}, Approvals: f.approvals, Verifier: allowVerifier{},
		Config: siteconfig.Config{PagesDomain: "pages.example.com"}, PublicScheme: "https",
	}
	f.tool = &Tool{Service: svc, Sources: f.sources, Crawler: f.crawler, PortalURL: "https://portal.example.com"}
	return f
}

func ctxAs(email string, admin bool, source string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: email, IsAdmin: admin, PersonaName: "analyst", Source: source,
	})
}

func admin() context.Context { return ctxAs(adminEmail, true, "") }

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func call(t *testing.T, f *fixture, ctx context.Context, in Input) map[string]any { //nolint:revive // the fixture leads
	t.Helper()
	res, _, err := f.tool.Handle(ctx, in)
	require.NoError(t, err)
	body := text(t, res)
	require.False(t, res.IsError, body)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(body), &out), body)
	return out
}

func refusal(t *testing.T, f *fixture, ctx context.Context, in Input) string { //nolint:revive // the fixture leads
	t.Helper()
	res, _, err := f.tool.Handle(ctx, in)
	require.NoError(t, err, "a refusal is a result, never a protocol error")
	require.True(t, res.IsError, "expected %s to be refused, got %s", in.Command, text(t, res))
	return text(t, res)
}

func files(names ...string) []File {
	out := make([]File, 0, len(names))
	for _, n := range names {
		out = append(out, File{Path: n, Content: "content of " + n})
	}
	return out
}

func createSite(t *testing.T, f *fixture) {
	t.Helper()
	call(t, f, admin(), Input{Command: CmdCreate, Name: siteName, Description: "the site"})
}

func publishTo(t *testing.T, f *fixture, env string) {
	t.Helper()
	call(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Environment: env, Files: files(indexName)})
}

func stored(f *fixture) *site.Site {
	for _, s := range f.store.sites {
		return s
	}
	return nil
}

// Every command in Commands is reached through Handle, in the order a site's
// life runs them.
func TestHandle_EveryCommandDispatches(t *testing.T) {
	f := newFixture()
	covered := map[string]bool{}
	do := func(in Input) map[string]any {
		t.Helper()
		covered[in.Command] = true
		return call(t, f, admin(), in)
	}

	created := do(Input{Command: CmdCreate, Name: siteName, Description: "first"})
	assert.Equal(t, siteName, created["name"])
	assert.Equal(t, adminEmail, created["owner_email"])

	got := do(Input{Command: CmdGet, Site: siteName})
	previews, ok := got["preview_urls"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://dev--marketing.pages.example.com", previews["dev"])

	byID := call(t, f, admin(), Input{Command: CmdGet, Site: stored(f).ID})
	assert.NotEmpty(t, byID["preview_urls"], "a site is read by id too")

	assert.EqualValues(t, 1, do(Input{Command: CmdList})["total"])

	updated := do(Input{Command: CmdUpdate, Site: siteName, Description: "second", OwnerEmail: "jane@example.com"})
	assert.Equal(t, "second", updated["description"])
	assert.Equal(t, "jane@example.com", updated["owner_email"])

	requires := true
	do(Input{Command: CmdEnvironment, Site: siteName, Environment: "qa", RequiresApproval: &requires})
	assert.True(t, stored(f).Environment("qa").RequiresApproval)

	published := do(Input{Command: CmdPublish, Site: siteName, Files: files(indexName), Note: "first"})
	assert.Equal(t, site.EnvDev, published["environment"])
	assert.Equal(t, "https://dev--marketing.pages.example.com", published["preview_url"])
	assert.Equal(t, 1, stored(f).Environment(site.EnvDev).Release)

	releases, ok := do(Input{Command: CmdReleases, Site: siteName})["releases"].([]any)
	require.True(t, ok)
	assert.Len(t, releases, 1)

	deployments, ok := do(Input{Command: CmdDeployments, Site: siteName, Environment: site.EnvDev})["deployments"].([]any)
	require.True(t, ok)
	assert.Len(t, deployments, 1)

	promoted := do(Input{Command: CmdPromote, Site: siteName, From: site.EnvDev, To: "stage"})
	assert.Equal(t, sitesvc.StatusPromoted, promoted["status"])
	assert.Equal(t, 1, stored(f).Environment("stage").Release)

	publishTo(t, f, site.EnvDev)
	require.Equal(t, 2, stored(f).Environment(site.EnvDev).Release)
	rolled := do(Input{Command: CmdRollback, Site: siteName, Environment: site.EnvDev})
	assert.EqualValues(t, 1, rolled["release"])
	assert.Equal(t, 1, stored(f).Environment(site.EnvDev).Release)

	added := do(Input{Command: CmdAddDomain, Hostname: testHost, Site: siteName, Environment: "prod"})
	assert.Equal(t, testHost, added["hostname"])
	assert.NotEmpty(t, added["token"])

	verified := do(Input{Command: CmdVerifyDomain, Hostname: testHost})
	assert.NotEmpty(t, verified["verified_at"])

	domains, ok := do(Input{Command: CmdDomains})["domains"].([]any)
	require.True(t, ok)
	assert.Len(t, domains, 1)

	hosts := do(Input{Command: CmdHosts})
	assert.Equal(t, []any{testHost}, hosts["hostnames"])
	assert.Equal(t, "*.pages.example.com", hosts["pages_wildcard"])

	assert.Equal(t, testHost, do(Input{Command: CmdRemoveDomain, Hostname: testHost})["removed"])
	assert.Empty(t, f.store.domains)

	f.crawler.result = &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: indexName, Body: []byte("copied")}}, Pages: 1, Redirects: 2}
	crawled := do(Input{Command: CmdCrawl, Site: siteName, URL: "https://old.example/", FormMap: map[string]string{"/contact": "form"}})
	assert.EqualValues(t, 1, crawled["pages"])
	assert.EqualValues(t, 2, crawled["redirects"])
	assert.Equal(t, []any{}, crawled["skipped"], "an empty list is [], never null")
	assert.Equal(t, sitecrawl.Request{Start: "https://old.example/", FormMap: map[string]string{"/contact": "form"}, PortalURL: "https://portal.example.com"}, f.crawler.asked)

	assert.Equal(t, siteName, do(Input{Command: CmdDelete, Site: siteName})["deleted"])
	assert.Empty(t, f.store.sites)

	for _, cmd := range Commands {
		assert.True(t, covered[cmd], "command %s was not exercised", cmd)
	}
}

func TestHandle_AnUnknownCommandIsRefused(t *testing.T) {
	assert.Contains(t, refusal(t, newFixture(), admin(), Input{Command: "nope"}), `unknown command "nope"`)
}

func TestHandle_SitesTurnedOffRefusesEveryCommand(t *testing.T) {
	off := false
	for _, cmd := range Commands {
		t.Run(cmd, func(t *testing.T) {
			f := newFixture()
			f.tool.Service.Config.Enabled = &off
			assert.Contains(t, refusal(t, f, admin(), Input{Command: cmd}), "sites.enabled is false")
			assert.Empty(t, f.store.sites)
		})
	}
}

func TestHandle_WritesAreRefusedInsideAScriptRunExceptPublish(t *testing.T) {
	run := ctxAs(adminEmail, true, middleware.SourceScript)
	for _, cmd := range Commands {
		if slices.Contains(Reads, cmd) || cmd == CmdPublish {
			continue
		}
		t.Run(cmd, func(t *testing.T) {
			f := newFixture()
			msg := refusal(t, f, run, Input{Command: cmd, Name: siteName, Site: siteName})
			assert.Contains(t, msg, "cannot be called from inside a script run")
			assert.Empty(t, f.store.sites, "nothing was changed")
		})
	}
}

func TestHandle_PublishAndReadsAreAllowedInsideAScriptRun(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	run := ctxAs(adminEmail, true, middleware.SourceScript)

	published := call(t, f, run, Input{Command: CmdPublish, Site: siteName, Files: files(indexName)})
	assert.Equal(t, site.EnvDev, published["environment"])
	for _, cmd := range Reads {
		t.Run(cmd, func(t *testing.T) {
			call(t, f, run, Input{Command: cmd, Site: siteName})
		})
	}
}

func TestHandle_NoCallerHasNoOwner(t *testing.T) {
	msg := refusal(t, newFixture(), context.Background(), Input{Command: CmdCreate, Name: siteName})
	assert.Contains(t, msg, "a site needs an owner")
}

func TestPublish_NamesExactlyOneSource(t *testing.T) {
	tests := []struct {
		name string
		in   Input
	}{
		{name: "none", in: Input{}},
		{name: "files and an archive", in: Input{Files: files(indexName), Archive: "mcp:resource:1"}},
		{name: "files and a folder", in: Input{Files: files(indexName), Folder: "web"}},
		{name: "an archive and a folder", in: Input{Archive: "mcp:resource:1", Folder: "web"}},
		{name: "all three", in: Input{Files: files(indexName), Archive: "mcp:resource:1", Folder: "web"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			createSite(t, f)
			tt.in.Command, tt.in.Site = CmdPublish, siteName
			assert.Contains(t, refusal(t, f, admin(), tt.in), "names one source")
			assert.Empty(t, f.objects.puts)
			assert.Zero(t, f.sources.folderCalls)
			assert.Empty(t, f.sources.archive)
		})
	}
}

func TestPublish_FilesWithBase64Encoding(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	binary := []byte{0x00, 0xff, 0x10, 0x80}
	call(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Files: []File{
		{Path: "logo.bin", Content: base64.StdEncoding.EncodeToString(binary), Encoding: "base64"},
		{Path: indexName, Content: "<h1>plain</h1>", Encoding: "utf-8"},
	}})

	id := stored(f).ID
	assert.Equal(t, binary, f.objects.puts[sitesvc.ObjectKey(id, f.store.releases[id][0].ID, "", "logo.bin")])
	assert.Equal(t, []byte("<h1>plain</h1>"), f.objects.puts[sitesvc.ObjectKey(id, f.store.releases[id][0].ID, "", indexName)])
	assert.Equal(t, "files (2)", f.store.releases[id][0].Source)
}

func TestPublish_MalformedBase64IsRefused(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	msg := refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Files: []File{{Path: "a.bin", Content: "not base64!", Encoding: "base64"}}})
	assert.Contains(t, msg, `file "a.bin": content is not base64`)
	assert.Empty(t, f.objects.puts, "nothing was stored")
}

func TestPublish_FromAnArchive(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	f.sources.files = []sitesvc.InputFile{{Path: indexName, Body: []byte("from archive")}}
	call(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Archive: "mcp:resource:site.zip"})

	assert.Equal(t, "mcp:resource:site.zip", f.sources.archive)
	id := stored(f).ID
	assert.Equal(t, []byte("from archive"), f.objects.puts[sitesvc.ObjectKey(id, f.store.releases[id][0].ID, "", indexName)])
	assert.Equal(t, "mcp:resource:site.zip", f.store.releases[id][0].Source)
}

func TestPublish_FromAFolder(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	f.sources.files = []sitesvc.InputFile{{Path: indexName, Body: []byte("from folder")}}
	call(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Folder: "web/build", Scope: "persona", ScopeID: "analyst"})

	assert.Equal(t, []string{"persona", "analyst", "web/build"}, f.sources.scope)
	assert.Equal(t, "web/build", f.store.releases[stored(f).ID][0].Source)
}

func TestPublish_ASourceThatFailsIsSaid(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	f.sources.err = &sitesvc.RefusedError{Msg: "the archive cannot be published: too large"}
	assert.Equal(t, "the archive cannot be published: too large", refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Archive: "x"}))
}

func TestPublish_NoManagedResourcesRefusesAReference(t *testing.T) {
	for _, in := range []Input{{Archive: "x"}, {Folder: "web"}} {
		f := newFixture()
		f.tool.Sources = nil
		createSite(t, f)
		in.Command, in.Site = CmdPublish, siteName
		assert.Contains(t, refusal(t, f, admin(), in), "no managed resources")
	}
}

func TestPublish_AnEnvironmentThatRequiresApprovalIsNotPublishedTo(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	msg := refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Environment: site.EnvProd, Files: files(indexName)})
	assert.Contains(t, msg, "requires approval")
}

func TestPublish_ADotfileIsRefused(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Files: files(".env")}), "dotted segment")
}

func TestPromote_ToAnEnvironmentThatRequiresApproval(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	publishTo(t, f, site.EnvDev)

	promoted := call(t, f, admin(), Input{Command: CmdPromote, Site: siteName, From: site.EnvDev, To: site.EnvProd})
	assert.Equal(t, sitesvc.StatusPromoted, promoted["status"])
	assert.Equal(t, 1, f.approvals.selfApproved)
	assert.Equal(t, 1, stored(f).Environment(site.EnvProd).Release)
}

func TestPromote_AnOwnersPromotionToProdWaits(t *testing.T) {
	f := newFixture()
	owner := ctxAs("owner@example.com", false, "")
	call(t, f, owner, Input{Command: CmdCreate, Name: siteName})
	call(t, f, owner, Input{Command: CmdPublish, Site: siteName, Files: files(indexName)})

	promoted := call(t, f, owner, Input{Command: CmdPromote, Site: siteName, From: site.EnvDev, To: site.EnvProd, Reason: "ship"})
	assert.Equal(t, sitesvc.StatusAwaitingApproval, promoted["status"])
	assert.Equal(t, 1, f.approvals.submitted)
	assert.Zero(t, stored(f).Environment(site.EnvProd).Release, "nothing moves until it is approved")
}

func TestHandle_AdministratorCommandsAreRefusedToAnOwner(t *testing.T) {
	f := newFixture()
	owner := ctxAs("owner@example.com", false, "")
	call(t, f, owner, Input{Command: CmdCreate, Name: siteName})

	for _, in := range []Input{
		{Command: CmdDelete, Site: siteName},
		{Command: CmdAddDomain, Hostname: testHost, Site: siteName, Environment: "prod"},
		{Command: CmdVerifyDomain, Hostname: testHost},
		{Command: CmdRemoveDomain, Hostname: testHost},
	} {
		t.Run(in.Command, func(t *testing.T) {
			assert.Contains(t, refusal(t, f, owner, in), "not allowed")
		})
	}
	assert.Len(t, f.store.sites, 1)
}

func TestCrawl(t *testing.T) {
	t.Run("a deployment with no crawler", func(t *testing.T) {
		f := newFixture()
		f.tool.Crawler = nil
		createSite(t, f)
		assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdCrawl, Site: siteName, URL: "https://a.example"}), "cannot crawl")
	})
	t.Run("a crawl that fails is said", func(t *testing.T) {
		f := newFixture()
		createSite(t, f)
		f.crawler.err = errors.New("the site could not be reached")
		assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdCrawl, Site: siteName, URL: "https://a.example"}), "could not be reached")
	})
	t.Run("a crawl of no files publishes nothing", func(t *testing.T) {
		f := newFixture()
		createSite(t, f)
		f.crawler.result = &sitecrawl.Result{}
		assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdCrawl, Site: siteName, URL: "https://a.example"}), "no files")
	})
	t.Run("what the crawl skipped is reported", func(t *testing.T) {
		f := newFixture()
		createSite(t, f)
		f.crawler.result = &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: indexName, Body: []byte("x")}}, Skipped: []string{"https://a.example/x"}}
		out := call(t, f, admin(), Input{Command: CmdCrawl, Site: siteName, URL: "https://a.example"})
		assert.Equal(t, []any{"https://a.example/x"}, out["skipped"])
	})
}

func TestHandle_ErrorsAreResultsTheCallerCanRead(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		set  func(*memStore)
		want string
	}{
		{name: "an unknown site by name", in: Input{Command: CmdGet, Site: "nope"}, want: "no such site: "},
		{name: "an unknown site by id", in: Input{Command: CmdGet, Site: "site_nope"}, want: "no such site: "},
		{name: "an unknown site for releases", in: Input{Command: CmdReleases, Site: "nope"}, want: "no such site"},
		{name: "an unknown site for deployments", in: Input{Command: CmdDeployments, Site: "nope"}, want: "no such site"},
		{name: "a failing list", in: Input{Command: CmdList}, set: func(m *memStore) { m.err = errors.New("db down") }, want: "db down"},
		{name: "failing domains", in: Input{Command: CmdDomains}, set: func(m *memStore) { m.err = errors.New("db down") }, want: "db down"},
		{name: "failing hosts", in: Input{Command: CmdHosts}, set: func(m *memStore) { m.err = errors.New("db down") }, want: "db down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			if tt.set != nil {
				tt.set(f.store)
			}
			assert.Contains(t, refusal(t, f, admin(), tt.in), tt.want)
		})
	}
}

func TestReleasesAndDeployments_AStoreFailureIsSaid(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cmd   string
		check string
	}{
		{name: "releases", cmd: CmdReleases, check: "listing releases"},
		{name: "deployments", cmd: CmdDeployments, check: "listing deployments"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.tool.Service.Store = &failingLists{memStore: f.store}
			createSite(t, f)
			assert.Contains(t, refusal(t, f, admin(), Input{Command: tt.cmd, Site: siteName}), tt.check)
		})
	}
}

// failingLists is memStore with its release and deployment listings refused.
type failingLists struct{ *memStore }

func (*failingLists) Releases(context.Context, string) ([]site.Release, error) {
	return nil, errors.New("db down")
}

func (*failingLists) Deployments(context.Context, string, string, int) ([]site.Deployment, error) {
	return nil, errors.New("db down")
}

func TestMessage(t *testing.T) {
	assert.Equal(t, "plain", message(&sitesvc.RefusedError{Msg: "plain"}))
	assert.Equal(t, "other", message(errors.New("other")))
	assert.True(t, strings.HasPrefix(message(site.ErrNotFound), "no such site: "))
}

func TestActorOf(t *testing.T) {
	assert.Equal(t, sitesvc.Actor{}, actorOf(context.Background()))
	assert.Equal(t,
		sitesvc.Actor{Email: "a@example.com", Admin: true, Persona: "analyst"},
		actorOf(ctxAs("a@example.com", true, "")))
}

func TestInsideRun(t *testing.T) {
	assert.False(t, insideRun(context.Background()))
	assert.False(t, insideRun(ctxAs("a@example.com", false, "mcp")))
	assert.True(t, insideRun(ctxAs("a@example.com", false, middleware.SourceScript)))
}

func TestRegister_DoesNothingWithoutAServiceOrServer(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	var none *Tool
	none.Register(server)
	(&Tool{}).Register(server)
	newFixture().tool.Register(nil)
	assert.Empty(t, toolsOf(t, server))
}

func TestRegister_TheToolIsCallableThroughARealClient(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	f := newFixture()
	f.tool.Register(server)
	require.Equal(t, []string{ToolName}, toolsOf(t, server))

	session := connect(t, server)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: ToolName, Arguments: map[string]any{"command": "list"}})
	require.NoError(t, err)
	require.False(t, res.IsError, text(t, res))
	assert.Contains(t, text(t, res), `"total":0`)
}

func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolsOf(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	listed, err := connect(t, server).ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestSchema_ListsEveryCommand(t *testing.T) {
	props, ok := schema()["properties"].(map[string]any)
	require.True(t, ok)
	command, ok := props["command"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, Commands, command["enum"])
}

func TestReads_NameOnlyCommandsThatExist(t *testing.T) {
	for _, cmd := range append(slices.Clone(Reads), allowedInRun...) {
		assert.Contains(t, Commands, cmd)
	}
}
