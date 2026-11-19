package appsvc

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

// recordIn records a release into one environment of an application with
// several, holding its pins only in the last.
func (m *memStore) recordIn(id string, r application.Recorded) (*application.Release, error) {
	a := m.apps[id]
	e, i, ok := a.EnvironmentNamed(r.Environment)
	if !ok {
		return nil, application.ErrNotFound
	}
	if e.Release != r.After {
		return nil, application.ErrStale
	}
	rel := r.Release
	rel.Number = len(m.releases[id]) + 1
	m.releases[id] = append(m.releases[id], rel)
	m.runIn(a, i, rel.Number)
	return &rel, nil
}

func (m *memStore) runIn(a *application.Application, i, number int) {
	a.Environments[i].Release = number
	if i == len(a.Environments)-1 {
		a.CurrentRelease = number
		for _, r := range m.releases[a.ID] {
			if r.Number == number {
				for _, p := range r.Pins {
					m.held[p.Kind+"/"+p.PartID] = p.Version
				}
			}
		}
	}
}

func (m *memStore) AddEnvironment(_ context.Context, id string, e application.Environment, after string) error {
	a := m.apps[id]
	at := 0
	if after != "" {
		_, i, _ := a.EnvironmentNamed(after)
		at = i + 1
	}
	if _, _, ok := a.EnvironmentNamed(e.Name); ok && e.Name != "" {
		return application.ErrNameTaken
	}
	if e.Held != nil {
		m.runRoles[id+"/"+e.Held.Schema] = application.RunRole{
			ApplicationID: id, Connection: e.Held.Connection, Schema: e.Held.Schema, Role: e.Held.Role, SealedPassword: e.Held.SealedPassword,
		}
	}
	e.Held = nil
	a.Environments = slices.Insert(a.Environments, at, e)
	return nil
}

func (m *memStore) RemoveEnvironment(_ context.Context, id, name string) error {
	a := m.apps[id]
	a.Environments = slices.DeleteFunc(a.Environments, func(e application.Environment) bool { return e.Name == name })
	return nil
}

func (m *memStore) SetEnvironmentRelease(_ context.Context, id string, mv application.Move) error {
	a := m.apps[id]
	e, i, _ := a.EnvironmentNamed(mv.Environment)
	if e.Release != mv.Expect {
		return application.ErrStale
	}
	m.runIn(a, i, mv.Number)
	return nil
}

func (m *memStore) SetEnvironmentApproval(_ context.Context, id, name string, requires bool, _ string) error {
	e, _, _ := m.apps[id].EnvironmentNamed(name)
	e.RequiresApproval = requires
	return nil
}

func (m *memStore) Variables(_ context.Context, id string) ([]application.Variable, error) {
	return slices.Clone(m.vars[id]), nil
}

func (m *memStore) SetVariable(_ context.Context, id string, v application.Variable) error {
	m.vars[id] = append(slices.DeleteFunc(m.vars[id], func(x application.Variable) bool {
		return x.Name == v.Name && x.Environment == v.Environment
	}), v)
	return nil
}

func (m *memStore) DeleteVariable(_ context.Context, id, env, name string) error {
	m.vars[id] = slices.DeleteFunc(m.vars[id], func(x application.Variable) bool { return x.Name == name && x.Environment == env })
	return nil
}

// envFixture is an application with dev and prod holding scriptA.
func envFixture(t *testing.T) (*Service, *memStore) {
	t.Helper()
	svc, store, _, _, _ := fixture()
	store.vars = map[string][]application.Variable{}
	svc.Environments = store
	a, err := svc.Create(context.Background(), jane, application.Application{
		Name: "shop", Environments: []application.Environment{{Name: "dev"}, {Name: "prod"}},
	})
	require.NoError(t, err)
	_, err = svc.AddPart(context.Background(), jane, a.ID, application.KindScript, scriptA)
	require.NoError(t, err)
	return svc, store
}

// A release lands in dev; promote moves it to prod, which then holds it; a
// rollback moves one environment back (#2696).
func TestReleasePromoteRollbackPerEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, store := envFixture(t)

	rel, err := svc.Release(ctx, admin, "shop", "first")
	require.NoError(t, err)
	a, _ := svc.Find(ctx, "shop")
	assert.Equal(t, rel.Number, a.Environments[0].Release)
	assert.Equal(t, 0, a.CurrentRelease, "dev's release is not live")
	assert.Zero(t, store.held["script/"+scriptA])

	_, err = svc.Promote(ctx, jane, "shop", "dev")
	require.ErrorIs(t, err, application.ErrNotAllowed)
	_, err = svc.Promote(ctx, admin, "shop", "prod")
	require.ErrorContains(t, err, "last environment")
	_, err = svc.Promote(ctx, admin, "shop", "qa")
	require.ErrorContains(t, err, `no environment "qa"`)
	p, err := svc.Promote(ctx, admin, "shop", "dev")
	require.NoError(t, err)
	assert.Equal(t, Promoted{From: "dev", To: "prod", Release: rel.Number, Applied: []int{}}, *p)
	_, err = svc.Promote(ctx, admin, "shop", "dev")
	require.ErrorContains(t, err, "already runs")
	a, _ = svc.Find(ctx, "shop")
	assert.Equal(t, rel.Number, a.CurrentRelease)
	assert.Equal(t, 1, store.held["script/"+scriptA])

	second, err := svc.Release(ctx, admin, "shop", "second")
	require.NoError(t, err)
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.NoError(t, err)
	a, _ = svc.Find(ctx, "shop")
	assert.Equal(t, rel.Number, a.Environments[0].Release, "dev back to the release before %d", second.Number)
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.ErrorContains(t, err, "no earlier release")
	_, err = svc.RollbackIn(ctx, jane, "shop", "dev", 0)
	require.ErrorIs(t, err, application.ErrNotAllowed)
}

func TestEnvironmentsAreAdministrators(t *testing.T) {
	ctx := context.Background()
	svc, _ := envFixture(t)

	_, err := svc.AddEnvironment(ctx, jane, "shop", application.Environment{Name: "stage"}, "dev")
	require.ErrorIs(t, err, application.ErrNotAllowed)
	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "Stage!"}, "dev")
	require.ErrorContains(t, err, "lowercase")
	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "stage"}, "prod")
	require.ErrorContains(t, err, "add the new one before it")
	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "stage"}, "qa")
	require.ErrorContains(t, err, `no environment "qa"`)
	a, err := svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "stage", RequiresApproval: true}, "dev")
	require.NoError(t, err)
	assert.Equal(t, "stage", a.Environments[1].Name)
	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "stage"}, "dev")
	require.ErrorContains(t, err, "already has")

	a, err = svc.SetEnvironmentApproval(ctx, admin, "shop", "stage", false)
	require.NoError(t, err)
	assert.False(t, a.Environments[1].RequiresApproval)
	_, err = svc.SetEnvironmentApproval(ctx, jane, "shop", "stage", true)
	require.ErrorIs(t, err, application.ErrNotAllowed)

	_, err = svc.RemoveEnvironment(ctx, admin, "shop", "prod")
	require.ErrorContains(t, err, "cannot be removed")
	_, err = svc.RemoveEnvironment(ctx, jane, "shop", "stage")
	require.ErrorIs(t, err, application.ErrNotAllowed)
	a, err = svc.RemoveEnvironment(ctx, admin, "shop", "stage")
	require.NoError(t, err)
	assert.Len(t, a.Environments, 2)

	_, err = svc.Create(ctx, jane, application.Application{Name: "twice", Environments: []application.Environment{{Name: "dev"}, {Name: "dev"}}})
	require.ErrorContains(t, err, "named twice")
	_, err = svc.Create(ctx, jane, application.Application{Name: "bad", Environments: []application.Environment{{Name: "Dev"}}})
	require.ErrorContains(t, err, "lowercase")
}

func TestVariablesAreTheOwnersAndAdministrators(t *testing.T) {
	ctx := context.Background()
	svc, _ := envFixture(t)

	require.NoError(t, svc.SetVariable(ctx, jane, "shop", application.Variable{Name: "API_BASE", Value: "https://x.example.com"}))
	require.NoError(t, svc.SetVariable(ctx, admin, "shop", application.Variable{Environment: "prod", Name: "API_BASE", Value: "https://y.example.com"}))
	require.ErrorIs(t, svc.SetVariable(ctx, joe, "shop", application.Variable{Name: "API_BASE", Value: "z"}), application.ErrNotAllowed)
	require.ErrorContains(t, svc.SetVariable(ctx, jane, "shop", application.Variable{Name: "lower", Value: "v"}), "uppercase")
	require.ErrorContains(t, svc.SetVariable(ctx, jane, "shop", application.Variable{Environment: "qa", Name: "A", Value: "v"}), `no environment "qa"`)
	vars, err := svc.Variables(ctx, jane, "shop")
	require.NoError(t, err)
	assert.Len(t, vars, 2)
	assert.Equal(t, map[string]string{"API_BASE": "https://y.example.com"}, application.VariablesFor(vars, "prod"))
	assert.Equal(t, map[string]string{"API_BASE": "https://x.example.com"}, application.VariablesFor(vars, "dev"))
	_, err = svc.Variables(ctx, joe, "shop")
	require.ErrorIs(t, err, application.ErrNotAllowed)
	require.NoError(t, svc.DeleteVariable(ctx, jane, "shop", "prod", "API_BASE"))
	vars, _ = svc.Variables(ctx, jane, "shop")
	assert.Len(t, vars, 1)
}

func TestEnvironmentsNeedAStore(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _ := fixture()
	_, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	_, err = svc.Promote(ctx, admin, "shop", "dev")
	require.ErrorIs(t, err, errNoEnvironments)
	_, err = svc.Variables(ctx, jane, "shop")
	require.ErrorIs(t, err, errNoEnvironments)
}

// The refusals each environment operation names (#2696).
func TestEnvironmentRefusals(t *testing.T) {
	ctx := context.Background()
	svc, _ := envFixture(t)
	_, err := svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)

	_, err = svc.RollbackIn(ctx, admin, "shop", "qa", 0)
	require.ErrorContains(t, err, `no environment "qa"`)
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 5)
	require.ErrorContains(t, err, "no earlier release")
	_, err = svc.RollbackIn(ctx, admin, "shop", "prod", 0)
	require.ErrorContains(t, err, "no earlier release", "the last environment is the application's rollback")
	_, err = svc.RollbackIn(ctx, admin, "nope", "dev", 0)
	require.ErrorIs(t, err, application.ErrNotFound)

	_, err = svc.RemoveEnvironment(ctx, admin, "shop", "qa")
	require.ErrorContains(t, err, `no environment "qa"`)
	_, err = svc.RemoveEnvironment(ctx, admin, "nope", "dev")
	require.ErrorIs(t, err, application.ErrNotFound)
	_, err = svc.SetEnvironmentApproval(ctx, admin, "shop", "qa", true)
	require.ErrorContains(t, err, `no environment "qa"`)
	_, err = svc.SetEnvironmentApproval(ctx, admin, "nope", "dev", true)
	require.ErrorIs(t, err, application.ErrNotFound)
	require.ErrorIs(t, svc.DeleteVariable(ctx, joe, "shop", "", "A"), application.ErrNotAllowed)
	_, err = svc.Variables(ctx, jane, "nope")
	require.ErrorIs(t, err, application.ErrNotFound)

	svc.Environments = nil
	_, err = svc.RemoveEnvironment(ctx, admin, "shop", "dev")
	require.ErrorIs(t, err, errNoEnvironments)
	_, err = svc.SetEnvironmentApproval(ctx, admin, "shop", "dev", true)
	require.ErrorIs(t, err, errNoEnvironments)
	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "qa"}, "")
	require.ErrorIs(t, err, errNoEnvironments)
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.Error(t, err)
	require.ErrorIs(t, svc.DeleteVariable(ctx, jane, "shop", "", "A"), errNoEnvironments)
}

// A promote or rollback to a release that can no longer be read changes
// nothing (#2696).
func TestEnvironmentMovesToAnUnreadableRelease(t *testing.T) {
	ctx := context.Background()
	svc, store := envFixture(t)
	_, err := svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)

	store.apps["app_shop"].Environments[0].Release = 99
	_, err = svc.Promote(ctx, admin, "shop", "dev")
	require.Error(t, err, "release 99 is not recorded")

	store.apps["app_shop"].Environments[0].Release = 2
	svc.Environments = nil
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.ErrorIs(t, err, errNoEnvironments)
	svc.Environments = store

	store.releases["app_shop"] = store.releases["app_shop"][1:]
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 1)
	require.Error(t, err, "release 1 is gone")
}

// siteEnvFixture is envFixture with a site, shop-web, whose dev and prod
// environments are coupled parts of shop (#2709).
func siteEnvFixture(t *testing.T) (*Service, *fakeSites) {
	t.Helper()
	svc, _ := envFixture(t)
	sites := &fakeSites{owner: jane.Email, latest: 5, step: map[string]int{"prod": 4}, releases: map[int]bool{3: true, 4: true, 5: true}}
	svc.Sites = sites
	for _, id := range []string{"shop-web/dev", "shop-web/prod"} {
		_, err := svc.AddPartMode(context.Background(), jane, "shop", application.Part{Kind: application.KindSite, ID: id, ReleaseMode: application.ModeCoupled})
		require.NoError(t, err)
	}
	return svc, sites
}

// A site environment part calls the application environment of its name,
// so one naming an environment the application lacks is refused, and an
// environment a part calls cannot be removed (#2709).
func TestSitePartsCallTheirEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, _ := siteEnvFixture(t)
	_, err := svc.AddPartMode(ctx, jane, "shop", application.Part{Kind: application.KindSite, ID: "shop-web/staging"})
	require.ErrorContains(t, err, "would call environment staging of application \"shop\", which it does not have (it has dev, prod); add environment staging first")
	_, err = svc.RemoveEnvironment(ctx, admin, "shop", "dev")
	require.ErrorContains(t, err, "dev can't be removed: site part shop-web/dev calls it. Remove that site part first")

	_, err = svc.AddEnvironment(ctx, admin, "shop", application.Environment{Name: "staging"}, "dev")
	require.NoError(t, err)
	_, err = svc.AddPartMode(ctx, jane, "shop", application.Part{Kind: application.KindSite, ID: "shop-web/staging"})
	require.NoError(t, err, "once the application has staging, its part is added")
}

// A release moves only the site parts calling the first environment; a
// promote moves the target's to the site release the source ran with it; a
// rollback returns only the parts calling the environment it moves (#2709).
func TestSitePartsMoveWithTheirEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, sites := siteEnvFixture(t)

	first, err := svc.Release(ctx, admin, "shop", "first")
	require.NoError(t, err)
	assert.Equal(t, []pinCall{{admin.Email, "shop-web", "dev", 5}}, sites.pins, "the release moves dev's site part only")

	sites.pins = nil
	_, err = svc.Promote(ctx, admin, "shop", "dev")
	require.NoError(t, err)
	assert.Equal(t, []pinCall{{admin.Email, "shop-web", "prod", 5}}, sites.pins, "prod's part moves to the site release dev ran with release %d", first.Number)

	sites.latest = 6
	sites.releases[6] = true
	sites.pins = nil
	_, err = svc.Release(ctx, admin, "shop", "second")
	require.NoError(t, err)
	assert.Equal(t, []pinCall{{admin.Email, "shop-web", "dev", 6}}, sites.pins)

	sites.pins = nil
	_, err = svc.Promote(ctx, admin, "shop", "dev")
	require.NoError(t, err)
	assert.Equal(t, []pinCall{{admin.Email, "shop-web", "prod", 6}}, sites.pins)

	sites.pins = nil
	_, err = svc.RollbackIn(ctx, admin, "shop", "prod", 0)
	require.NoError(t, err)
	assert.Equal(t, []pinCall{{admin.Email, "shop-web", "prod", 5}}, sites.pins, "a rollback in prod returns prod's part only")

	sites.pins = nil
	_, err = svc.RollbackIn(ctx, admin, "shop", "dev", 0)
	require.NoError(t, err)
	assert.Equal(t, []pinCall{{admin.Email, "shop-web", "dev", 5}}, sites.pins, "a rollback in dev returns dev's part only")
}

// A release needs nothing of a later environment's site part: it takes the
// pin of the site's part calling the first environment, so a site whose
// environment before prod serves no release does not refuse it (#2709).
func TestARelease_NeedsNoStepOfALaterSitePart(t *testing.T) {
	ctx := context.Background()
	svc, sites := siteEnvFixture(t)
	sites.step["prod"] = 0
	rel, err := svc.Release(ctx, admin, "shop", "first")
	require.NoError(t, err, "prod's part does not move on a release, so its own step is not what pins it")
	pins := map[string]int{}
	for _, p := range rel.Pins {
		pins[p.PartID] = p.Version
	}
	assert.Equal(t, map[string]int{"shop-web/dev": 5, "shop-web/prod": 5}, map[string]int{"shop-web/dev": pins["shop-web/dev"], "shop-web/prod": pins["shop-web/prod"]})
}
