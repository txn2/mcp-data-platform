package capacity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// fakeWalker lists a fixed set of objects per bucket, honoring the prefix.
// A put is stored with LastModified at storeNow (the store's own clock), or
// refused with putErr.
type fakeWalker struct {
	objects  map[string][]s3adapter.WalkedObject
	err      error
	putErr   error
	storeNow time.Time
}

func (f fakeWalker) PutObject(_ context.Context, bucket, key string, _ []byte, _ string) error {
	if f.putErr != nil {
		return f.putErr
	}
	if f.objects != nil {
		f.objects[bucket] = append(f.objects[bucket], s3adapter.WalkedObject{Key: key, LastModified: f.storeNow})
	}
	return nil
}

func (f fakeWalker) Walk(_ context.Context, bucket, prefix string, fn func(s3adapter.WalkedObject) error) error {
	if f.err != nil {
		return f.err
	}
	for _, o := range f.objects[bucket] {
		if strings.HasPrefix(o.Key, prefix) {
			if err := fn(o); err != nil {
				return err
			}
		}
	}
	return nil
}

func newMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func keyRows(keys ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"key"})
	for _, k := range keys {
		rows.AddRow(k)
	}
	return rows
}

var testLayout = Layout{PortalBucket: "p", PortalPrefix: "artifacts", ResourceBucket: "r"}

func usageRows(rows ...[4]any) *sqlmock.Rows {
	out := sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects"})
	for _, r := range rows {
		out.AddRow(r[0], r[1], r[2], r[3])
	}
	return out
}

func expectSnapshot(mock sqlmock.Sqlmock, rows ...[4]any) {
	mock.ExpectQuery(regexp.QuoteMeta(snapshotQuery)).WillReturnRows(usageRows(rows...))
}

// TestScan_CountsUsageOrphansAndDangling lists two buckets against the rows
// that reference them: one object with no row is an orphan, one row with no
// object is dangling, and everything else is counted in its purpose.
func TestScan_CountsUsageOrphansAndDangling(t *testing.T) {
	db, mock := newMock(t)
	old := time.Now().Add(-time.Hour)
	expectSnapshot(mock, [4]any{"r", "resources", 900, 1})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s3_key FROM portal_assets")).WithArgs("p").
		WillReturnRows(keyRows("artifacts/u/a/content.html", "artifacts/collections/c/thumbnail.png", "artifacts/u/gone/content.csv"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s3_key FROM resources")).WithArgs("r").
		WillReturnRows(keyRows("resources/global/global/r1/f.pdf", "elsewhere/x"))
	walker := fakeWalker{objects: map[string][]s3adapter.WalkedObject{
		"p": {
			{Key: "artifacts/u/a/content.html", Size: 100, LastModified: old},
			{Key: "artifacts/collections/c/thumbnail.png", Size: 10, LastModified: old},
			{Key: "artifacts/collections/c/thumbnail_dark.png", Size: 10, LastModified: old},
			{Key: "artifacts/u/orphan/content.html", Size: 5, LastModified: old},
			{Key: "artifacts/u/a/.thumbnail.png", Size: 1, LastModified: old},
			{Key: "artifacts/u/deleted/.thumbnail_dark.png", Size: 1, LastModified: old},
			{Key: "artifacts/u/fresh/content.html", Size: 5, LastModified: time.Now().Add(-time.Minute)},
			{Key: "artifacts/u/after/content.html", Size: 7, LastModified: time.Now().Add(time.Hour)},
		},
		"r": {
			{Key: "resources/global/global/r1/f.pdf", Size: 1000, LastModified: old},
			{Key: "maps/uploads/region.pmtiles", Size: 7, LastModified: old},
			{Key: "webhooks/s/raw/x.jsonl.gz", Size: 3, LastModified: old},
		},
	}}
	s := &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": walker, "r": walker}, OrphanGrace: 30 * time.Second}

	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, int64(11), res.Objects, "every listed object is in the scan's own count")
	assert.Equal(t, usageDelta{objects: 3, bytes: 110}, res.Usage[usageKey{"p", observability.StoragePurposePortalAssets}],
		"an object modified after the listing began is left to the write that made it")
	assert.Equal(t, usageDelta{objects: 1, bytes: 900}, res.Snapshot[usageKey{"r", observability.StoragePurposeResources}])
	assert.Equal(t, usageDelta{objects: 4, bytes: 22}, res.Usage[usageKey{"p", observability.StoragePurposeThumbnails}])
	assert.Equal(t, usageDelta{objects: 1, bytes: 1000}, res.Usage[usageKey{"r", observability.StoragePurposeResources}])
	assert.Equal(t, usageDelta{objects: 1, bytes: 7}, res.Usage[usageKey{"r", observability.StoragePurposeMaps}])
	assert.Equal(t, usageDelta{objects: 1, bytes: 3}, res.Usage[usageKey{"r", observability.StoragePurposeWebhooks}])

	assert.Equal(t, map[string]int64{observability.StoragePurposePortalAssets: 2, observability.StoragePurposeThumbnails: 1}, res.Orphaned,
		"the object written within the grace, the dark mosaic, the tile beside referenced content and the hand-placed map are not orphans; "+
			"an old object and a tile no row references are, and so is the one written after the listing began; webhook segments are not reconciled")
	assert.Equal(t, map[string]int64{observability.StoragePurposePortalAssets: 1}, res.Dangling,
		"a reference outside the platform prefixes is counted under no purpose")
	assert.ElementsMatch(t, []string{"p", "r"}, res.Buckets)
}

// TestScan_BucketWithNoWalker shows a bucket nothing can list contributes
// neither usage nor dangling counts.
func TestScan_BucketWithNoWalker(t *testing.T) {
	db, mock := newMock(t)
	expectSnapshot(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT s3_key FROM portal_assets")).WillReturnRows(keyRows("artifacts/u/a/content.html"))
	s := &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"p"}, res.Buckets)
	assert.Equal(t, int64(1), res.Dangling[observability.StoragePurposePortalAssets])
	assert.NotContains(t, res.Buckets, "r")
}

func TestScan_Failures(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(snapshotQuery)).WillReturnError(errors.New("down"))
	s := &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}
	_, err := s.Scan(context.Background())
	require.ErrorContains(t, err, "reading storage usage")

	db, mock = newMock(t)
	expectSnapshot(mock)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnError(errors.New("down"))
	s = &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}
	_, err = s.Scan(context.Background())
	require.ErrorContains(t, err, "reading object references")

	db, mock = newMock(t)
	expectSnapshot(mock)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnRows(keyRows())
	s = &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{err: errors.New("denied")}}}
	_, err = s.Scan(context.Background())
	require.ErrorContains(t, err, "listing artifacts/")

	db, mock = newMock(t)
	expectSnapshot(mock)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnRows(sqlmock.NewRows([]string{"a", "b"}).AddRow("x", "y"))
	s = &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}
	_, err = s.Scan(context.Background())
	require.ErrorContains(t, err, "reading object references")

	db, mock = newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(snapshotQuery)).WillReturnRows(sqlmock.NewRows([]string{"bucket"}).AddRow("short"))
	s = &Scanner{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}
	_, err = s.Scan(context.Background())
	require.ErrorContains(t, err, "reading storage usage")
}

// TestScanResult_Save sets each row to the listing's count plus what the row
// gained while the listing ran, drops the rows of buckets no longer listed,
// and records every reconciled purpose and the scan.
func TestScanResult_Save(t *testing.T) {
	db, mock := newMock(t)
	assets := usageKey{"p", observability.StoragePurposePortalAssets}
	exports := usageKey{"p", observability.StoragePurposeExports}
	res := ScanResult{
		Usage:    map[usageKey]usageDelta{assets: {objects: 2, bytes: 30}},
		Snapshot: map[usageKey]usageDelta{assets: {objects: 5, bytes: 100}, exports: {objects: 1, bytes: 9}},
		Orphaned: map[string]int64{observability.StoragePurposePortalAssets: 1},
		Dangling: map[string]int64{observability.StoragePurposeResources: 4},
		Objects:  2, Duration: 1500 * time.Millisecond, Buckets: []string{"p"},
	}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(dropUnlistedQuery)).WillReturnResult(sqlmock.NewResult(0, 2))
	// While the listing ran: one more asset written (6, 140) and the export
	// deleted (0, 9: a delete frees no bytes on the row).
	mock.ExpectQuery(regexp.QuoteMeta(lockUsageQuery)).
		WillReturnRows(usageRows([4]any{"p", "portal_assets", 140, 6}, [4]any{"p", "exports", 9, 0}))
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("INSERT INTO storage_usage").WithArgs("p", "portal_assets", int64(70), int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO storage_usage").WithArgs("p", "exports", int64(0), int64(0)).WillReturnResult(sqlmock.NewResult(0, 1))
	for _, p := range reconciled {
		mock.ExpectExec("INSERT INTO storage_reconciles").WithArgs(p, res.Orphaned[p], res.Dangling[p]).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("INSERT INTO storage_scans").WithArgs(1.5, int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, res.Save(context.Background(), db))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScanResult_Totals(t *testing.T) {
	k := usageKey{"r", "resources"}
	res := ScanResult{Usage: map[usageKey]usageDelta{k: {objects: 1, bytes: 10}}, Snapshot: map[usageKey]usageDelta{k: {objects: 9, bytes: 90}}}
	assert.Equal(t, usageDelta{}, res.totals(map[usageKey]usageDelta{k: {objects: 2, bytes: 20}})[k], "never below zero")
	fresh := usageKey{"r", "maps"}
	assert.Equal(t, usageDelta{objects: 1, bytes: 5}, res.totals(map[usageKey]usageDelta{fresh: {objects: 1, bytes: 5}})[fresh],
		"a row the listing never read is all gain")
}

func TestScanResult_SaveFailures(t *testing.T) {
	res := ScanResult{Usage: map[usageKey]usageDelta{{"p", "resources"}: {objects: 1, bytes: 1}}, Buckets: []string{"p"}}
	steps := []func(m sqlmock.Sqlmock) bool{
		func(m sqlmock.Sqlmock) bool { m.ExpectBegin().WillReturnError(errors.New("boom")); return false },
		func(m sqlmock.Sqlmock) bool {
			m.ExpectExec("DELETE FROM storage_usage").WillReturnError(errors.New("boom"))
			return false
		},
		func(m sqlmock.Sqlmock) bool {
			m.ExpectQuery("FOR UPDATE").WillReturnError(errors.New("boom"))
			return false
		},
		func(m sqlmock.Sqlmock) bool {
			m.ExpectExec("INSERT INTO storage_usage").WillReturnError(errors.New("boom"))
			return false
		},
		func(m sqlmock.Sqlmock) bool {
			m.ExpectExec("INSERT INTO storage_reconciles").WillReturnError(errors.New("boom"))
			return false
		},
		func(m sqlmock.Sqlmock) bool {
			m.ExpectExec("INSERT INTO storage_scans").WillReturnError(errors.New("boom"))
			return false
		},
		func(m sqlmock.Sqlmock) bool { m.ExpectCommit().WillReturnError(errors.New("boom")); return false },
	}
	ok := []func(m sqlmock.Sqlmock){
		func(m sqlmock.Sqlmock) { m.ExpectBegin() },
		func(m sqlmock.Sqlmock) {
			m.ExpectExec("DELETE FROM storage_usage").WillReturnResult(sqlmock.NewResult(0, 0))
		},
		func(m sqlmock.Sqlmock) { m.ExpectQuery("FOR UPDATE").WillReturnRows(usageRows()) },
		func(m sqlmock.Sqlmock) {
			m.ExpectExec("INSERT INTO storage_usage").WillReturnResult(sqlmock.NewResult(0, 1))
		},
		func(m sqlmock.Sqlmock) {
			for range reconciled {
				m.ExpectExec("INSERT INTO storage_reconciles").WillReturnResult(sqlmock.NewResult(0, 1))
			}
		},
		func(m sqlmock.Sqlmock) {
			m.ExpectExec("INSERT INTO storage_scans").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	}
	for fail := range steps {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			db, mock := newMock(t)
			for i := range fail {
				ok[i](mock)
			}
			steps[fail](mock)
			assert.Error(t, res.Save(context.Background(), db))
		})
	}
}

// TestScan_TimesTheListingOnTheStoresClock: a store whose clock runs an hour
// ahead of this process still has an object written before the listing began
// counted, and one written after it left to its write. Compared with this
// process's clock, the first would be skipped and counted nowhere.
func TestScan_TimesTheListingOnTheStoresClock(t *testing.T) {
	db, mock := newMock(t)
	expectSnapshot(mock)
	mock.ExpectQuery("SELECT s3_key FROM resources").WillReturnRows(keyRows())
	ahead := time.Now().Add(time.Hour)
	walker := fakeWalker{storeNow: ahead, objects: map[string][]s3adapter.WalkedObject{"r": {
		{Key: "resources/global/global/a/before.csv", Size: 4, LastModified: ahead.Add(-time.Minute)},
		{Key: "resources/global/global/b/after.csv", Size: 8, LastModified: ahead.Add(time.Minute)},
	}}}
	s := &Scanner{DB: db, Layout: Layout{ResourceBucket: "r"}, Walkers: map[string]Walker{"r": walker}}
	res, err := s.Scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, usageDelta{objects: 1, bytes: 4}, res.Usage[usageKey{"r", observability.StoragePurposeResources}])
	assert.Equal(t, int64(2), res.Objects, "the marker is not a platform object")
}

func TestMarkStart(t *testing.T) {
	local := time.Now()
	refused := fakeWalker{putErr: errors.New("read only")}
	assert.Equal(t, local, markStart(context.Background(), refused, "r", local), "a refused marker falls back to this process's clock")
	store := local.Add(-time.Minute)
	ok := fakeWalker{storeNow: store, objects: map[string][]s3adapter.WalkedObject{}}
	assert.Equal(t, store, markStart(context.Background(), ok, "r", local))
	_, platform := Layout{PortalBucket: "r"}.Classify("r", markerKey)
	assert.False(t, platform, "even a portal that owns its whole bucket does not count the marker")
}
