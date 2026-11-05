package capacity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// reclaimFixture is a portal bucket holding, an hour after the listing began:
// a referenced head with a tile beside it, an orphan a day and a half old, an
// orphan an hour old (past the counting grace, short of the reclaim age), an
// operator's basemap upload, and an orphan whose row lands while the listing
// runs.
type reclaimFixture struct {
	walker fakeWalker
	now    time.Time
}

const (
	headKey      = "artifacts/u/a1/content.html"
	headTileKey  = "artifacts/u/a1/.thumbnail.png"
	oldOrphanKey = "artifacts/u/gone/content.html"
	youngKey     = "artifacts/u/young/content.html"
	lateRowKey   = "artifacts/u/late/content.html"
	uploadKey    = "maps/uploads/region.pmtiles"
)

func newReclaimFixture() reclaimFixture {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	old := now.Add(-36 * time.Hour)
	return reclaimFixture{now: now, walker: fakeWalker{
		putErr: errors.New("read-only"),
		objects: map[string][]s3adapter.WalkedObject{
			"p": {
				{Key: headKey, Size: 10, LastModified: old},
				{Key: headTileKey, Size: 4, LastModified: old},
				{Key: oldOrphanKey, Size: 7, LastModified: old},
				{Key: youngKey, Size: 5, LastModified: now.Add(-time.Hour)},
				{Key: lateRowKey, Size: 3, LastModified: old},
			},
			"r": {{Key: uploadKey, Size: 99, LastModified: old}},
		},
	}}
}

func (f reclaimFixture) scanner(t *testing.T) (*Scanner, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newMock(t)
	s := &Scanner{
		DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": f.walker, "r": f.walker},
		OrphanGrace: DefaultOrphanGrace, ReclaimAge: DefaultReclaimAge, now: func() time.Time { return f.now },
	}
	return s, mock
}

// expectRefs expects one read of both buckets' references.
func expectRefs(mock sqlmock.Sqlmock, portalKeys ...string) {
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnRows(keyRows(portalKeys...))
	mock.ExpectQuery("SELECT s3_key FROM resources").WillReturnRows(keyRows())
}

func keys(objs []s3adapter.WalkedObject) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.Key)
	}
	return out
}

// TestReclaim_DeletesOnlyAnOldOrphanNoRowNames: of everything the listing
// met, only the orphan older than the reclaim age and still unreferenced
// after the listing is deleted, counted per purpose, and taken off what the
// reconcile reports as orphaned.
func TestReclaim_DeletesOnlyAnOldOrphanNoRowNames(t *testing.T) {
	f := newReclaimFixture()
	s, mock := f.scanner(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(usageRows())
	expectRefs(mock, headKey)
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.Orphaned[observability.StoragePurposePortalAssets], "old, young and late-row orphans are counted")

	// The late row has landed by the time the reclaim reads references again.
	expectRefs(mock, headKey, lateRowKey)
	deleted := s.Reclaim(context.Background(), &res, ReclaimDelete)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, map[string]int64{observability.StoragePurposePortalAssets: 1}, res.Reclaimed)
	assert.ElementsMatch(t, []string{headKey, headTileKey, youngKey, lateRowKey}, keys(f.walker.objects["p"]))
	assert.Equal(t, []string{uploadKey}, keys(f.walker.objects["r"]), "an operator's upload is never deleted")
	assert.Equal(t, usageDelta{objects: 4, bytes: 18}, res.Usage[usageKey{"p", observability.StoragePurposePortalAssets}],
		"the deleted object's bytes are off the listing's usage; its object count is taken off by the delete's own observation")
}

// TestReclaim_ReportDeletesNothing logs what a delete pass would remove and
// removes nothing.
func TestReclaim_ReportDeletesNothing(t *testing.T) {
	f := newReclaimFixture()
	s, mock := f.scanner(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(usageRows())
	expectRefs(mock, headKey)
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	expectRefs(mock, headKey)
	logs := captureLogs(t)

	assert.Zero(t, s.Reclaim(context.Background(), &res, ReclaimReport))
	assert.Empty(t, res.Reclaimed)
	assert.Len(t, f.walker.objects["p"], 5)
	assert.Contains(t, logs.String(), "eligible_portal_assets=2")
	assert.NotContains(t, logs.String(), "artifacts/", "no key reaches a log line")
}

// TestReclaim_OffCollectsNothing: with reclaim off the scanner is given no
// reclaim age, collects no candidate, and deletes nothing.
func TestReclaim_OffCollectsNothing(t *testing.T) {
	f := newReclaimFixture()
	s, mock := f.scanner(t)
	s.ReclaimAge = reclaimAge(Config{Reclaim: ReclaimOff, ReclaimAge: time.Hour})
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(usageRows())
	expectRefs(mock, headKey)
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, res.candidates.items)
	assert.Zero(t, s.Reclaim(context.Background(), &res, ReclaimOff))
	require.NoError(t, mock.ExpectationsWereMet(), "no second read of the references")
}

// TestReclaim_RefusesAPurposeMostlyOrphaned: a listing in which most of a
// purpose's objects have no row (a deployment reading a bucket that is not its
// own) deletes none of them, and says so.
func TestReclaim_RefusesAPurposeMostlyOrphaned(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	objs := make([]s3adapter.WalkedObject, 0, breakerFloor+1)
	for i := range breakerFloor + 1 {
		objs = append(objs, s3adapter.WalkedObject{Key: fmt.Sprintf("artifacts/u/a%d/content.html", i), Size: 1, LastModified: now.Add(-48 * time.Hour)})
	}
	walker := fakeWalker{putErr: errors.New("read-only"), objects: map[string][]s3adapter.WalkedObject{"p": objs}}
	db, mock := newMock(t)
	s := &Scanner{
		DB: db, Layout: Layout{PortalBucket: "p", PortalPrefix: "artifacts"}, Walkers: map[string]Walker{"p": walker},
		OrphanGrace: DefaultOrphanGrace, ReclaimAge: DefaultReclaimAge, now: func() time.Time { return now },
	}
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(usageRows())
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnRows(keyRows())
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnRows(keyRows())
	logs := captureLogs(t)

	assert.Zero(t, s.Reclaim(context.Background(), &res, ReclaimDelete))
	assert.Len(t, walker.objects["p"], breakerFloor+1)
	assert.Contains(t, logs.String(), "orphan reclaim refused")
	assert.Contains(t, logs.String(), "purpose=portal_assets")
}

// TestReclaim_AFailedDeleteIsNotCounted: an object the store would not delete
// is neither counted reclaimed nor taken off the usage.
func TestReclaim_AFailedDeleteIsNotCounted(t *testing.T) {
	f := newReclaimFixture()
	f.walker.deleteErr = errors.New("access denied")
	s, mock := f.scanner(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(usageRows())
	expectRefs(mock, headKey)
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	expectRefs(mock, headKey, lateRowKey)

	assert.Zero(t, s.Reclaim(context.Background(), &res, ReclaimDelete))
	assert.Empty(t, res.Reclaimed)
	assert.Equal(t, int64(25), res.Usage[usageKey{"p", observability.StoragePurposePortalAssets}].bytes)
}

// TestReclaim_ReferenceReadFailureDeletesNothing: when the references cannot
// be read again, nothing is deleted on the strength of the listing's read.
func TestReclaim_ReferenceReadFailureDeletesNothing(t *testing.T) {
	f := newReclaimFixture()
	s, mock := f.scanner(t)
	mock.ExpectQuery("FROM storage_usage").WillReturnRows(usageRows())
	expectRefs(mock, headKey)
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnError(errors.New("down"))

	assert.Zero(t, s.Reclaim(context.Background(), &res, ReclaimDelete))
	assert.Len(t, f.walker.objects["p"], 5)
}

// TestReclaimCandidates_Bounded keeps at most maxReclaimPerPass and counts
// every one.
func TestReclaimCandidates_Bounded(t *testing.T) {
	c := newReclaimCandidates()
	for range maxReclaimPerPass + 5 {
		c.add(candidate{purpose: "p"})
	}
	assert.Len(t, c.items, maxReclaimPerPass)
	assert.Equal(t, int64(maxReclaimPerPass+5), c.count["p"])
}

// TestIsReclaimable: an object of unknown age, or none when the cutoff is
// unset, is never a candidate.
func TestIsReclaimable(t *testing.T) {
	cut := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	assert.True(t, isReclaimable(s3adapter.WalkedObject{LastModified: cut.Add(-time.Second)}, cut))
	assert.False(t, isReclaimable(s3adapter.WalkedObject{LastModified: cut}, cut))
	assert.False(t, isReclaimable(s3adapter.WalkedObject{}, cut))
	assert.False(t, isReclaimable(s3adapter.WalkedObject{LastModified: cut.Add(-time.Hour)}, time.Time{}))
}

// TestStillReferenced: a bucket whose references were not read is treated as
// referenced.
func TestStillReferenced(t *testing.T) {
	assert.True(t, stillReferenced(map[string]bucketRefs{}, candidate{bucket: "p", key: "artifacts/x"}))
}

// TestScanner_ReclaimCutoff never reaches past the counting grace.
func TestScanner_ReclaimCutoff(t *testing.T) {
	start := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	s := &Scanner{OrphanGrace: 2 * time.Hour, ReclaimAge: time.Hour}
	assert.Equal(t, start.Add(-2*time.Hour), s.reclaimCutoff(start))
	s.ReclaimAge = 0
	assert.True(t, s.reclaimCutoff(start).IsZero())
}

// captureLogs sends the default logger to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
