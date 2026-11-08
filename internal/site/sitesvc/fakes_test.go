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
// release the move was decided against.
type memStore struct {
	sites       map[string]*site.Site
	releases    map[string][]site.Release
	deployments map[string][]site.Deployment
	domains     map[string]*site.Domain
	failPoint   error
	failRecord  error
	failDomains error
	failListing error
}

func newMem() *memStore {
	return &memStore{
		sites: map[string]*site.Site{}, releases: map[string][]site.Release{},
		deployments: map[string][]site.Deployment{}, domains: map[string]*site.Domain{},
	}
}

func cloneSite(st *site.Site) *site.Site {
	c := *st
	c.Environments = slices.Clone(st.Environments)
	return &c
}

func (m *memStore) Create(_ context.Context, st *site.Site) error {
	for _, x := range m.sites {
		if x.Name == st.Name {
			return site.ErrNameTaken
		}
	}
	m.sites[st.ID] = cloneSite(st)
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*site.Site, error) {
	st, ok := m.sites[id]
	if !ok {
		return nil, site.ErrNotFound
	}
	return cloneSite(st), nil
}

func (m *memStore) GetByName(ctx context.Context, name string) (*site.Site, error) {
	for id, st := range m.sites {
		if st.Name == name {
			return m.Get(ctx, id)
		}
	}
	return nil, site.ErrNotFound
}

func (m *memStore) List(context.Context) ([]site.Site, error) {
	out := make([]site.Site, 0, len(m.sites))
	for _, st := range m.sites {
		out = append(out, *cloneSite(st))
	}
	return out, nil
}

func (m *memStore) Update(_ context.Context, st *site.Site) error {
	x, ok := m.sites[st.ID]
	if !ok {
		return site.ErrNotFound
	}
	x.Description, x.OwnerEmail = st.Description, st.OwnerEmail
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	if _, ok := m.sites[id]; !ok {
		return site.ErrNotFound
	}
	delete(m.sites, id)
	return nil
}

func (m *memStore) SetEnvironment(_ context.Context, siteID, name string, requiresApproval bool, actor string) error {
	st := m.sites[siteID]
	if e := st.Environment(name); e != nil {
		e.RequiresApproval, e.UpdatedBy = requiresApproval, actor
		return nil
	}
	st.Environments = append(st.Environments, site.Environment{Name: name, RequiresApproval: requiresApproval, UpdatedBy: actor})
	return nil
}

func (m *memStore) RecordRelease(_ context.Context, r *site.Release) error {
	if m.failRecord != nil {
		return m.failRecord
	}
	r.Number = len(m.releases[r.SiteID]) + 1
	m.releases[r.SiteID] = append([]site.Release{*r}, m.releases[r.SiteID]...)
	return nil
}

func (m *memStore) Release(_ context.Context, siteID string, number int) (*site.Release, error) {
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
	if m.failListing != nil {
		return nil, m.failListing
	}
	return m.releases[siteID], nil
}

func (m *memStore) Files(_ context.Context, releaseID string) ([]site.File, error) {
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
	if _, ok := m.domains[d.Hostname]; ok {
		return site.ErrDomainTaken
	}
	c := *d
	m.domains[d.Hostname] = &c
	return nil
}

func (m *memStore) Domain(_ context.Context, hostname string) (*site.Domain, error) {
	d, ok := m.domains[hostname]
	if !ok {
		return nil, site.ErrNoDomain
	}
	c := *d
	return &c, nil
}

func (m *memStore) Domains(context.Context) ([]site.Domain, error) {
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

func (m *memStore) MarkVerified(_ context.Context, hostname, by string) error {
	d, ok := m.domains[hostname]
	if !ok {
		return site.ErrNoDomain
	}
	now := time.Unix(1700000000, 0)
	d.VerifiedAt, d.VerifiedBy = &now, by
	return nil
}

func (m *memStore) RemoveDomain(_ context.Context, hostname string) error {
	if _, ok := m.domains[hostname]; !ok {
		return site.ErrNoDomain
	}
	delete(m.domains, hostname)
	return nil
}

func (*memStore) RouteHost(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (*memStore) RoutePreview(context.Context, string, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

// memObjects is the bucket. Uploads run concurrently, so it locks.
type memObjects struct {
	mu   sync.Mutex
	put  map[string][]byte
	fail string // a key suffix whose put fails
}

func newObjects() *memObjects { return &memObjects{put: map[string][]byte{}} }

func (o *memObjects) DeleteObject(_ context.Context, bucket, key string) error {
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

type fakeVerifier struct {
	err   error
	calls [][2]string
}

func (f *fakeVerifier) Verify(_ context.Context, hostname, token string) error {
	f.calls = append(f.calls, [2]string{hostname, token})
	return f.err
}
