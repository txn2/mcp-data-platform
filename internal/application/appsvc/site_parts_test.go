package appsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

const sitePart = "marketing/prod"

// fakeSites answers the site calls an application makes and records each pin
// it is asked to apply.
type fakeSites struct {
	owner      string
	ownerErr   error
	latest     int
	latestErr  error
	releases   map[int]bool
	failPin    error
	pins       []pinCall
	askedOwner [][2]string
}

type pinCall struct {
	actor, site, env string
	number           int
}

func (f *fakeSites) EnvironmentOwner(_ context.Context, site, environment string) (string, error) {
	f.askedOwner = append(f.askedOwner, [2]string{site, environment})
	return f.owner, f.ownerErr
}

func (f *fakeSites) LatestRelease(context.Context, string) (int, error) {
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
		{name: "a site with no release", part: part, set: func(_ *Service, f *fakeSites) { f.latest = 0 }, problem: "has no release to pin; publish one first"},
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
	assert.Contains(t, r.Msg, "has no release to pin")
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
