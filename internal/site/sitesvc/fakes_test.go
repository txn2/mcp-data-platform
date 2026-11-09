package sitesvc

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
)

// memStore is a site.Store in memory. It holds the two rules a service reads
// back by identity: a release number is taken once, and Point compares the
// release the move was decided against. Jobs run on their own goroutine, so
// every method locks; a test reads the maps directly only when none is running.
type memStore struct {
	mu          sync.Mutex
	sites       map[string]*site.Site
	releases    map[string][]site.Release
	deployments map[string][]site.Deployment
	domains     map[string]*site.Domain
	grants      map[string]map[string]site.Grant
	jobs        map[string]*site.Job
	reports     []site.JobProgress
	finished    chan site.Job
	failPoint   error
	failRecord  error
	failListing error
	failDomains error
	failGrant   error
	failSet     error
	failRemove  error
	failFiles   error
	failFilesOf string // the release Files fails for, "" for any
	failUpdate  error
	failCreate  error
	failDelete  error
	failJob     error
	failReport  error
	failFinish  error
	failDomain  error
	failSetEnv  error
	failAdd     error
	failStop    error
}

func newMem() *memStore {
	return &memStore{
		sites: map[string]*site.Site{}, releases: map[string][]site.Release{},
		deployments: map[string][]site.Deployment{}, domains: map[string]*site.Domain{},
		grants: map[string]map[string]site.Grant{}, jobs: map[string]*site.Job{},
		finished: make(chan site.Job, 16),
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
	st, ok := m.sites[id]
	if !ok {
		return nil, site.ErrNotFound
	}
	return cloneSite(st), nil
}

func (m *memStore) GetByName(_ context.Context, name string) (*site.Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	out := make([]site.Site, 0, len(m.sites))
	for _, st := range m.sites {
		out = append(out, *cloneSite(st))
	}
	return out, nil
}

func (m *memStore) Update(_ context.Context, st *site.Site) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failUpdate != nil {
		return m.failUpdate
	}
	x, ok := m.sites[st.ID]
	if !ok {
		return site.ErrNotFound
	}
	x.Description, x.OwnerEmail, x.InternalView = st.Description, st.OwnerEmail, st.InternalView
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDelete != nil {
		return m.failDelete
	}
	if _, ok := m.sites[id]; !ok {
		return site.ErrNotFound
	}
	delete(m.sites, id)
	return nil
}

func (m *memStore) SetEnvironment(_ context.Context, siteID, name string, requiresApproval bool, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSetEnv != nil {
		return m.failSetEnv
	}
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
	if m.failRecord != nil {
		return m.failRecord
	}
	r.Number = len(m.releases[r.SiteID]) + 1
	m.releases[r.SiteID] = append([]site.Release{*r}, m.releases[r.SiteID]...)
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

func (*memStore) ReleaseByID(context.Context, string) (*site.Release, error) {
	return nil, site.ErrNoRelease
}

func (m *memStore) Releases(_ context.Context, siteID string) ([]site.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failListing != nil {
		return nil, m.failListing
	}
	return m.releases[siteID], nil
}

func (m *memStore) Files(_ context.Context, releaseID string) ([]site.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failFiles != nil && (m.failFilesOf == "" || m.failFilesOf == releaseID) {
		return nil, m.failFiles
	}
	for _, releases := range m.releases {
		for _, r := range releases {
			if r.ID == releaseID {
				return r.Files, nil
			}
		}
	}
	return []site.File{}, nil
}

func (m *memStore) Point(_ context.Context, p site.Pointer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPoint != nil {
		return m.failPoint
	}
	st, ok := m.sites[p.SiteID]
	if !ok {
		return site.ErrNotFound
	}
	env := st.Environment(p.Environment)
	if env == nil {
		return site.ErrNoEnvironment
	}
	if env.Release != p.After {
		return site.ErrStale
	}
	env.Release, env.UpdatedBy = p.To, p.Actor
	m.deployments[p.SiteID] = append([]site.Deployment{{
		Environment: p.Environment, Action: p.Action, From: p.After, To: p.To, Actor: p.Actor,
	}}, m.deployments[p.SiteID]...)
	return nil
}

func (m *memStore) Deployments(_ context.Context, siteID, environment string, _ int) ([]site.Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failListing != nil {
		return nil, m.failListing
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
	if m.failAdd != nil {
		return m.failAdd
	}
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
	if m.failDomain != nil {
		return nil, m.failDomain
	}
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

func (m *memStore) RemoveDomain(_ context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[hostname]; !ok {
		return site.ErrNoDomain
	}
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
	slices.SortFunc(out, func(a, b site.Domain) int { return strings.Compare(a.Hostname, b.Hostname) })
	return out, nil
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

func (m *memStore) SetGrant(_ context.Context, siteID string, g *site.Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSet != nil {
		return m.failSet
	}
	if m.grants[siteID] == nil {
		m.grants[siteID] = map[string]site.Grant{}
	}
	g.CreatedAt = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	m.grants[siteID][g.Email] = *g
	return nil
}

func (m *memStore) RemoveGrant(_ context.Context, siteID, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failRemove != nil {
		return m.failRemove
	}
	delete(m.grants[siteID], email)
	return nil
}

func (m *memStore) CreateJob(_ context.Context, j *site.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failJob != nil {
		return m.failJob
	}
	j.Status, j.UpdatedAt = site.JobRunning, time.Now()
	c := *j
	m.jobs[j.ID] = &c
	return nil
}

func (m *memStore) Job(_ context.Context, id string) (*site.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failJob != nil {
		return nil, m.failJob
	}
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
	if m.failJob != nil {
		return nil, m.failJob
	}
	out := []site.Job{}
	for _, j := range m.jobs {
		if j.SiteID == siteID && j.Status == site.JobRunning {
			out = append(out, *j)
		}
	}
	slices.SortFunc(out, func(a, b site.Job) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (m *memStore) ReportJob(_ context.Context, id string, p site.JobProgress) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failReport != nil {
		return false, m.failReport
	}
	m.reports = append(m.reports, p)
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
	return m.failFinish
}

func (m *memStore) StopJob(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failStop != nil {
		return m.failStop
	}
	m.jobs[id].Status = site.JobStopped
	return nil
}

// memObjects is the bucket. Uploads run concurrently, so it locks.
type memObjects struct {
	mu         sync.Mutex
	put        map[string][]byte
	fail       string // a key suffix whose put fails
	failDelete error
}

func newObjects() *memObjects { return &memObjects{put: map[string][]byte{}} }

func (o *memObjects) DeleteObject(_ context.Context, bucket, key string) error {
	if o.failDelete != nil {
		return o.failDelete
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.put, bucket+"/"+key)
	return nil
}

func (o *memObjects) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	if o.fail != "" && strings.HasSuffix(key, o.fail) {
		return errors.New("the bucket refused it")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.put[bucket+"/"+key] = data
	return nil
}

func (o *memObjects) keys() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.put))
	for k := range o.put {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (o *memObjects) has(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.put[key]
	return ok
}

// fakeApprovals records what a promotion asked of the queue.
type fakeApprovals struct {
	approved    bool
	asked       [][3]string // release, target, digest
	submitted   []approval.SubmitRelease
	selfRecords []approval.SubmitRelease
	actors      []approval.Actor
	failSubmit  error
	failSelf    error
}

func (f *fakeApprovals) SubmitRelease(_ context.Context, actor approval.Actor, in approval.SubmitRelease) (*approval.Request, error) {
	if f.failSubmit != nil {
		return nil, f.failSubmit
	}
	f.submitted, f.actors = append(f.submitted, in), append(f.actors, actor)
	return &approval.Request{ID: "apr_1", Status: approval.StatusPending, Target: in.Target}, nil
}

func (f *fakeApprovals) ReleaseApproved(_ context.Context, releaseID, target, digest string) bool {
	f.asked = append(f.asked, [3]string{releaseID, target, digest})
	return f.approved
}

func (f *fakeApprovals) RecordReleaseSelfApproval(_ context.Context, actor approval.Actor, in approval.SubmitRelease) error {
	if f.failSelf != nil {
		return f.failSelf
	}
	f.selfRecords, f.actors = append(f.selfRecords, in), append(f.actors, actor)
	return nil
}

type fakeApps map[string]*application.Application

func (f fakeApps) GetByName(_ context.Context, name string) (*application.Application, error) {
	if a, ok := f[name]; ok {
		return a, nil
	}
	return nil, application.ErrNotFound
}
