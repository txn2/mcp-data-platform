package appsvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

const sitePart = "marketing/prod"

// fakeSites answers the site calls an application makes and records each pin
// it is asked to apply.
type fakeSites struct {
	owner    string
	ownerErr error
	// latest is what StepRelease answers for an environment step does not
	// name, and askedStep records each environment it was asked of.
	latest     int
	latestErr  error
	step       map[string]int
	askedStep  []string
	releases   map[int]bool
	failPin    error
	pins       []pinCall
	askedOwner [][2]string
	// envs is every site environment SiteEnvironments lists; candidateErr
	// fails it; listedEnvs counts the calls.
	envs         []application.SiteEnvironment
	candidateErr error
	listedEnvs   int
}

type pinCall struct {
	actor, site, env string
	number           int
}

func (f *fakeSites) EnvironmentOwner(_ context.Context, site, environment string) (string, error) {
	f.askedOwner = append(f.askedOwner, [2]string{site, environment})
	return f.owner, f.ownerErr
}

func (f *fakeSites) StepRelease(_ context.Context, _, environment string) (int, error) {
	f.askedStep = append(f.askedStep, environment)
	if n, ok := f.step[environment]; ok {
		return n, f.latestErr
	}
	return f.latest, f.latestErr
}

func (f *fakeSites) ReleaseExists(_ context.Context, _ string, number int) bool {
	return f.releases[number]
}

func (f *fakeSites) ApplyPin(_ context.Context, actorEmail, site, environment string, number int) error {
	if f.failPin != nil {
		return f.failPin
	}
	f.pins = append(f.pins, pinCall{actorEmail, site, environment, number})
	return nil
}

func (f *fakeSites) SiteEnvironments(context.Context) ([]application.SiteEnvironment, error) {
	f.listedEnvs++
	if f.candidateErr != nil {
		return nil, f.candidateErr
	}
	return slices.Clone(f.envs), nil
}

func siteFixture(t *testing.T) (*Service, *memStore, *fakeSites) {
	t.Helper()
	svc, store, _, _, _ := fixture()
	sites := &fakeSites{owner: jane.Email, latest: 3, releases: map[int]bool{1: true, 2: true, 3: true}}
	svc.Sites = sites
	_, err := svc.Create(context.Background(), jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	return svc, store, sites
}

func TestAddPart_ASite(t *testing.T) {
	ctx := context.Background()

	t.Run("the site's owner adds it", func(t *testing.T) {
		svc, _, sites := siteFixture(t)
		a, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
		require.NoError(t, err)
		require.Len(t, a.Parts, 1)
		assert.Equal(t, application.KindSite, a.Parts[0].Kind)
		assert.Equal(t, sitePart, a.Parts[0].ID)
		assert.Equal(t, [][2]string{{"marketing", "prod"}}, sites.askedOwner)
	})
	t.Run("the owner in other capitals", func(t *testing.T) {
		svc, _, sites := siteFixture(t)
		sites.owner = "JANE@Example.com"
		_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
		require.NoError(t, err)
	})
	t.Run("an administrator adds anyone's", func(t *testing.T) {
		svc, _, sites := siteFixture(t)
		sites.owner = joe.Email
		_, err := svc.AddPart(ctx, admin, "shop", application.KindSite, sitePart)
		require.NoError(t, err)
	})
	t.Run("someone else's site is refused", func(t *testing.T) {
		svc, store, sites := siteFixture(t)
		sites.owner = joe.Email
		_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
		require.ErrorIs(t, err, application.ErrNotAllowed)
		assert.Empty(t, store.apps["app_shop"].Parts)
	})
	for name, id := range map[string]string{"no environment": "marketing", "no site": "/prod", "an empty environment": "marketing/", "empty": ""} {
		t.Run("a malformed id: "+name, func(t *testing.T) {
			svc, _, sites := siteFixture(t)
			_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, id)
			var r *RefusedError
			require.ErrorAs(t, err, &r)
			assert.Contains(t, r.Msg, `"<site>/<environment>"`)
			assert.Empty(t, sites.askedOwner, "refused before asking")
		})
	}
	t.Run("no sites on this deployment", func(t *testing.T) {
		svc, _, _ := siteFixture(t)
		svc.Sites = nil
		_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
		var r *RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "no sites")
	})
	t.Run("an environment that is not there", func(t *testing.T) {
		svc, _, sites := siteFixture(t)
		sites.ownerErr = errors.New("the site has no such environment")
		_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
		var r *RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "the site has no such environment")
		assert.Contains(t, r.Msg, sitePart)
	})
	t.Run("a part belongs to one application", func(t *testing.T) {
		svc, _, _ := siteFixture(t)
		_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
		require.NoError(t, err)
		_, err = svc.Create(ctx, jane, application.Application{Name: "billing"})
		require.NoError(t, err)
		_, err = svc.AddPart(ctx, jane, "billing", application.KindSite, sitePart)
		require.ErrorIs(t, err, application.ErrPartTaken)
		assert.ErrorContains(t, err, `"shop"`)
	})
}

func TestNewestPin_ASite(t *testing.T) {
	ctx := context.Background()
	part := application.Part{Kind: application.KindSite, ID: sitePart}
	cases := []struct {
		name    string
		part    application.Part
		set     func(*Service, *fakeSites)
		version int
		problem string
	}{
		{name: "the newest release", part: part, version: 3},
		{
			name: "the environment before serves nothing", part: part, set: func(_ *Service, f *fakeSites) { f.latest = 0 },
			problem: "has nothing to pin: the environment before prod serves no release; promote one to it first",
		},
		{
			name: "a dev part on a site with no release", part: application.Part{Kind: application.KindSite, ID: "marketing/dev"},
			set: func(_ *Service, f *fakeSites) { f.latest = 0 }, problem: "has nothing to pin: the site has no release; publish one first",
		},
		{name: "a site that cannot be read", part: part, set: func(_ *Service, f *fakeSites) { f.latestErr = errors.New("gone") }, problem: "site marketing/prod: gone"},
		{name: "a malformed id", part: application.Part{Kind: application.KindSite, ID: "marketing"}, problem: "cannot be read"},
		{name: "no sites here", part: part, set: func(s *Service, _ *fakeSites) { s.Sites = nil }, problem: "cannot be read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, sites := siteFixture(t)
			if tc.set != nil {
				tc.set(svc, sites)
			}
			pin, problem := svc.newestPin(ctx, tc.part)
			assert.Equal(t, application.KindSite, pin.Kind)
			assert.Equal(t, tc.part.ID, pin.PartID)
			if tc.problem != "" {
				assert.Contains(t, problem, tc.problem)
				assert.Zero(t, pin.Version)
				return
			}
			assert.Empty(t, problem)
			assert.Equal(t, tc.version, pin.Version)
		})
	}
}

func TestRelease_PointsEachSitePartAtThePinnedRelease(t *testing.T) {
	svc, store, sites := siteFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)

	rel, err := svc.Release(ctx, admin, "shop", "launch")
	require.NoError(t, err)
	assert.Contains(t, rel.Pins, application.Pin{Kind: application.KindSite, PartID: sitePart, Version: 3})
	assert.Equal(t, []pinCall{{admin.Email, "marketing", "prod", 3}}, sites.pins, "only the site part moves a pointer")
	assert.Equal(t, 3, store.held[application.KindSite+"/"+sitePart])
}

func TestRelease_ASiteWithNoReleaseRefusesTheRelease(t *testing.T) {
	svc, store, sites := siteFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	sites.latest = 0

	_, err = svc.Release(ctx, admin, "shop", "")
	var r *RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "nothing was released")
	assert.Contains(t, r.Msg, "the environment before prod serves no release")
	assert.Empty(t, sites.pins)
	assert.Empty(t, store.releases["app_shop"])
}

func TestRelease_APinThatCannotBeAppliedIsReported(t *testing.T) {
	svc, _, sites := siteFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	boom := errors.New("another move got there first")
	sites.failPin = boom

	_, err = svc.Release(ctx, admin, "shop", "")
	require.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, "site marketing/prod was not pointed at release 3")
}

func TestRollback_PointsTheSitePartBack(t *testing.T) {
	svc, _, sites := siteFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	sites.latest = 4
	sites.releases[4] = true
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	sites.pins = nil

	rel, err := svc.Rollback(ctx, admin, "shop", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, rel.Restored)
	assert.Equal(t, []pinCall{{admin.Email, "marketing", "prod", 3}}, sites.pins, "the environment goes back to the release that was pinned")
}

func TestRollback_ARemovedSiteReleaseRefusesIt(t *testing.T) {
	svc, store, sites := siteFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	sites.latest = 4
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	sites.pins = nil
	delete(sites.releases, 3)
	recorded := len(store.releases["app_shop"])

	_, err = svc.Rollback(ctx, admin, "shop", 1)
	var r *RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "nothing was rolled back")
	assert.Contains(t, r.Msg, "site marketing/prod version 3")
	assert.Empty(t, sites.pins)
	assert.Len(t, store.releases["app_shop"], recorded)
}

func TestRollback_APinThatCannotBeAppliedIsReported(t *testing.T) {
	svc, _, sites := siteFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	sites.latest = 2
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	boom := errors.New("down")
	sites.failPin = boom

	_, err = svc.Rollback(ctx, admin, "shop", 1)
	require.ErrorIs(t, err, boom)
}

func TestApplySitePins_OnlySiteParts(t *testing.T) {
	ctx := context.Background()
	pins := []application.Pin{
		{Kind: application.KindScript, PartID: scriptA, Version: 1},
		{Kind: application.KindAsset, PartID: "as1", Version: 1},
		{Kind: application.KindSite, PartID: "malformed", Version: 1},
		{Kind: application.KindSite, PartID: sitePart, Version: 2},
		{Kind: application.KindSite, PartID: "blog/dev", Version: 5},
	}

	svc, _, sites := siteFixture(t)
	require.NoError(t, svc.applySitePins(ctx, admin, pins))
	assert.Equal(t, []pinCall{{admin.Email, "marketing", "prod", 2}, {admin.Email, "blog", "dev", 5}}, sites.pins)

	svc.Sites = nil
	require.NoError(t, svc.applySitePins(ctx, admin, pins), "a deployment with no sites has nothing to point")
}

func TestReadable_ASite(t *testing.T) {
	ctx := context.Background()
	pin := func(id string, v int) application.Pin {
		return application.Pin{Kind: application.KindSite, PartID: id, Version: v}
	}
	svc, _, _ := siteFixture(t)
	assert.True(t, svc.readable(ctx, pin(sitePart, 2)))
	assert.False(t, svc.readable(ctx, pin(sitePart, 9)), "a release that is gone")
	assert.False(t, svc.readable(ctx, pin("malformed", 2)), "an id that does not name a site and environment")
	svc.Sites = nil
	assert.False(t, svc.readable(ctx, pin(sitePart, 2)), "a deployment with no sites")
}

// The branches beside the site's, so one kind's case cannot answer for another.
func TestReadable_TheOtherKinds(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := siteFixture(t)
	assert.True(t, svc.readable(ctx, application.Pin{Kind: application.KindScript, PartID: scriptA, Version: 1}))
	assert.False(t, svc.readable(ctx, application.Pin{Kind: application.KindScript, PartID: scriptA, Version: 9}))
	assert.True(t, svc.readable(ctx, application.Pin{Kind: application.KindAsset, PartID: "as1", Version: 1}))
	assert.False(t, svc.readable(ctx, application.Pin{Kind: application.KindAsset, PartID: "as1", Version: 9}))
	assert.True(t, svc.readable(ctx, application.Pin{Kind: application.KindSource, PartID: "orders-hook"}), "a webhook source pins nothing to read")

	svc.Scripts, svc.AssetVersions = nil, nil
	assert.False(t, svc.readable(ctx, application.Pin{Kind: application.KindScript, PartID: scriptA, Version: 1}))
	assert.False(t, svc.readable(ctx, application.Pin{Kind: application.KindAsset, PartID: "as1", Version: 1}))
}

func TestDescribe_ASiteIsNamedByItsPartID(t *testing.T) {
	svc, _, _ := siteFixture(t)
	got := svc.Describe(context.Background(), jane, []application.Part{{Kind: application.KindSite, ID: sitePart, AddedBy: jane.Email}}, nil)
	require.Len(t, got, 1)
	assert.Equal(t, PartOK, got[0].State)
	assert.Equal(t, sitePart, got[0].Name)
}

// The old name of dev is read as dev for one release (#2604): the part is kept
// as <site>/dev, so releasing and rolling back move dev.
func TestAddPart_ASiteByTheOldNameOfDev(t *testing.T) {
	svc, store, sites := siteFixture(t)
	a, err := svc.AddPart(context.Background(), jane, "shop", application.KindSite, "marketing/default")
	require.NoError(t, err)
	require.Len(t, a.Parts, 1)
	assert.Equal(t, "marketing/dev", a.Parts[0].ID)
	assert.Equal(t, [][2]string{{"marketing", "dev"}}, sites.askedOwner)
	assert.Equal(t, "marketing/dev", store.apps["app_shop"].Parts[0].ID)

	b, err := svc.Create(context.Background(), jane, application.Application{Name: "defaults"})
	require.NoError(t, err)
	b, err = svc.AddPart(context.Background(), jane, b.Name, application.KindSite, "default/prod")
	require.NoError(t, err, "a site named default is not the alias")
	assert.Equal(t, "default/prod", b.Parts[0].ID)
}

// A new application cannot hold "--": <site>--<env> is a site environment's
// address on the pages domain, and an application holding it would take it
// over (#2604).
func TestCreate_RefusesADoubleHyphen(t *testing.T) {
	svc, store, _, _, _ := fixture()
	_, err := svc.Create(context.Background(), jane, application.Application{Name: "intranet--dev"})
	var r *RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "kept for site environment addresses")
	assert.Empty(t, store.apps)

	_, err = svc.Create(context.Background(), jane, application.Application{Name: "intranet-dev"})
	require.NoError(t, err, "a single hyphen is fine")
}

// TestFindCandidates_Sites pins #2613: the search for parts lists the site
// environments the caller may add, by the rule AddPart applies: the
// environments of their own sites (their address matched case aside), every
// site's for an administrator, none where the deployment has no sites, by
// site and then environment, matched on "<site>/<environment>". One already
// in this application is left out and one in another application names it.
func TestFindCandidates_Sites(t *testing.T) {
	ctx := context.Background()
	svc, store, sites := siteFixture(t)
	svc.Lister = &fakeLister{}
	moved := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	sites.envs = []application.SiteEnvironment{
		{Site: "marketing", Environment: "prod", Owner: jane.Email},
		{Site: "docs", Environment: "prod", Owner: joe.Email},
		{Site: "marketing", Environment: "dev", Owner: "JANE@example.com", UpdatedAt: moved},
	}
	billing, err := svc.Create(ctx, jane, application.Application{Name: "billing"})
	require.NoError(t, err)
	require.NoError(t, store.AddPart(ctx, billing.ID, application.Part{Kind: application.KindSite, ID: sitePart}))

	got, err := svc.FindCandidates(ctx, jane, "shop", "", application.KindSite)
	require.NoError(t, err)
	require.Len(t, got.Candidates, 2, "a person lists the environments of their own sites")
	assert.Equal(t, Candidate{Kind: application.KindSite, ID: "marketing/dev", Name: "marketing/dev", OwnerEmail: "JANE@example.com", UpdatedAt: &moved}, got.Candidates[0])
	assert.Equal(t, sitePart, got.Candidates[1].ID)
	require.NotNil(t, got.Candidates[1].Application, "an environment in another application names it")
	assert.Equal(t, "billing", got.Candidates[1].Application.Name)
	assert.Nil(t, got.Candidates[1].UpdatedAt, "no time is none, never the zero time")
	assert.Equal(t, 2, got.Counts[application.KindSite])

	byAdmin, err := svc.FindCandidates(ctx, admin, "shop", "Prod", "")
	require.NoError(t, err)
	assert.Equal(t, 2, byAdmin.Counts[application.KindSite], "an administrator lists every site, matched case aside")

	_, err = svc.AddPart(ctx, jane, "shop", application.KindSite, "marketing/dev")
	require.NoError(t, err)
	after, err := svc.FindCandidates(ctx, jane, "shop", "", application.KindSite)
	require.NoError(t, err)
	assert.Len(t, after.Candidates, 1, "a part of this application is not listed")

	many := make([]application.SiteEnvironment, 0, MaxCandidates+5)
	for i := range MaxCandidates + 5 {
		many = append(many, application.SiteEnvironment{Site: "big", Environment: fmt.Sprintf("e%03d", i), Owner: jane.Email})
	}
	sites.envs = many
	bounded, err := svc.FindCandidates(ctx, jane, "shop", "", application.KindSite)
	require.NoError(t, err)
	assert.Len(t, bounded.Candidates, MaxCandidates, "at most MaxCandidates of a kind")

	sites.candidateErr = errors.New("db down")
	_, err = svc.FindCandidates(ctx, jane, "shop", "", "")
	require.Error(t, err)

	svc.Sites = nil
	calls := sites.listedEnvs
	none, err := svc.FindCandidates(ctx, jane, "shop", "", application.KindSite)
	require.NoError(t, err)
	assert.Empty(t, none.Candidates, "a deployment with no sites lists none")
	assert.Equal(t, calls, sites.listedEnvs)
}

// An application release pins each site environment part one step (#2617):
// at what the environment before it serves, read before any part moves, so
// stage and prod parts of one application each move one step.
func TestRelease_PinsEachSitePartOneStep(t *testing.T) {
	svc, _, sites := siteFixture(t)
	ctx := context.Background()
	sites.step = map[string]int{"stage": 3, "prod": 2}
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, "marketing/stage")
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.Release(ctx, admin, "shop", "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []pinCall{{admin.Email, "marketing", "stage", 3}, {admin.Email, "marketing", "prod", 2}}, sites.pins,
		"prod takes what stage served before the release, not what stage takes in it")
	assert.Subset(t, sites.askedStep, []string{"stage", "prod"}, "each part is asked of its own environment")
}
