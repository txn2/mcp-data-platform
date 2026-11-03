package postgres

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

var sweepLockQ = regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")

func TestSweepRunsUnderThePlatformLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	old := "mcpc_" + itoa(time.Now().Add(-3*time.Hour).Unix()) + "_0123456789abcdef"
	srv := newServer(true, old)
	tk, _ := testServerToolkit(srv, baseConfig())

	mock.ExpectQuery(sweepLockQ).WithArgs(TestSweepLockKey).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(false))
	assert.ErrorIs(t, tk.sweepOnceLocked(context.Background(), db, time.Hour), bgloop.ErrSkipped, "another replica sweeps")
	assert.Equal(t, []string{old}, srv.has(clonePrefix))

	mock.ExpectQuery(sweepLockQ).WithArgs(TestSweepLockKey).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WithArgs(TestSweepLockKey).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, tk.sweepOnceLocked(context.Background(), db, time.Hour))
	assert.Empty(t, srv.has(clonePrefix))
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectQuery(sweepLockQ).WillReturnError(context.DeadlineExceeded)
	assert.ErrorContains(t, tk.sweepOnceLocked(context.Background(), db, time.Hour), "taking the test sweep lock")
}

func TestStartTestSweepStartsOnceAndStopsWithTheToolkit(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	tk.StartTestSweep(nil)
	assert.Nil(t, tk.sweepStop, "no platform database, no sweep")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(sweepLockQ).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(false))
	tk.runTestSweep(db, time.Hour, time.Hour)
	tk.sweepMu.Lock()
	first := tk.sweepStop
	tk.sweepMu.Unlock()
	require.NotNil(t, first)
	tk.runTestSweep(db, time.Hour, time.Hour)
	tk.sweepMu.Lock()
	assert.Equal(t, first, tk.sweepStop, "a second start is the same sweep")
	tk.sweepMu.Unlock()
	require.NoError(t, tk.Close())
	tk.sweepMu.Lock()
	assert.Nil(t, tk.sweepStop)
	tk.sweepMu.Unlock()
	tk.stopSweep()
}

func TestTestSweepFromEnvReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvTestSweepInterval, "5s")
	t.Setenv(EnvTestMaxAge, "soon")
	interval, maxAge := testSweepFromEnv()
	assert.Equal(t, 5*time.Second, interval)
	assert.Equal(t, DefaultTestMaxAge, maxAge, "an unreadable value takes the default")
	t.Setenv(EnvTestSweepInterval, "")
	interval, _ = testSweepFromEnv()
	assert.Equal(t, DefaultTestSweepInterval, interval)
	t.Setenv(EnvTestSweepInterval, "-1s")
	interval, _ = testSweepFromEnv()
	assert.Equal(t, DefaultTestSweepInterval, interval)
}
