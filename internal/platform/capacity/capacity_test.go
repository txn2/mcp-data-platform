package capacity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

func expectRead(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("FROM storage_usage").
		WillReturnRows(sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects"}).AddRow("r", "resources", 10, 1))
	mock.ExpectQuery("FROM storage_reconciles").WillReturnRows(sqlmock.NewRows([]string{"purpose", "orphaned", "dangling"}))
	mock.ExpectQuery("FROM storage_scans").WillReturnRows(sqlmock.NewRows([]string{"d", "o"}).AddRow(1.0, 1))
}

// TestStart_ReportsAndStops starts the service on a metrics handle, waits
// for its first table sample and usage read to reach the scrape, and stops
// it, which removes the usage recorder again.
func TestStart_ReportsAndStops(t *testing.T) {
	db, mock := newMock(t)
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("pg_partition_tree").WillReturnRows(sqlmock.NewRows([]string{"name", "bytes", "rows"}).AddRow("audit_logs", 1, 1))
	mock.ExpectQuery("amname = 'hnsw'").WillReturnRows(sqlmock.NewRows([]string{"name", "bytes"}))
	mock.ExpectQuery("datfrozenxid").WillReturnRows(sqlmock.NewRows([]string{"age"}).AddRow(5))
	expectRead(mock)

	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	s := Start(m, Sources{DB: db, Layout: testLayout}, Config{Backend: observability.StorageBackendGCS})
	require.Eventually(t, func() bool {
		got := s.Sample(context.Background())
		return got.Database.Known && got.Storage.ScanKnown
	}, 5*time.Second, 5*time.Millisecond)
	got := s.Sample(context.Background())
	assert.Empty(t, got.Storage.Backends, "no bucket is listed, so no backend is claimed")
	assert.Empty(t, got.Storage.Usage, "a row for a bucket the deployment does not configure is not reported")

	require.NoError(t, s.Close(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
	_, err = objectobs.DoObject(context.Background(), "x", objectobs.OpPut, objectobs.Object{Bucket: "r", Key: "resources/a/b/c/d"}, func(context.Context) (int64, error) { return 1, nil })
	require.NoError(t, err)
	assert.Empty(t, s.recorder.take(), "after Close the recorder no longer hears of writes")
}

func TestStart_NothingToReport(t *testing.T) {
	assert.NoError(t, Start(nil, Sources{}, Config{}).Close(context.Background()))
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	assert.NoError(t, Start(m, Sources{}, Config{}).Close(context.Background()))
	var s *Service
	assert.NoError(t, s.Close(context.Background()))
}

// TestService_RecorderHearsDoObject proves the wiring from an object write
// to the usage row: a put through objectobs.DoObject reaches the installed
// recorder sorted by its key.
func TestService_RecorderHearsDoObject(t *testing.T) {
	s := newService(Sources{Layout: testLayout}, Config{})
	objectobs.SetUsageRecorder(s.recorder)
	t.Cleanup(func() { objectobs.SetUsageRecorder(nil) })
	_, err := objectobs.DoObject(context.Background(), "x", objectobs.OpPut, objectobs.Object{Bucket: "r", Key: "resources/a/b/c/f.csv"},
		func(context.Context) (int64, error) { return 64, nil })
	require.NoError(t, err)
	_, err = objectobs.DoObject(context.Background(), "x", objectobs.OpDelete, objectobs.Object{Bucket: "p", Key: "artifacts/u/a/content.html"},
		func(context.Context) (int64, error) { return 0, nil })
	require.NoError(t, err)
	_, err = objectobs.DoObject(context.Background(), "x", objectobs.OpPut, objectobs.Object{Bucket: "r", Key: "resources/a/b/c/g.csv"},
		func(context.Context) (int64, error) { return 0, errors.New("refused") })
	require.Error(t, err)
	assert.Equal(t, map[usageKey]usageDelta{
		{"r", observability.StoragePurposeResources}:    {objects: 1, bytes: 64},
		{"p", observability.StoragePurposePortalAssets}: {objects: -1},
	}, s.recorder.take(), "a failed put is not counted")
}

func TestService_SampleCarriesBudgetsAndBackends(t *testing.T) {
	budgets := []observability.BucketBudget{{Bucket: "r", Bytes: 1}}
	s := newService(Sources{Walkers: map[string]Walker{"r": fakeWalker{}}}, Config{Budgets: budgets})
	s.backends = map[string]string{"r": observability.StorageBackendSeaweedFS, "p": observability.StorageBackendOther, "q": observability.StorageBackendSeaweedFS}
	got := s.Sample(context.Background())
	assert.Equal(t, budgets, got.Storage.Budgets)
	assert.Equal(t, []string{observability.StorageBackendOther, observability.StorageBackendSeaweedFS}, got.Storage.Backends,
		"each store once, sorted")
}

// TestService_DetectsEachBucketsStore asks each bucket's own endpoint, once per
// endpoint: a deployment may keep its portal and its managed resources on two
// stores (#1899).
func TestService_DetectsEachBucketsStore(t *testing.T) {
	seaweed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "SeaweedFS 30GB 3.88")
	}))
	defer seaweed.Close()
	asked := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("Server", "SomeStore")
	}))
	defer elsewhere.Close()
	s := newService(Sources{
		Walkers:   map[string]Walker{"portal": fakeWalker{}, "resources": fakeWalker{}, "maps": fakeWalker{}},
		Endpoints: map[string]string{"portal": seaweed.URL, "resources": elsewhere.URL, "maps": elsewhere.URL},
	}, Config{})
	assert.Equal(t, map[string]string{
		"portal": observability.StorageBackendSeaweedFS, "resources": observability.StorageBackendOther, "maps": observability.StorageBackendOther,
	}, s.detectBackends(context.Background()))
	assert.Equal(t, 1, asked, "a store two buckets share is asked once")

	forced := newService(Sources{Walkers: map[string]Walker{"portal": fakeWalker{}}, Endpoints: map[string]string{"portal": seaweed.URL}},
		Config{Backend: observability.StorageBackendGCS})
	assert.Equal(t, map[string]string{"portal": observability.StorageBackendGCS}, forced.detectBackends(context.Background()))
}

func TestService_SampleTablesFailureKeepsLastReading(t *testing.T) {
	db, mock := newMock(t)
	s := newService(Sources{DB: db}, Config{})
	s.database = observability.DatabaseCapacity{Known: true, TransactionIDAge: 9}
	mock.ExpectQuery("pg_partition_tree").WillReturnError(errors.New("down"))
	require.Error(t, s.sampleTables(context.Background()))
	assert.Equal(t, int64(9), s.Sample(context.Background()).Database.TransactionIDAge)
}

// TestService_FlushDetectsBackendOnce names the backend on the first flush
// from configuration, and reads the rows back under it.
func TestService_FlushDetectsBackendOnce(t *testing.T) {
	db, mock := newMock(t)
	s := newService(Sources{DB: db, Walkers: map[string]Walker{"r": fakeWalker{}}, Endpoints: map[string]string{"r": "http://never-asked.invalid"}},
		Config{Backend: observability.StorageBackendGCS})
	expectRead(mock)
	require.NoError(t, s.flush(context.Background()))
	assert.Equal(t, observability.StorageBackendGCS, s.Sample(context.Background()).Storage.Usage[0].Backend)

	mock.ExpectQuery("FROM storage_usage").WillReturnError(errors.New("down"))
	require.Error(t, s.flush(context.Background()))
	assert.Len(t, s.Sample(context.Background()).Storage.Usage, 1, "a failed read keeps the last one")
}

func TestService_ScanSkips(t *testing.T) {
	s := newService(Sources{}, Config{})
	require.ErrorIs(t, s.scan(context.Background()), bgloop.ErrSkipped, "nothing to list")

	db, mock := newMock(t)
	s = newService(Sources{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}, Config{})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).WithArgs(scanLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(false))
	require.ErrorIs(t, s.scan(context.Background()), bgloop.ErrSkipped, "another replica holds the lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestService_ScanUnderLock lists, saves and reads back while holding the
// lock, and releases it.
func TestService_ScanUnderLock(t *testing.T) {
	db, mock := newMock(t)
	walker := fakeWalker{objects: map[string][]s3adapter.WalkedObject{
		"p": {{Key: "artifacts/u/a/content.html", Size: 3, LastModified: time.Now().Add(-time.Hour)}},
	}}
	s := newService(Sources{DB: db, Layout: Layout{PortalBucket: "p", PortalPrefix: "artifacts"}, Walkers: map[string]Walker{"p": walker}},
		Config{Backend: observability.StorageBackendS3})
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(recentQuery)).WithArgs(DefaultScanInterval.Seconds() * recentFraction).
		WillReturnRows(sqlmock.NewRows([]string{"recent"}).AddRow(false))
	expectSnapshot(mock)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnRows(keyRows("artifacts/u/a/content.html"))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM storage_usage").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("FOR UPDATE").WillReturnRows(usageRows())
	mock.ExpectExec("INSERT INTO storage_usage").WithArgs("p", observability.StoragePurposePortalAssets, int64(3), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	for range reconciled {
		mock.ExpectExec("INSERT INTO storage_reconciles").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec("INSERT INTO storage_scans").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	expectRead(mock)
	require.NoError(t, s.scan(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestService_ScanSkipsARecentListing: a replica that starts, or whose timer
// fires, within most of an interval of another's listing does not list again.
func TestService_ScanSkipsARecentListing(t *testing.T) {
	db, mock := newMock(t)
	s := newService(Sources{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}, Config{ScanInterval: time.Hour})
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(recentQuery)).WithArgs(3240.0).WillReturnRows(sqlmock.NewRows([]string{"recent"}).AddRow(true))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, s.scan(context.Background()), bgloop.ErrSkipped,
		"a skipped listing is not a success, so the loop's last success means one finished")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestService_RetriesAStoreThatDidNotAnswer asks again, after backendRetry,
// for a bucket whose store read as other, and not before.
func TestService_RetriesAStoreThatDidNotAnswer(t *testing.T) {
	answer := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", answer)
	}))
	defer srv.Close()
	now := time.Now()
	s := newService(Sources{Walkers: map[string]Walker{"r": fakeWalker{}}, Endpoints: map[string]string{"r": srv.URL}}, Config{})
	s.now = func() time.Time { return now }
	require.True(t, s.shouldDetect(), "never asked")
	s.backends, s.detectedAt = s.detectBackends(context.Background()), now
	assert.Equal(t, observability.StorageBackendOther, s.backends["r"])
	assert.False(t, s.shouldDetect(), "not again within the retry")
	now = now.Add(backendRetry)
	assert.True(t, s.shouldDetect(), "asked again once the retry passes")
	answer = "SeaweedFS 3.88"
	s.backends = s.detectBackends(context.Background())
	assert.Equal(t, observability.StorageBackendSeaweedFS, s.backends["r"])
	assert.False(t, s.shouldDetect(), "a store that named itself is not asked again")

	forced := newService(Sources{Walkers: map[string]Walker{"r": fakeWalker{}}, Endpoints: map[string]string{"r": srv.URL}}, Config{Backend: observability.StorageBackendOther})
	forced.backends, forced.detectedAt = map[string]string{"r": observability.StorageBackendOther}, time.Time{}
	assert.False(t, forced.shouldDetect(), "a configured kind is never asked")
	assert.False(t, (&Service{backends: map[string]string{"r": "other"}, src: Sources{Endpoints: map[string]string{"r": ""}}}).shouldDetect(),
		"an empty endpoint is AWS and is not asked")
}

func TestService_ScanFailures(t *testing.T) {
	db, mock := newMock(t)
	s := newService(Sources{DB: db, Layout: testLayout, Walkers: map[string]Walker{"p": fakeWalker{}}}, Config{})
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.scan(context.Background()), "advisory lock")

	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(recentQuery)).WillReturnRows(sqlmock.NewRows([]string{"recent"}).AddRow(false))
	expectSnapshot(mock)
	mock.ExpectQuery("SELECT s3_key FROM portal_assets").WillReturnError(errors.New("down"))
	mock.ExpectExec("pg_advisory_unlock").WillReturnError(errors.New("gone"))
	require.ErrorContains(t, s.scan(context.Background()), "reading object references")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(recentQuery)).WillReturnError(errors.New("down"))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorContains(t, s.scan(context.Background()), "reading the last listing")
	require.NoError(t, mock.ExpectationsWereMet())

	s.recorder.RecordObject("p", "artifacts/u/a/content.html", 1, 1)
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(recentQuery)).WillReturnRows(sqlmock.NewRows([]string{"recent"}).AddRow(false))
	mock.ExpectExec("INSERT INTO storage_usage").WillReturnError(errors.New("down"))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorContains(t, s.scan(context.Background()), "adding to storage usage", "this replica's writes go in before the listing reads the rows")
	require.NoError(t, mock.ExpectationsWereMet())

	_ = db.Close()
	require.ErrorContains(t, s.scan(context.Background()), "acquiring a connection")
}
