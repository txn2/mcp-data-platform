package catalogscan

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// signalSource reports each time a scan asks it for connections, which is the
// edge a test waits on instead of the clock.
type signalSource struct{ asked chan struct{} }

func (signalSource) Kind() string { return KindTrino }
func (s signalSource) Connections() []Connection {
	s.asked <- struct{}{}
	return nil
}

func (signalSource) Datasets(context.Context, Connection, Exclusions, func(catalog.DatasetInput) error) error {
	return nil
}
func (signalSource) Types() []catalog.DatasetType { return nil }

func newLoopMock(t *testing.T, set Settings, src Source) (*Loop, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	settings := fakeSettings{set: set}
	var sources []Source
	if src != nil {
		sources = append(sources, src)
	}
	scanner := New(&fakeWriter{}, settings, &fakeRecorder{}, sources...)
	return NewLoop(scanner, settings, db), mock
}

func expectLock(mock sqlmock.Sqlmock, granted bool) {
	mock.ExpectQuery("pg_try_advisory_lock").WithArgs(LockKey).
		WillReturnRows(sqlmock.NewRows([]string{"got"}).AddRow(granted))
}

func expectUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectExec("pg_advisory_unlock").WithArgs(LockKey).WillReturnResult(sqlmock.NewResult(0, 0))
}

// expectStart expects a scan's start and, once it has run, the check for a
// request made during it, which finds none.
func expectStart(mock sqlmock.Sqlmock) {
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, started_at").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(time.Now(), nil))
}

func dueRows(last, requested any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"last", "requested"}).AddRow(last, requested)
}

func TestLockedScanSkipsWhenAnotherReplicaHoldsTheLock(t *testing.T) {
	l, mock := newLoopMock(t, Defaults(), nil)
	expectLock(mock, false)
	ran, err := l.lockedScan(context.Background())
	require.ErrorIs(t, err, bgloop.ErrSkipped)
	assert.False(t, ran)
	expectLock(mock, false)
	assert.ErrorIs(t, l.runOnce(context.Background()), bgloop.ErrSkipped, "counted as skipped, not failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockedScanRunsOnlyWhenDue(t *testing.T) {
	ctx := context.Background()
	l, mock := newLoopMock(t, Defaults(), nil)
	now := time.Now()
	cases := []struct {
		name      string
		last      any
		requested any
		want      bool
	}{
		{"started within the interval, nothing requested", now, nil, false},
		{"started within the interval, requested before it", now, now.Add(-time.Minute), false},
		{"requested after the last scan started", now.Add(-time.Minute), now, true},
		{"the interval has passed", now.Add(-7 * time.Hour), nil, true},
		{"never scanned", nil, nil, true},
	}
	for _, tc := range cases {
		expectLock(mock, true)
		mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(tc.last, tc.requested))
		if tc.want {
			expectStart(mock)
		}
		expectUnlock(mock)
		ran, err := l.lockedScan(ctx)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, ran, tc.name)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockedScanFailures(t *testing.T) {
	ctx := context.Background()
	l, mock := newLoopMock(t, Defaults(), nil)
	mock.ExpectQuery("pg_try_advisory_lock").WillReturnError(errBoom)
	_, err := l.lockedScan(ctx)
	assert.ErrorIs(t, err, errBoom)

	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnError(errBoom)
	mock.ExpectExec("pg_advisory_unlock").WillReturnError(errBoom)
	_, err = l.lockedScan(ctx)
	assert.ErrorIs(t, err, errBoom, "an unlock failure is logged; the read failure is returned")

	bad, mock2 := newLoopMock(t, Settings{}, nil)
	bad.settings = fakeSettings{err: errBoom}
	expectLock(mock2, true)
	expectUnlock(mock2)
	_, err = bad.lockedScan(ctx)
	assert.ErrorIs(t, err, errBoom)

	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(sqlmock.NewRows([]string{"started_at", "requested_at"}))
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, started_at").WillReturnResult(sqlmock.NewResult(0, 1))
	expectUnlock(mock)
	l.scanner.settings = fakeSettings{err: errBoom}
	assert.ErrorIs(t, l.runOnce(ctx), errBoom, "a failed scan fails the iteration")

	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(nil, nil))
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, started_at").WillReturnError(errBoom)
	expectUnlock(mock)
	_, err = l.lockedScan(ctx)
	assert.ErrorIs(t, err, errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScanNowRecordsTheRequest(t *testing.T) {
	l, mock := newLoopMock(t, Defaults(), nil)
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, requested_at").WithArgs("admin@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, l.ScanNow(context.Background(), "admin@example.com"))
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, requested_at").WillReturnError(errBoom)
	assert.ErrorIs(t, l.ScanNow(context.Background(), ""), errBoom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The loop scans at start and again on a request. Each scan is waited for
// through AfterScan, which runs once its lock is released, so the request is
// made with no scan in progress and every read of catalog_scan_state,
// including the re-check before each lock is released, meets one expectation
// in order (#2283).
func TestLoopScansAtStartAndOnRequest(t *testing.T) {
	asked := make(chan struct{}, 2)
	l, mock := newLoopMock(t, Defaults(), signalSource{asked: asked})
	l.poll = time.Hour
	finished := make(chan struct{}, 2)
	l.AfterScan(func(context.Context) { finished <- struct{}{} })
	now := time.Now()

	// The first check at start: never scanned, so due.
	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(nil, nil))
	expectStart(mock)
	expectUnlock(mock)
	// The request: recorded, then found due on this replica's own check.
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, requested_at").WillReturnResult(sqlmock.NewResult(0, 1))
	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(now.Add(-time.Minute), now))
	expectStart(mock)
	expectUnlock(mock)

	l.Start(context.Background())
	l.Start(context.Background())
	<-finished
	require.NoError(t, l.ScanNow(context.Background(), "a"))
	<-finished
	l.Stop()
	l.Stop()
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Len(t, asked, 2, "each scan read its sources")

	var nilLoop *Loop
	nilLoop.Start(context.Background())
	nilLoop.Stop()
}

// What runs after a scan runs after each scan this replica finishes, and not
// when none was due or another replica holds the lock.
func TestAfterScanRunsAfterAFinishedScan(t *testing.T) {
	ctx := context.Background()
	l, mock := newLoopMock(t, Defaults(), nil)
	after := 0
	l.AfterScan(func(context.Context) { after++ })

	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(nil, nil))
	expectStart(mock)
	expectUnlock(mock)
	require.NoError(t, l.runOnce(ctx))
	assert.Equal(t, 1, after)

	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(time.Now(), nil))
	expectUnlock(mock)
	require.NoError(t, l.runOnce(ctx))
	expectLock(mock, false)
	assert.ErrorIs(t, l.runOnce(ctx), bgloop.ErrSkipped)
	assert.Equal(t, 1, after, "no scan, nothing after it")
	require.NoError(t, mock.ExpectationsWereMet())

	var none *Loop
	none.AfterScan(func(context.Context) {})
}

// A request made while this replica scans is served before the lock is
// released, rather than at the next poll (#2218).
func TestLockedScanServesARequestMadeDuringTheScan(t *testing.T) {
	l, mock := newLoopMock(t, Defaults(), nil)
	now := time.Now()
	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(nil, nil))
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, started_at").WillReturnResult(sqlmock.NewResult(0, 1))
	// The request arrived after the first pass started.
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(now.Add(-time.Minute), now))
	expectStart(mock)
	expectUnlock(mock)

	ran, err := l.lockedScan(context.Background())
	require.NoError(t, err)
	assert.True(t, ran)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A failure on a later pass is returned and the scans already run still
// count.
func TestLockedScanReportsALaterPassFailing(t *testing.T) {
	l, mock := newLoopMock(t, Defaults(), nil)
	now := time.Now()
	expectLock(mock, true)
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(nil, nil))
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, started_at").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("FROM catalog_scan_state").WillReturnRows(dueRows(now.Add(-time.Minute), now))
	mock.ExpectExec("INSERT INTO catalog_scan_state \\(id, started_at").WillReturnError(errBoom)
	expectUnlock(mock)

	ran, err := l.lockedScan(context.Background())
	require.ErrorIs(t, err, errBoom)
	assert.True(t, ran)
	require.NoError(t, mock.ExpectationsWereMet())
}
