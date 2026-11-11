package siteprune

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

const (
	sum1 = "1111111111111111111111111111111111111111111111111111111111111111"
	sum2 = "2222222222222222222222222222222222222222222222222222222222222222"
	sum3 = "3333333333333333333333333333333333333333333333333333333333333333"
)

var errDown = errors.New("down")

type fakeStore struct {
	mu      sync.Mutex
	refs    []sitestore.SiteRef
	results map[string]*sitestore.PruneResult
	applied []bool
	stored  map[string][]site.File
	sweeps  []sitestore.Sweep
	failOn  string
	// holdsLeft, when set for a site, is how many sweep holds it grants
	// before a publish holds it; holds counts the holds taken.
	holdsLeft map[string]int
	holds     int
}

func (f *fakeStore) HoldSweep(_ context.Context, id string) (*sitestore.Hold, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "hold:"+id {
		return nil, errDown
	}
	if left, ok := f.holdsLeft[id]; ok {
		if left == 0 {
			return nil, nil //nolint:nilnil // a nil hold is a site a publish holds
		}
		f.holdsLeft[id] = left - 1
	}
	f.holds++
	return &sitestore.Hold{}, nil
}

func (f *fakeStore) SiteRefs(context.Context) ([]sitestore.SiteRef, error) {
	if f.failOn == "refs" {
		return nil, errDown
	}
	return f.refs, nil
}

func (f *fakeStore) GetByName(_ context.Context, name string) (*site.Site, error) {
	for _, r := range f.refs {
		if r.Name == name {
			return &site.Site{ID: r.ID, Name: r.Name}, nil
		}
	}
	return nil, site.ErrNotFound
}

func (f *fakeStore) Prune(_ context.Context, id string, apply bool) (*sitestore.PruneResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, apply)
	if f.failOn == "prune:"+id {
		return nil, errDown
	}
	if r, ok := f.results[id]; ok {
		return r, nil
	}
	return &sitestore.PruneResult{Releases: []int{}, Objects: []site.File{}}, nil
}

func (f *fakeStore) StoredFiles(_ context.Context, id string) ([]site.File, error) {
	if f.failOn == "stored" {
		return nil, errDown
	}
	return f.stored[id], nil
}

func (f *fakeStore) RecordSweep(_ context.Context, w *sitestore.Sweep) error {
	if f.failOn == "record" {
		return errDown
	}
	w.FinishedAt = w.StartedAt
	f.sweeps = append([]sitestore.Sweep{*w}, f.sweeps...)
	return nil
}

func (f *fakeStore) Sweeps(context.Context, int) ([]sitestore.Sweep, error) {
	if f.failOn == "sweeps" {
		return nil, errDown
	}
	return f.sweeps, nil
}

type fakeBucket struct {
	mu      sync.Mutex
	objects []s3adapter.WalkedObject
	deleted []string
	refuse  map[string]bool
	walkErr error
}

func (b *fakeBucket) DeleteObject(_ context.Context, _, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refuse[key] {
		return errDown
	}
	b.deleted = append(b.deleted, key)
	return nil
}

func (b *fakeBucket) Walk(_ context.Context, _, prefix string, fn func(s3adapter.WalkedObject) error) error {
	if b.walkErr != nil {
		return b.walkErr
	}
	for _, o := range b.objects {
		if strings.HasPrefix(o.Key, prefix) {
			if err := fn(o); err != nil {
				return err
			}
		}
	}
	return nil
}

var now = time.Unix(1800000000, 0).UTC()

func runner(store *fakeStore, bucket *fakeBucket) *Runner {
	r := &Runner{
		Store: store, Bucket: "portal", Now: func() time.Time { return now },
		Config: func() siteconfig.Config {
			return siteconfig.Config{Releases: siteconfig.ReleasesConfig{OrphanGrace: time.Hour}}
		},
	}
	if bucket != nil {
		r.Objects, r.Walker = bucket, bucket
	}
	return r
}

func TestRun_DeleteRemovesWhatPruningFreedAndTheOrphans(t *testing.T) {
	store := &fakeStore{
		refs: []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}},
		results: map[string]*sitestore.PruneResult{"site_a": {
			Releases: []int{1, 2}, Rewritten: 1, Bytes: 30,
			Objects: []site.File{{SHA256: sum1, Size: 10}, {Path: "old.txt", SHA256: sum2, StoredIn: "srel_old", Size: 20}},
		}},
		stored: map[string][]site.File{"site_a": {{SHA256: sum3}}},
	}
	old := now.Add(-2 * time.Hour)
	bucket := &fakeBucket{objects: []s3adapter.WalkedObject{
		{Key: "sites/site_a/objects/" + sum3, Size: 5, LastModified: old},             // named
		{Key: "sites/site_a/objects/" + sum2 + ".gz", Size: 4, LastModified: old},     // orphan
		{Key: "sites/site_a/objects/" + sum1, Size: 9, LastModified: now},             // inside the grace
		{Key: "sites/site_a/notes.txt", Size: 1, LastModified: old},                   // not our layout
		{Key: "sites/site_a/releases/srel_x/files/a.css", Size: 3, LastModified: old}, // orphan
	}}
	rep, err := runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeDelete, RunBy: "admin@example.com"})
	require.NoError(t, err)

	assert.Equal(t, []bool{true}, store.applied)
	assert.Equal(t, 2, rep.Releases)
	assert.Equal(t, 2, rep.Files)
	assert.Equal(t, int64(30), rep.Bytes)
	assert.Equal(t, 2, rep.Orphans)
	assert.Equal(t, int64(7), rep.OrphanBytes)
	assert.Contains(t, bucket.deleted, "sites/site_a/objects/"+sum1)
	assert.Contains(t, bucket.deleted, "sites/site_a/objects/"+sum1+".gz", "every compressed form goes with the file")
	assert.Contains(t, bucket.deleted, "sites/site_a/objects/"+sum1+".br")
	assert.Contains(t, bucket.deleted, "sites/site_a/releases/srel_old/files/old.txt")
	assert.Contains(t, bucket.deleted, "sites/site_a/objects/"+sum2+".gz")
	assert.Contains(t, bucket.deleted, "sites/site_a/releases/srel_x/files/a.css")
	assert.NotContains(t, bucket.deleted, "sites/site_a/notes.txt", "a key outside the layout is never touched")
	assert.NotContains(t, bucket.deleted, "sites/site_a/objects/"+sum3)
	require.Len(t, store.sweeps, 1)
	assert.Equal(t, "admin@example.com", store.sweeps[0].RunBy)
	assert.Equal(t, siteconfig.ModeDelete, store.sweeps[0].Mode)
}

func TestRun_ReportDeletesNothing(t *testing.T) {
	store := &fakeStore{
		refs:    []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}},
		results: map[string]*sitestore.PruneResult{"site_a": {Releases: []int{3}, Objects: []site.File{{SHA256: sum1}}}},
	}
	bucket := &fakeBucket{objects: []s3adapter.WalkedObject{{Key: "sites/site_a/objects/" + sum2, Size: 4, LastModified: now.Add(-2 * time.Hour)}}}
	rep, err := runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeReport})
	require.NoError(t, err)
	assert.Equal(t, []bool{false}, store.applied)
	assert.Equal(t, 1, rep.Releases)
	assert.Equal(t, 1, rep.Orphans, "report counts the orphans too")
	assert.Empty(t, bucket.deleted)
	require.Len(t, rep.Sites, 1)
	assert.Equal(t, []int{3}, rep.Sites[0].Releases)
}

func TestRun_OneSiteByName(t *testing.T) {
	store := &fakeStore{refs: []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}, {ID: "site_b", Name: "beta"}}}
	rep, err := runner(store, nil).Run(context.Background(), Request{Mode: siteconfig.ModeReport, Site: "beta"})
	require.NoError(t, err)
	require.Len(t, rep.Sites, 1)
	assert.Equal(t, "beta", rep.Sites[0].Site)
	assert.Equal(t, "beta", rep.Scope)

	_, err = runner(store, nil).Run(context.Background(), Request{Mode: siteconfig.ModeReport, Site: "gamma"})
	require.ErrorIs(t, err, site.ErrNotFound)
}

func TestRun_SkippedFailedAndRefused(t *testing.T) {
	store := &fakeStore{
		refs: []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}, {ID: "site_b", Name: "beta"}, {ID: "site_c", Name: "gamma"}},
		results: map[string]*sitestore.PruneResult{
			"site_a": {Skipped: true},
		},
		failOn: "prune:site_b",
	}
	old := now.Add(-48 * time.Hour)
	objects := make([]s3adapter.WalkedObject, 0, breakerFloor+5)
	for i := range breakerFloor + 5 {
		objects = append(objects, s3adapter.WalkedObject{Key: "sites/site_c/objects/" + strings.Repeat(string(rune('a'+i%6)), 63) + string(rune('a'+i/6%6)), LastModified: old})
	}
	bucket := &fakeBucket{objects: objects}
	rep, err := runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Skipped, "a site with a job running is left for the next sweep")
	assert.Equal(t, 1, rep.Failed, "a site that failed is counted and the sweep goes on")
	assert.Equal(t, 1, rep.Refused, "a site whose objects are mostly orphans is refused")
	assert.Empty(t, bucket.deleted)
}

func TestRun_Failures(t *testing.T) {
	ctx := context.Background()
	_, err := runner(&fakeStore{}, nil).Run(ctx, Request{Mode: "sometimes"})
	require.ErrorContains(t, err, "mode")

	_, err = runner(&fakeStore{failOn: "refs"}, nil).Run(ctx, Request{Mode: siteconfig.ModeReport})
	require.ErrorIs(t, err, errDown)

	_, err = runner(&fakeStore{failOn: "record"}, nil).Run(ctx, Request{Mode: siteconfig.ModeReport})
	require.ErrorIs(t, err, errDown)

	store := &fakeStore{refs: []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}}, failOn: "stored"}
	rep, err := runner(store, &fakeBucket{}).Run(ctx, Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Zero(t, rep.Failed, "the prune stands; only the orphans went unchecked")
	require.Len(t, rep.Sites, 1)
	assert.True(t, rep.Sites[0].OrphansUnchecked, "a site whose references cannot be read is not swept for orphans")

	store = &fakeStore{refs: []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}}, failOn: "hold:site_a"}
	rep, err = runner(store, &fakeBucket{}).Run(ctx, Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Failed, "a hold that cannot be taken fails the site before anything is pruned")
	assert.Empty(t, store.applied)

	store = &fakeStore{
		refs:    []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}},
		results: map[string]*sitestore.PruneResult{"site_a": {Releases: []int{1}, Objects: []site.File{{SHA256: sum1}}}},
	}
	bucket := &fakeBucket{refuse: map[string]bool{"sites/site_a/objects/" + sum1: true}}
	rep, err = runner(store, bucket).Run(ctx, Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Failed, "a delete that fails is counted and left for a later sweep")
}

func TestRemoveOrphans_ARowRecordedDuringTheListingKeepsItsObject(t *testing.T) {
	store := &fakeStore{stored: map[string][]site.File{}}
	r := runner(store, &fakeBucket{})
	sr := &SiteReport{}
	totals := &sitestore.Sweep{}
	// Between the listing and the second read a release came to name sum1.
	store.stored["site_a"] = []site.File{{SHA256: sum1}}
	err := r.removeOrphans(context.Background(), "site_a", []s3adapter.WalkedObject{
		{Key: "sites/site_a/objects/" + sum1}, {Key: "sites/site_a/objects/" + sum2, Size: 3},
	}, pass{apply: true, site: sr, totals: totals})
	require.NoError(t, err)
	assert.Equal(t, 1, sr.Orphans)
	assert.Equal(t, int64(3), sr.OrphanBytes)
}

func TestIdentify(t *testing.T) {
	cases := map[string]string{
		"objects/" + sum1:               "d:" + sum1,
		"objects/" + sum1 + ".gz":       "d:" + sum1,
		"objects/" + sum1 + ".br":       "d:" + sum1,
		"releases/srel_1/files/a/b.css": "r:srel_1\x00a/b.css",
		"releases/srel_1/gz/index.html": "r:srel_1\x00index.html",
		"releases/srel_1/br/index.html": "r:srel_1\x00index.html",
	}
	for key, want := range cases {
		got, ok := identify(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got, key)
	}
	for _, key := range []string{"objects/short", "objects/" + strings.Repeat("A", 64), "releases/srel_1/other/a", "releases//files/a", "releases/srel_1/files/", "notes.txt"} {
		_, ok := identify(key)
		assert.False(t, ok, key)
	}
}

func TestScheduled_SweepsWhenDue(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{refs: []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}}}
	r := runner(store, nil)
	require.NoError(t, r.scheduled(ctx))
	require.Len(t, store.sweeps, 1, "no scheduled sweep yet: one is due")
	assert.Equal(t, siteconfig.ModeDelete, store.sweeps[0].Mode, "the deployment's mode")

	require.NoError(t, r.scheduled(ctx))
	assert.Len(t, store.sweeps, 1, "the last scheduled sweep is younger than the interval")

	store.sweeps = []sitestore.Sweep{{RunBy: "admin@example.com", FinishedAt: now}, {FinishedAt: now.Add(-25 * time.Hour)}}
	require.NoError(t, r.scheduled(ctx))
	assert.Len(t, store.sweeps, 3, "an administrator's sweep does not stand in for the scheduled one")

	require.ErrorIs(t, runner(&fakeStore{failOn: "sweeps"}, nil).scheduled(ctx), errDown)
}

func TestLoop_StopsWhenAsked(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	store := &fakeStore{}
	go func() {
		runner(store, nil).Loop(context.Background(), stop)
		close(done)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
	}
	assert.NotEmpty(t, bgloop.NameSiteReleaseSweep)
}

// The correctness reviewer's case: a bucket that cannot be listed leaves the
// prune's counts in the report and marks the orphans unchecked, not failed.
func TestRun_APruneCountsWhenTheOrphansCannotBeListed(t *testing.T) {
	store := &fakeStore{
		refs:    []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}},
		results: map[string]*sitestore.PruneResult{"site_a": {Releases: []int{1, 2}, Bytes: 30, Objects: []site.File{{SHA256: sum1}}}},
	}
	bucket := &fakeBucket{walkErr: errDown}
	rep, err := runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Releases)
	assert.Equal(t, 1, rep.Files)
	assert.Equal(t, int64(30), rep.Bytes)
	assert.Zero(t, rep.Failed)
	require.Len(t, rep.Sites, 1)
	assert.True(t, rep.Sites[0].OrphansUnchecked)
	assert.Contains(t, bucket.deleted, "sites/site_a/objects/"+sum1, "what the prune freed is still deleted")
	require.Len(t, store.sweeps, 1)
	assert.Equal(t, 2, store.sweeps[0].Releases, "the recorded sweep carries the prune")
}

// A publish holding a site's stored files keeps a delete sweep off it: nothing
// is pruned, nothing deleted, and the site is left for the next sweep. Report
// mode takes no hold.
func TestRun_APublishHoldingTheSiteLeavesItAlone(t *testing.T) {
	store := &fakeStore{
		refs:      []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}},
		results:   map[string]*sitestore.PruneResult{"site_a": {Releases: []int{1}, Objects: []site.File{{SHA256: sum1}}}},
		holdsLeft: map[string]int{"site_a": 0},
	}
	bucket := &fakeBucket{}
	rep, err := runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Skipped)
	assert.Empty(t, store.applied, "the site is not pruned while a publish holds it")
	assert.Empty(t, bucket.deleted)

	rep, err = runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeReport})
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Releases, "report mode decides without a hold")
	assert.Zero(t, store.holds)
}

// A publish that takes the site between the prune's deletes and the orphan
// deletes leaves the orphans for later; the prune's deletes stand.
func TestRun_APublishBetweenThePruneAndTheOrphansKeepsThem(t *testing.T) {
	store := &fakeStore{
		refs:      []sitestore.SiteRef{{ID: "site_a", Name: "alpha"}},
		results:   map[string]*sitestore.PruneResult{"site_a": {Releases: []int{1}, Objects: []site.File{{SHA256: sum1}}}},
		holdsLeft: map[string]int{"site_a": 1},
	}
	bucket := &fakeBucket{objects: []s3adapter.WalkedObject{{Key: "sites/site_a/objects/" + sum2, Size: 4, LastModified: now.Add(-2 * time.Hour)}}}
	rep, err := runner(store, bucket).Run(context.Background(), Request{Mode: siteconfig.ModeDelete})
	require.NoError(t, err)
	assert.Contains(t, bucket.deleted, "sites/site_a/objects/"+sum1)
	assert.NotContains(t, bucket.deleted, "sites/site_a/objects/"+sum2, "the orphan waits while a publish holds the site")
	require.Len(t, rep.Sites, 1)
	assert.True(t, rep.Sites[0].OrphansUnchecked)
	assert.Zero(t, rep.Orphans)
	assert.Zero(t, rep.Failed)
}

type expiredHold struct{}

func (expiredHold) Renew(context.Context) error { return sitestore.ErrHoldExpired }

// A sweep whose hold ran out stops deleting: a publish may be storing again.
func TestDeleteKeys_StopsWhenTheHoldRunsOut(t *testing.T) {
	bucket := &fakeBucket{}
	r := runner(&fakeStore{}, bucket)
	keys := make([]string, deleteBatch+3)
	for i := range keys {
		keys[i] = "sites/site_a/objects/" + strings.Repeat("a", 63) + string(rune('a'+i%6))
	}
	assert.Equal(t, len(keys), r.deleteKeys(context.Background(), keys, expiredHold{}))
	assert.Empty(t, bucket.deleted)

	assert.Zero(t, r.deleteKeys(context.Background(), keys, &sitestore.Hold{}))
	assert.Len(t, bucket.deleted, len(keys), "a live hold deletes every batch")
}
