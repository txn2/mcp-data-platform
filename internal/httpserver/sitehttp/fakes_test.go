package sitehttp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// memStore is a site.Store in memory, under a real sitesvc.Service. Jobs run on
// their own goroutine, so every method locks and a test reads the maps only
// once the job has finished.
type memStore struct {
	site.Store
	mu              sync.Mutex
	sites           map[string]*site.Site
	releases        map[string][]site.Release
	files           map[string][]site.File
	deployments     map[string][]site.Deployment
	domains         map[string]*site.Domain
	grants          map[string]map[string]site.Grant
	jobs            map[string]*site.Job
	finished        chan site.Job
	failGet         error
	failList        error
	failDomains     error
	failPoint       error
	failGrant       error
	failFiles       error
	failReleases    error
	failDeployments error
	failCreate      error
	failSiteDomains error
}

func newMem() *memStore {
	return &memStore{
		sites: map[string]*site.Site{}, releases: map[string][]site.Release{}, files: map[string][]site.File{},
		deployments: map[string][]site.Deployment{}, domains: map[string]*site.Domain{},
		grants: map[string]map[string]site.Grant{}, jobs: map[string]*site.Job{}, finished: make(chan site.Job, 8),
	}
}

func cloneSite(st *site.Site) *site.Site {
	c := *st
	c.Environments = slices.Clone(st.Environments)
	return &c
}

func (m *memStore) Create(_ context.Context, st *site.Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreate != nil {
		return m.failCreate
	}
	for _, x := range m.sites {
		if x.Name == st.Name {
			return site.ErrNameTaken
		}
	}
	m.sites[st.ID] = cloneSite(st)
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGet != nil {
		return nil, m.failGet
	}
	st, ok := m.sites[id]
	if !ok {
		return nil, site.ErrNotFound
	}
	return cloneSite(st), nil
}

func (m *memStore) GetByName(_ context.Context, name string) (*site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGet != nil {
		return nil, m.failGet
	}
	for _, st := range m.sites {
		if st.Name == name {
			return cloneSite(st), nil
		}
	}
	return nil, site.ErrNotFound
}

func (m *memStore) List(context.Context) ([]site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failList != nil {
		return nil, m.failList
	}
	out := make([]site.Site, 0, len(m.sites))
	for _, st := range m.sites {
		out = append(out, *cloneSite(st))
	}
	slices.SortFunc(out, func(a, b site.Site) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (m *memStore) Update(_ context.Context, st *site.Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.sites[st.ID]
	x.Description, x.OwnerEmail, x.InternalView = st.Description, st.OwnerEmail, st.InternalView
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sites, id)
	return nil
}

func (m *memStore) SetEnvironment(_ context.Context, siteID, name string, requiresApproval bool, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.sites[siteID]
	if e := st.Environment(name); e != nil {
		e.RequiresApproval, e.UpdatedBy = requiresApproval, actor
		return nil
	}
	st.Environments = append(st.Environments, site.Environment{Name: name, RequiresApproval: requiresApproval, UpdatedBy: actor})
	return nil
}

func (m *memStore) RecordRelease(_ context.Context, r *site.Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Number = len(m.releases[r.SiteID]) + 1
	m.releases[r.SiteID] = append([]site.Release{*r}, m.releases[r.SiteID]...)
	m.files[r.ID] = r.Files
	return nil
}

func (m *memStore) Release(_ context.Context, siteID string, number int) (*site.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.releases[siteID] {
		if r.Number == number {
			c := r
			return &c, nil
		}
	}
	return nil, site.ErrNoRelease
}

func (m *memStore) Releases(_ context.Context, siteID string) ([]site.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failReleases != nil {
		return nil, m.failReleases
	}
	return m.releases[siteID], nil
}

func (m *memStore) Files(_ context.Context, releaseID string) ([]site.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failFiles != nil {
		return nil, m.failFiles
	}
	return m.files[releaseID], nil
}

func (m *memStore) Point(_ context.Context, p site.Pointer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPoint != nil {
		return m.failPoint
	}
	st := m.sites[p.SiteID]
	env := st.Environment(p.Environment)
	if env.Release != p.After {
		return site.ErrStale
	}
	m.deployments[p.SiteID] = append([]site.Deployment{{Environment: p.Environment, Action: p.Action, From: env.Release, To: p.To, Actor: p.Actor}}, m.deployments[p.SiteID]...)
	env.Release = p.To
	return nil
}

func (m *memStore) Deployments(_ context.Context, siteID, environment string, _ int) ([]site.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDeployments != nil {
		return nil, m.failDeployments
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
	if _, ok := m.domains[d.Hostname]; ok {
		return site.ErrDomainTaken
	}
	c := *d
	m.domains[d.Hostname] = &c
	return nil
}

func (m *memStore) Domain(_ context.Context, hostname string) (*site.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[hostname]
	if !ok {
		return nil, site.ErrNoDomain
	}
	c := *d
	return &c, nil
}

func (m *memStore) Domains(context.Context) ([]site.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDomains != nil {
		return nil, m.failDomains
	}
	out := []site.Domain{}
	for _, d := range m.domains {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b site.Domain) int { return strings.Compare(a.Hostname, b.Hostname) })
	return out, nil
}

func (m *memStore) SiteDomains(_ context.Context, siteID string) ([]site.Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSiteDomains != nil {
		return nil, m.failSiteDomains
	}
	out := []site.Domain{}
	for _, d := range m.domains {
		if d.SiteID == siteID {
			out = append(out, *d)
		}
	}
	slices.SortFunc(out, func(a, b site.Domain) int { return strings.Compare(a.Hostname, b.Hostname) })
	return out, nil
}

func (m *memStore) RemoveDomain(_ context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.domains, hostname)
	return nil
}

func (m *memStore) Grants(_ context.Context, siteID string) ([]site.Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGrant != nil {
		return nil, m.failGrant
	}
	out := []site.Grant{}
	for _, g := range m.grants[siteID] {
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b site.Grant) int { return strings.Compare(a.Email, b.Email) })
	return out, nil
}

func (m *memStore) GrantLevel(_ context.Context, siteID, email string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failGrant != nil {
		return "", m.failGrant
	}
	return m.grants[siteID][email].Level, nil
}

func (m *memStore) SetGrant(_ context.Context, siteID string, g site.Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.grants[siteID] == nil {
		m.grants[siteID] = map[string]site.Grant{}
	}
	m.grants[siteID][g.Email] = g
	return nil
}

func (m *memStore) RemoveGrant(_ context.Context, siteID, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.grants[siteID], email)
	return nil
}

func (m *memStore) CreateJob(_ context.Context, j *site.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.Status, j.UpdatedAt = site.JobRunning, time.Now()
	c := *j
	m.jobs[j.ID] = &c
	return nil
}

func (m *memStore) Job(_ context.Context, id string) (*site.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, site.ErrNoJob
	}
	c := *j
	return &c, nil
}

func (m *memStore) ActiveJobs(_ context.Context, siteID string) ([]site.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []site.Job{}
	for _, j := range m.jobs {
		if j.SiteID == siteID && j.Status == site.JobRunning {
			out = append(out, *j)
		}
	}
	return out, nil
}

func (m *memStore) ReportJob(_ context.Context, id string, p site.JobProgress) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	j.Phase, j.Done, j.Total = p.Phase, p.Done, p.Total
	return j.Status == site.JobStopped, nil
}

func (m *memStore) FinishJob(_ context.Context, id string, r site.JobResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	j.Status, j.Release, j.Pages, j.Redirects, j.Skipped, j.Error = r.Status, r.Release, r.Pages, r.Redirects, r.Skipped, r.Error
	m.finished <- *j
	return nil
}

func (m *memStore) StopJob(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[id].Status = site.JobStopped
	return nil
}

type memObjects struct {
	mu  sync.Mutex
	put map[string][]byte
}

func (o *memObjects) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.put[key] = data
	return nil
}

func (o *memObjects) DeleteObject(_ context.Context, _, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.put, key)
	return nil
}

type fakeApps map[string]*application.Application

func (f fakeApps) GetByName(_ context.Context, name string) (*application.Application, error) {
	if a, ok := f[name]; ok {
		return a, nil
	}
	return nil, application.ErrNotFound
}

type fakeApprovals struct{ approved bool }

func (*fakeApprovals) SubmitRelease(_ context.Context, _ approval.Actor, in approval.SubmitRelease) (*approval.Request, error) {
	return &approval.Request{ID: "apr_1", Status: approval.StatusPending, Target: in.Target}, nil
}

func (f *fakeApprovals) ReleaseApproved(context.Context, string, string, string) bool {
	return f.approved
}

func (*fakeApprovals) RecordReleaseSelfApproval(context.Context, approval.Actor, approval.SubmitRelease) error {
	return nil
}

// memSources reads what a publish names; who records the caller the read ran as.
type memSources struct {
	mu      sync.Mutex
	archive string
	scope   []string
	files   []sitesvc.InputFile
	err     error
	who     []string
}

func (s *memSources) note(ctx context.Context) {
	if u := callerOf(ctx); u != "" {
		s.who = append(s.who, u)
	}
}

func (s *memSources) Archive(ctx context.Context, ref string) ([]sitesvc.InputFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note(ctx)
	s.archive = ref
	return s.files, s.err
}

func (s *memSources) Folder(ctx context.Context, scope, scopeID, path string) ([]sitesvc.InputFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note(ctx)
	s.scope = []string{scope, scopeID, path}
	return s.files, s.err
}

func (s *memSources) whoSaw() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.who)
}

type memCrawler struct {
	mu     sync.Mutex
	result *sitecrawl.Result
	err    error
	asked  sitecrawl.Request
}

func (c *memCrawler) Crawl(_ context.Context, req sitecrawl.Request) (*sitecrawl.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = req
	return c.result, c.err
}

func (c *memCrawler) request() sitecrawl.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked
}

// memAudit records every event the routes write.
type memAudit struct {
	audit.Logger
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (a *memAudit) Log(_ context.Context, ev audit.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, ev)
	return a.err
}

func (a *memAudit) last() audit.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.events[len(a.events)-1]
}

func (a *memAudit) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events)
}

// memSharer records every email a shared site sends.
type memSharer struct {
	mu   sync.Mutex
	sent []string
	body []notification.Payload
	err  error
}

func (s *memSharer) Notify(_ context.Context, recipient, category string, p notification.Payload) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, recipient+"/"+category)
	s.body = append(s.body, p)
	return true, s.err
}

var errBoom = errors.New("pq: connection to 10.1.2.3 failed: password authentication failed")
