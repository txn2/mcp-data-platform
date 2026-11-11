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
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	adminEmail = "admin@example.com"
	testBucket = "site-bucket"
	siteName   = "marketing"
	indexName  = "index.html"
	testHost   = "www.client.example"
)

// memStore is a site.Store over maps. The embedded interface is nil: the job
// methods manage_site never calls panic if something starts to.
type memStore struct {
	site.Store
	mu          sync.Mutex
	sites       map[string]*site.Site
	releases    map[string][]site.Release
	deployments map[string][]site.Deployment
	domains     map[string]*site.Domain
	grants      map[string]map[string]site.Grant
	err         error
	failGrants  error
	failDomains error
}

func newMemStore() *memStore {
	return &memStore{
		sites: map[string]*site.Site{}, releases: map[string][]site.Release{},
		deployments: map[string][]site.Deployment{}, domains: map[string]*site.Domain{},
		grants: map[string]map[string]site.Grant{},
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

func (m *memStore) SetEnvironment(_ context.Context, siteID, name string, set site.EnvironmentSettings, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.sites[siteID]
	e := st.Environment(name)
	if e == nil {
		st.Environments = append(st.Environments, site.Environment{Name: name})
		e = &st.Environments[len(st.Environments)-1]
	}
	if set.RequiresApproval != nil {
		e.RequiresApproval = *set.RequiresApproval
	}
	if set.Public != nil {
		if e.Public != *set.Public {
			e.AccessChangedAt = e.AccessChangedAt.Add(time.Second)
		}
		e.Public = *set.Public
	}
	if set.InternalView != nil {
		e.InternalView = *set.InternalView
	}
	switch {
	case set.KeepDefault:
		e.KeepReleases, e.KeepDefault = nil, true
	case set.Keep != nil:
		n := *set.Keep
		e.KeepReleases, e.Keep = &n, n
	}
	e.UpdatedBy = actor
	return nil
}

func (m *memStore) SetMainEnvironment(_ context.Context, siteID, env string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.sites[siteID]
	if env != "" && st.Environment(env) == nil {
		return site.ErrNoEnvironment
	}
	st.MainEnvironment = env
	return nil
}

func (*memStore) RouteEnv(context.Context, string, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (m *memStore) RecordRelease(_ context.Context, r *site.Release, move *site.Pointer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Number = len(m.releases[r.SiteID]) + 1
	if move != nil {
		move.To = r.Number
		if err := m.pointLocked(*move); err != nil {
			return err
		}
	}
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

// Files walks a release's chain as the store's query does.
func (m *memStore) Files(ctx context.Context, releaseID string) ([]site.File, error) {
	return m.Tree(ctx, releaseID, site.TreeOutput)
}

func (m *memStore) Tree(_ context.Context, releaseID, tree string) ([]site.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := map[string]site.Release{}
	for _, list := range m.releases {
		for _, r := range list {
			byID[r.ID] = r
		}
	}
	seen, out := map[string]bool{}, []site.File{}
	for r, ok := byID[releaseID]; ok; r, ok = byID[r.ManifestParent] {
		files, gone := r.Files, r.Removed
		if tree == site.TreeSource {
			files, gone = r.SourceFiles, r.SourceRemoved
		}
		if tree == site.TreeRender {
			files, gone = nil, nil
			if r.RenderIndex != nil {
				files = []site.File{*r.RenderIndex}
			}
		}
		for _, f := range files {
			if !seen[f.Path] {
				seen[f.Path] = true
				out = append(out, f)
			}
		}
		for _, p := range gone {
			seen[p] = true
		}
	}
	slices.SortFunc(out, func(a, b site.File) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func (*memStore) StoredFiles(context.Context, string) ([]site.File, error) { return nil, nil }

func (m *memStore) Point(_ context.Context, p site.Pointer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pointLocked(p)
}

func (m *memStore) pointLocked(p site.Pointer) error {
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

func (m *memStore) RemoveDomain(_ context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.domains, hostname)
	return nil
}

func (*memStore) RouteHost(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (*memStore) RouteOwn(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (m *memStore) SiteDomains(_ context.Context, siteID string) ([]site.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDomains != nil {
		return nil, m.failDomains
	}
	out := []site.Domain{}
	for _, d := range m.domains {
		if d.SiteID == siteID {
			out = append(out, *d)
		}
	}
	return out, nil
}

func (m *memStore) Grants(_ context.Context, siteID string) ([]site.Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGrants != nil {
		return nil, m.failGrants
	}
	out := []site.Grant{}
	for _, g := range m.grants[siteID] {
		out = append(out, g)
	}
	return out, nil
}

func (m *memStore) GrantLevel(_ context.Context, siteID, email string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.grants[siteID][email].Level, nil
}

func (m *memStore) SetGrant(_ context.Context, siteID string, g *site.Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.grants[siteID] == nil {
		m.grants[siteID] = map[string]site.Grant{}
	}
	g.CreatedAt = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	m.grants[siteID][grantKey(g.Email, g.Persona)] = *g
	return nil
}

func (m *memStore) RemoveGrant(_ context.Context, siteID, email, persona string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.grants[siteID], grantKey(email, persona))
	return nil
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

// objectFor is where the newest release keeps a file's bytes.
func objectFor(t *testing.T, f *fixture, siteID, name string) string {
	t.Helper()
	for _, file := range f.store.releases[siteID][0].Files {
		if file.Path == name {
			return sitesvc.ObjectKey(siteID, file, "")
		}
	}
	t.Fatalf("the release has no %s", name)
	return ""
}

func (o *memObjects) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.puts[key], "", nil
}

func (o *memObjects) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.puts[key] = data
	return nil
}

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
		Store: f.store, Objects: f.objects, Bucket: testBucket, Apps: memApps{}, Approvals: f.approvals,
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
		out = append(out, File{Path: n, Content: new("content of " + n)})
	}
	return out
}

func createSite(t *testing.T, f *fixture) {
	t.Helper()
	call(t, f, admin(), Input{Command: CmdCreate, Name: siteName, Description: "the site"})
}

// requireApproval adds a prod environment that requires approval.
func requireApproval(t *testing.T, f *fixture, ctx context.Context) { //nolint:revive // the fixture leads
	t.Helper()
	yes := true
	call(t, f, ctx, Input{Command: CmdEnvironment, Site: siteName, Environment: "prod", RequiresApproval: &yes})
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
	addresses, ok := got["addresses"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://marketing.pages.example.com", addresses[site.EnvDev])
	assert.Equal(t, []any{}, got["grants"], "someone who manages the site sees who has access, [] when no one")

	byID := call(t, f, admin(), Input{Command: CmdGet, Site: stored(f).ID})
	assert.NotEmpty(t, byID["addresses"], "a site is read by id too")

	assert.EqualValues(t, 1, do(Input{Command: CmdList})["total"])

	updated := do(Input{Command: CmdUpdate, Site: siteName, Description: "second", OwnerEmail: "jane@example.com"})
	assert.Equal(t, "second", updated["description"])
	assert.Equal(t, "jane@example.com", updated["owner_email"])

	requires := true
	do(Input{Command: CmdEnvironment, Site: siteName, Environment: "qa", RequiresApproval: &requires})
	assert.True(t, stored(f).Environment("qa").RequiresApproval)
	do(Input{Command: CmdEnvironment, Site: siteName, Environment: "stage"})

	published := do(Input{Command: CmdPublish, Site: siteName, Files: files(indexName), Note: "first"})
	assert.Equal(t, site.EnvDev, published["environment"])
	assert.Equal(t, "https://marketing.pages.example.com", published["address"])
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

	added := do(Input{Command: CmdAddDomain, Hostname: testHost, Site: siteName, Environment: "prod", RequiresApproval: &requires})
	assert.Equal(t, testHost, added["hostname"])
	require.NotNil(t, stored(f).Environment("prod"), "adding a domain creates its environment")
	assert.True(t, stored(f).Environment("prod").RequiresApproval)
	assert.Contains(t, do(Input{Command: CmdGet, Site: siteName})["addresses"], "prod")

	domains, ok := do(Input{Command: CmdDomains})["domains"].([]any)
	require.True(t, ok)
	assert.Len(t, domains, 1)

	assert.Equal(t, testHost, do(Input{Command: CmdRemoveDomain, Hostname: testHost})["removed"])
	assert.Empty(t, f.store.domains)

	f.crawler.result = &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: indexName, Body: []byte("copied")}}, Pages: 1, Redirects: 2}
	crawled := do(Input{Command: CmdCrawl, Site: siteName, URL: "https://old.example/", FormMap: map[string]string{"/contact": "form"}})
	assert.EqualValues(t, 1, crawled["pages"])
	assert.EqualValues(t, 2, crawled["redirects"])
	assert.Equal(t, []any{}, crawled["skipped"], "an empty list is [], never null")
	assert.Equal(t, sitecrawl.Request{Start: "https://old.example/", FormMap: map[string]string{"/contact": "form"}, PortalURL: "https://portal.example.com"}, f.crawler.asked)

	granted := do(Input{Command: CmdGrant, Site: siteName, Email: "Pat@Client.example", Level: site.LevelView})
	assert.Equal(t, "pat@client.example", granted["email"])
	assert.Equal(t, "pat@client.example", do(Input{Command: CmdRevoke, Site: siteName, Email: "pat@client.example"})["revoked"])
	assert.Empty(t, f.store.grants[stored(f).ID])

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
		{Path: "logo.bin", Content: new(base64.StdEncoding.EncodeToString(binary)), Encoding: "base64"},
		{Path: indexName, Content: new("<h1>plain</h1>"), Encoding: "utf-8"},
	}})

	id := stored(f).ID
	assert.Equal(t, binary, f.objects.puts[objectFor(t, f, id, "logo.bin")])
	assert.Equal(t, []byte("<h1>plain</h1>"), f.objects.puts[objectFor(t, f, id, indexName)])
	assert.Equal(t, "files (2)", f.store.releases[id][0].Source)
}

func TestPublish_MalformedBase64IsRefused(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	msg := refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Files: []File{{Path: "a.bin", Content: new("not base64!"), Encoding: "base64"}}})
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
	assert.Equal(t, []byte("from archive"), f.objects.puts[objectFor(t, f, id, indexName)])
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

// Agents work in dev, people use what was promoted (#2604): a publish aimed at
// any environment but dev is refused, naming dev and promote, even for an
// administrator.
func TestPublish_AnEnvironmentPeopleUseIsReadOnly(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	requireApproval(t, f, admin())
	msg := refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Environment: "prod", Files: files(indexName)})
	assert.Contains(t, msg, `environment "prod" is read-only`)
	assert.Contains(t, msg, "change dev")
	assert.Contains(t, msg, "by promote")
}

// The old name of dev still works for one release, and the answer says so.
func TestPublish_TheOldNameIsReadAsDevWithANote(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	out := call(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Environment: "default", Files: files(indexName)})
	assert.Equal(t, site.EnvDev, out["environment"])
	assert.Equal(t, site.AliasNote, out["note"])
}

func TestPublish_ADotfileIsRefused(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Files: files(".env")}), "dotted segment")
}

func TestPromote_ToAnEnvironmentThatRequiresApproval(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	requireApproval(t, f, admin())
	publishTo(t, f, site.EnvDev)

	promoted := call(t, f, admin(), Input{Command: CmdPromote, Site: siteName, From: site.EnvDev, To: "prod"})
	assert.Equal(t, sitesvc.StatusPromoted, promoted["status"])
	assert.Equal(t, 1, f.approvals.selfApproved)
	assert.Equal(t, 1, stored(f).Environment("prod").Release)
}

func TestPromote_AnOwnersPromotionToProdWaits(t *testing.T) {
	f := newFixture()
	owner := ctxAs("owner@example.com", false, "")
	call(t, f, owner, Input{Command: CmdCreate, Name: siteName})
	requireApproval(t, f, owner)
	call(t, f, owner, Input{Command: CmdPublish, Site: siteName, Files: files(indexName)})

	promoted := call(t, f, owner, Input{Command: CmdPromote, Site: siteName, From: site.EnvDev, To: "prod", Reason: "ship"})
	assert.Equal(t, sitesvc.StatusAwaitingApproval, promoted["status"])
	assert.Equal(t, 1, f.approvals.submitted)
	assert.Zero(t, stored(f).Environment("prod").Release, "nothing moves until it is approved")
}

func TestHandle_AdministratorCommandsAreRefusedToAnOwner(t *testing.T) {
	f := newFixture()
	owner := ctxAs("owner@example.com", false, "")
	call(t, f, owner, Input{Command: CmdCreate, Name: siteName})
	call(t, f, admin(), Input{Command: CmdAddDomain, Hostname: testHost, Application: "shop"})

	for _, in := range []Input{
		{Command: CmdDelete, Site: siteName},
		{Command: CmdAddDomain, Hostname: "other.client.example", Application: "shop"},
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
		f.crawler.result = &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: indexName, Body: []byte("x")}}, Skipped: []site.Skipped{{Address: "/x", Reason: "answered 404"}}}
		out := call(t, f, admin(), Input{Command: CmdCrawl, Site: siteName, URL: "https://a.example"})
		assert.Equal(t, []any{map[string]any{"address": "/x", "reason": "answered 404"}}, out["skipped"])
	})
	t.Run("the source recorded holds no query", func(t *testing.T) {
		f := newFixture()
		createSite(t, f)
		f.crawler.result = &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: indexName, Body: []byte("x")}}}
		call(t, f, admin(), Input{Command: CmdCrawl, Site: siteName, URL: "https://a.example/start?token=s3cret"})
		rel := f.store.releases[stored(f).ID][0]
		assert.Equal(t, "crawl https://a.example/start", rel.Source)
		assert.NotContains(t, rel.Source, "s3cret")
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

	cases := []struct {
		name   string
		claims map[string]any
		want   bool
	}{
		{"no claims", nil, false},
		{"the provider said verified", map[string]any{browsersession.ClaimEmailVerified: true}, false},
		{"the provider said not verified", map[string]any{browsersession.ClaimEmailVerified: false}, true},
		{"a claim of another type is not a statement", map[string]any{browsersession.ClaimEmailVerified: "false"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserEmail: "a@example.com", UserClaims: tc.claims})
			assert.Equal(t, tc.want, actorOf(ctx).EmailUnverified)
		})
	}
}

func TestGet_OnlyThoseWhoManageSeeWhoHasAccess(t *testing.T) {
	f := newFixture()
	owner := ctxAs("owner@example.com", false, "")
	call(t, f, owner, Input{Command: CmdCreate, Name: siteName})
	call(t, f, owner, Input{Command: CmdGrant, Site: siteName, Email: "mo@example.com", Level: site.LevelManage})
	call(t, f, owner, Input{Command: CmdGrant, Site: siteName, Email: "vi@client.example", Level: site.LevelView})

	assert.NotContains(t, call(t, f, ctxAs("vi@client.example", false, ""), Input{Command: CmdGet, Site: siteName}), "grants", "a viewer does not see who else has access")
	assert.NotContains(t, call(t, f, ctxAs("joe@example.com", false, ""), Input{Command: CmdGet, Site: siteName}), "grants")

	got := call(t, f, ctxAs("mo@example.com", false, ""), Input{Command: CmdGet, Site: siteName})
	assert.Len(t, got["grants"], 2)

	unverified := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: "owner@example.com", UserClaims: map[string]any{browsersession.ClaimEmailVerified: false},
	})
	assert.NotContains(t, call(t, f, unverified, Input{Command: CmdGet, Site: siteName}), "grants", "an unverified email matches no owner")

	f.store.failGrants = errors.New("db down")
	assert.Contains(t, refusal(t, f, owner, Input{Command: CmdGet, Site: siteName}), "listing grants")
}

func TestGet_AnAddedDomainIsAnEnvironmentsAddress(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	call(t, f, admin(), Input{Command: CmdAddDomain, Hostname: testHost, Site: siteName, Environment: "prod"})
	addresses, ok := call(t, f, admin(), Input{Command: CmdGet, Site: siteName})["addresses"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://"+testHost, addresses["prod"])
}

// TestGet_AnswersAsThePortalDoes pins #2607 and #2611: get reads the site's
// domains once and answers each environment's address by them, the composed
// addresses apart, says why a two-character site's environments have no pages
// address, and fails on a domain read it cannot make rather than answering
// addresses it did not check.
func TestGet_AnswersAsThePortalDoes(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	call(t, f, admin(), Input{Command: CmdAddDomain, Hostname: testHost, Site: siteName, Environment: "prod"})
	got := call(t, f, admin(), Input{Command: CmdGet, Site: siteName})
	pages, ok := got["pages_addresses"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://"+siteName+"--prod.pages.example.com", pages["prod"], "the composed address is reported apart")
	assert.NotContains(t, got, "address_note", "a site whose addresses answer is told nothing")

	two := stored(f)
	two.Name = "ab"
	got = call(t, f, admin(), Input{Command: CmdGet, Site: "ab"})
	assert.Contains(t, got["address_note"], "an address like ab--prod.pages.example.com")

	f.store.failDomains = errors.New("db down")
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdGet, Site: "ab"}), "listing domains")
}

func TestSetEnvironment_InternalViewCanBeTurnedOff(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	on, off := true, false
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: site.EnvDev, InternalView: &on})
	assert.True(t, stored(f).Environment(site.EnvDev).InternalView)
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: site.EnvDev, InternalView: &off})
	assert.False(t, stored(f).Environment(site.EnvDev).InternalView, "a false is told from unset")
	assert.Equal(t, "the site", stored(f).Description)
}

// set_environment makes an environment public only for the owner or an
// administrator, refuses a public dev, and main moves the main address.
func TestSetEnvironment_PublicAndMain(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	yes := true
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "prod"})
	assert.False(t, stored(f).Environment("prod").Public, "a new environment is private")
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: site.EnvDev, Public: &yes}), "always private")
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "prod", Public: &yes, Main: &yes})
	assert.True(t, stored(f).Environment("prod").Public)
	assert.Equal(t, "prod", stored(f).MainEnvironment)
	none := ""
	call(t, f, admin(), Input{Command: CmdUpdate, Site: siteName, MainEnvironment: &none})
	assert.Empty(t, stored(f).MainEnvironment, "update with main_environment \"\" gives the main address to none")
}

// A persona is shared view of named environments, and revoked by name.
func TestGrant_APersona(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: "prod"})
	got := call(t, f, admin(), Input{Command: CmdGrant, Site: siteName, Persona: "finance-team", Environments: []string{"prod"}})
	assert.Equal(t, "finance-team", got["persona"])
	assert.Equal(t, site.LevelView, got["level"], "a persona is given view")
	assert.Equal(t, []any{"prod"}, got["environments"])
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdGrant, Site: siteName, Persona: "finance-team", Level: site.LevelManage}), "manage is given to a person")
	assert.Equal(t, "finance-team", call(t, f, admin(), Input{Command: CmdRevoke, Site: siteName, Persona: "finance-team"})["revoked"])
}

func TestGrant_ThroughTheTool(t *testing.T) {
	f := newFixture()
	owner := ctxAs("owner@example.com", false, "")
	call(t, f, owner, Input{Command: CmdCreate, Name: siteName})

	assert.Contains(t, refusal(t, f, ctxAs("joe@example.com", false, ""), Input{Command: CmdGrant, Site: siteName, Email: "x@example.com", Level: site.LevelView}), "not allowed")
	assert.Contains(t, refusal(t, f, owner, Input{Command: CmdGrant, Site: siteName, Email: "x@example.com", Level: "owner"}), "level:")
	assert.Contains(t, refusal(t, f, owner, Input{Command: CmdRevoke, Site: siteName, Email: "x@example.com"}), site.ErrNoGrant.Error())
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

// A caller who could not publish makes the platform fetch nothing (#2559).
func TestCrawl_RefusedFetchesNothing(t *testing.T) {
	f := newFixture()
	f.crawler.result = &sitecrawl.Result{Files: []sitesvc.InputFile{{Path: indexName, Body: []byte("copied")}}}
	created, err := f.tool.Service.Create(context.Background(), sitesvc.Actor{Email: "owner@example.com"}, "guarded", "")
	require.NoError(t, err)
	_, err = f.tool.crawl(context.Background(), sitesvc.Actor{Email: "stranger@example.com"}, Input{Site: created.Name, URL: "https://old.example.org/"})
	require.ErrorIs(t, err, site.ErrNotAllowed)
	assert.Empty(t, f.crawler.asked.Start, "nothing was fetched")
}

// grantKey is where a grant is kept: by email, or by persona for a persona grant.
func grantKey(email, persona string) string {
	if email != "" {
		return email
	}
	return "persona:" + persona
}

func (*memApprovals) SubmitEnvironment(_ context.Context, _ approval.Actor, in approval.SubmitEnvironment) (*approval.Request, error) {
	return &approval.Request{ID: "apr_env", Status: approval.StatusPending, Purpose: approval.PurposeSitePublic, Target: in.Target}, nil
}

func (*memApprovals) EnvironmentApproved(context.Context, string, string, string) bool {
	return false
}

// SiteAccess reads the grants map the way the store's statement reads
// site_grants: a manage grant or a view grant naming the environment (or
// none) opens it by email, a view grant to the persona opens it by persona.
func (m *memStore) SiteAccess(_ context.Context, siteID, env, email, persona string) (*site.Access, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sites[siteID]
	if !ok {
		return nil, site.ErrNotFound
	}
	a := &site.Access{OwnerEmail: st.OwnerEmail}
	if e := st.Environment(env); e != nil {
		a.InternalView = e.InternalView
	}
	email = site.NormalizeEmail(email)
	for _, g := range m.grants[siteID] {
		switch {
		case g.Email != "" && g.Email == email && (g.Level == site.LevelManage || g.Opens(env)):
			a.ByEmail = true
		case g.Persona != "" && g.Persona == persona && g.Opens(env):
			a.ByPersona = true
		}
	}
	return a, nil
}

func TestWithNote(t *testing.T) {
	got, ok := withNote(map[string]any{"environment": "dev"}).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "dev", got["environment"])
	assert.Equal(t, site.AliasNote, got["note"], "an object answer gains the note beside its fields")

	wrapped, ok := withNote([]string{"a"}).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"a"}, wrapped["result"], "an answer that is not an object is kept whole")
	assert.Equal(t, site.AliasNote, wrapped["note"])

	unmarshalable := make(chan int)
	assert.Equal(t, unmarshalable, withNote(unmarshalable), "an answer that cannot be written is returned as it was")
}

// keep_releases arrives as an integer, a string of digits or "default", on
// set_environment and on add_domain creating an environment (#2602).
func TestSetEnvironment_KeepReleasesInEveryForm(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	for raw, want := range map[string]int{`7`: 7, `"9"`: 9, `0`: 0} {
		call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: site.EnvDev, KeepReleases: json.RawMessage(raw)})
		e := stored(f).Environment(site.EnvDev)
		require.NotNil(t, e.KeepReleases, raw)
		assert.Equal(t, want, *e.KeepReleases, raw)
	}
	call(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: site.EnvDev, KeepReleases: json.RawMessage(`"default"`)})
	assert.Nil(t, stored(f).Environment(site.EnvDev).KeepReleases)
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdEnvironment, Site: siteName, Environment: site.EnvDev, KeepReleases: json.RawMessage(`"ten"`)}), "keep_releases")
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdAddDomain, Site: siteName, Environment: "qa", Hostname: "qa.client.example", KeepReleases: json.RawMessage(`-2`)}), "keep_releases")

	call(t, f, admin(), Input{Command: CmdAddDomain, Site: siteName, Environment: "qa", Hostname: "qa.client.example", KeepReleases: json.RawMessage(`3`)})
	qa := stored(f).Environment("qa")
	require.NotNil(t, qa)
	require.NotNil(t, qa.KeepReleases)
	assert.Equal(t, 3, *qa.KeepReleases)
}
