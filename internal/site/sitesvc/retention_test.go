package sitesvc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
)

// prune marks release n of the site pruned, as a sweep leaves it.
func prune(store *memStore, n int) {
	for i := range store.releases[siteID] {
		if store.releases[siteID][i].Number == n {
			store.releases[siteID][i].Pruned = true
		}
	}
}

func TestSetEnvironment_WhoChangesTheReleaseLimit(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		actor  Actor
		change EnvironmentChange
		msg    string
	}{
		{"the owner", owner, EnvironmentChange{Keep: new(5)}, ""},
		{"an administrator", admin, EnvironmentChange{Keep: new(0)}, ""},
		{"the owner back to the default", owner, EnvironmentChange{KeepDefault: true}, ""},
		{"a manager", manager, EnvironmentChange{Keep: new(5)}, "only the owner"},
		{"a manager back to the default", manager, EnvironmentChange{KeepDefault: true}, "only the owner"},
		{"a negative limit", owner, EnvironmentChange{Keep: new(-1)}, "keep_releases"},
		{"a limit past the most", admin, EnvironmentChange{Keep: new(siteconfig.MaxKeepReleases + 1)}, "keep_releases"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			grant(store, manager.Email, site.LevelManage)
			_, err := svc.SetEnvironment(ctx, tc.actor, "marketing", envProd, tc.change)
			if tc.msg == "" {
				require.NoError(t, err)
				e := store.sites[siteID].Environment(envProd)
				if tc.change.KeepDefault {
					assert.True(t, e.KeepDefault)
					return
				}
				require.NotNil(t, e.KeepReleases)
				assert.Equal(t, *tc.change.Keep, *e.KeepReleases)
				return
			}
			require.ErrorContains(t, err, tc.msg)
			assert.Nil(t, store.sites[siteID].Environment(envProd).KeepReleases, "a refused change leaves the limit")
		})
	}
	t.Run("a manager still changes what managing allows", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		grant(store, manager.Email, site.LevelManage)
		_, err := svc.SetEnvironment(ctx, manager, "marketing", "stage", EnvironmentChange{InternalView: new(true)})
		require.NoError(t, err)
	})
}

func TestAddDomain_CreatesTheEnvironmentWithItsLimit(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "qa.client.example", Site: "marketing", Environment: "qa", Keep: new(4), Place: &site.Place{After: "stage"}})
	require.NoError(t, err)
	e := store.sites[siteID].Environment("qa")
	require.NotNil(t, e)
	require.NotNil(t, e.KeepReleases)
	assert.Equal(t, 4, *e.KeepReleases)

	grant(store, manager.Email, site.LevelManage)
	_, err = svc.AddDomain(ctx, manager, DomainRequest{Hostname: "uat.client.example", Site: "marketing", Environment: "uat", Keep: new(4), Place: &site.Place{After: "stage"}})
	require.ErrorContains(t, err, "only the owner")
}

func TestPrunedReleases_AreNeverReachedAgain(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*Service, *memStore) {
		t.Helper()
		svc, store, _, _ := fixture(t)
		for n := 1; n <= 4; n++ {
			released(store, n, site.EnvDev)
		}
		store.sites[siteID].Environment(site.EnvDev).Keep = 2
		store.sites[siteID].Environment("stage").Keep = 1
		prune(store, 2)
		return svc, store
	}
	t.Run("a rollback to one is refused, naming the limits", func(t *testing.T) {
		svc, _ := setup(t)
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 2)
		require.ErrorIs(t, err, site.ErrPruned)
		assert.Contains(t, err.Error(), "release 2 was removed by the release limit")
		assert.Contains(t, err.Error(), "dev keeps the last 2")
		assert.Contains(t, err.Error(), "stage keeps the last 1")
	})
	t.Run("a rollback with no release named passes over it", func(t *testing.T) {
		svc, store := setup(t)
		prune(store, 3)
		got, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, got.Release, "3 and 2 are gone, so the newest kept release before 4")
	})
	t.Run("a rollback with nothing kept before is refused", func(t *testing.T) {
		svc, store := setup(t)
		prune(store, 1)
		prune(store, 3)
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 0)
		require.ErrorContains(t, err, "no earlier release it still keeps")
	})
	t.Run("a promote of one is refused", func(t *testing.T) {
		svc, store := setup(t)
		store.sites[siteID].Environment(site.EnvDev).Release = 2
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", To: "stage"})
		require.ErrorIs(t, err, site.ErrPruned)
	})
	t.Run("an application pin to one is refused", func(t *testing.T) {
		svc, _ := setup(t)
		require.ErrorIs(t, svc.ApplyPin(ctx, owner.Email, "marketing", "stage", 2), site.ErrPruned)
		require.NoError(t, svc.ApplyPin(ctx, owner.Email, "marketing", "stage", 3))
	})
	t.Run("a comparison with one is refused, naming no environment", func(t *testing.T) {
		svc, _ := setup(t)
		_, err := svc.Compare(ctx, "marketing", 2, 4)
		require.ErrorIs(t, err, site.ErrPruned)
		assert.EqualError(t, err, "release 2 was removed by the release limit, so it cannot be compared")
		_, err = svc.Compare(ctx, "marketing", 1, 2)
		require.ErrorIs(t, err, site.ErrPruned)
		assert.NotContains(t, err.Error(), "stage", "Compare answers anyone who finds the site; its environments are the managers'")
	})
	t.Run("a move to one pruned after it was checked is refused, naming the limits", func(t *testing.T) {
		svc, store := setup(t)
		store.failPoint = site.ErrPruned
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 3)
		require.ErrorIs(t, err, site.ErrPruned)
		assert.Contains(t, err.Error(), "release 3 was removed by the release limit")
		assert.Contains(t, err.Error(), "dev keeps the last 2")
		require.ErrorIs(t, svc.ApplyPin(ctx, owner.Email, "marketing", "stage", 3), site.ErrPruned)
	})
	t.Run("an environment keeping every release says so", func(t *testing.T) {
		svc, store := setup(t)
		store.sites[siteID].Environment(envProd).Keep = 0
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 2)
		require.ErrorContains(t, err, "prod keeps every release")
	})
}

// A diff never reads a pruned release, whose chain would hand it a kept
// ancestor's text under its own number.
func TestDiff_APrunedReleaseIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)
	_, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: homeRef, Edits: edit("$99", "$89")})
	require.NoError(t, err)
	prune(store, 1)
	for name, call := range map[string]func() error{
		"a file from the release before": func() error { _, err := svc.Diff(ctx, owner, homeRef, 0, 0); return err },
		"a tree from the release before": func() error { _, err := svc.Diff(ctx, owner, treeRef, 0, 0); return err },
		"a file named from":              func() error { _, err := svc.Diff(ctx, owner, homeRef, 1, 2); return err },
		"the release diffed to":          func() error { _, err := svc.Diff(ctx, owner, homeRef, 0, 1); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.ErrorIs(t, err, site.ErrPruned)
			assert.ErrorContains(t, err, "release 1 was removed by the release limit, so it cannot be compared")
		})
	}
}

// holdingStore is a store that holds a site's stored files, as the PostgreSQL
// store does.
type holdingStore struct {
	*memStore
	holdErr, recordErr error
	held               int
}

func (h *holdingStore) HoldUpload(context.Context, string) (*sitestore.Hold, error) {
	h.held++
	if h.holdErr != nil {
		return nil, h.holdErr
	}
	return &sitestore.Hold{}, nil
}

func (h *holdingStore) RecordRelease(ctx context.Context, r *site.Release, move *site.Pointer) error {
	if h.recordErr != nil {
		return h.recordErr
	}
	return h.memStore.RecordRelease(ctx, r, move)
}

// A publish holds the site's stored files from its first stored file until
// its release is recorded; a sweep holding them, or a hold that ran out, is
// a refusal in the hold's words.
func TestPublish_HoldsTheSitesStoredFiles(t *testing.T) {
	ctx := context.Background()
	req := PublishRequest{Site: "marketing", Files: []InputFile{{Path: "index.html", Body: []byte("<p>held</p>")}}}
	cases := []struct {
		name               string
		holdErr, recordErr error
		want               error
	}{
		{"held and recorded", nil, nil, nil},
		{"a sweep holds the files", sitestore.ErrCleanupRunning, nil, sitestore.ErrCleanupRunning},
		{"the hold ran out", nil, sitestore.ErrHoldExpired, sitestore.ErrHoldExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			held := &holdingStore{memStore: store, holdErr: tc.holdErr, recordErr: tc.recordErr}
			svc.Store = held
			_, err := svc.Publish(ctx, owner, req)
			assert.Equal(t, 1, held.held)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
			refused, ok := errors.AsType[*RefusedError](err)
			require.True(t, ok, "a refusal the caller reads")
			assert.Equal(t, tc.want.Error(), refused.Msg)
		})
	}
	assert.NoError(t, holdRefusal(nil))
	other := errors.New("other")
	assert.Equal(t, other, holdRefusal(other))
}
