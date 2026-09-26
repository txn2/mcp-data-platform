package retention

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portalpurge"
)

func TestDays(t *testing.T) {
	assert.Equal(t, 30, Days(0, 30), "unset takes the default")
	assert.Equal(t, 0, Days(-1, 30), "negative disables")
	assert.Equal(t, 7, Days(7, 30))
}

func TestSweepConstructorsDisabled(t *testing.T) {
	assert.Nil(t, MemoryArchived(nil, 0))
	assert.Nil(t, OrphanedProducers(nil, 0))
	assert.Nil(t, PortalDeleted(nil, 30))
	assert.Nil(t, PortalDeleted(portalpurge.NewPurger(nil, nil, ""), 0))
	assert.Len(t, SupersededGraphQL(nil), 1)
}

var (
	lockQ   = regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")
	unlockQ = regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")
)

// RunOnce takes each sweep's lock, runs it, and releases the lock; a sweep
// another replica holds is skipped; a failing sweep does not stop the next.
func TestRunOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var ran []string
	sweep := func(name string, key int64, err error) Sweep {
		return Sweep{Name: name, LockKey: key, Run: func(context.Context) (int64, error) {
			ran = append(ran, name)
			return 3, err
		}}
	}
	mock.ExpectQuery(lockQ).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
	mock.ExpectExec(unlockQ).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(lockQ).WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(false))
	mock.ExpectQuery(lockQ).WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
	mock.ExpectExec(unlockQ).WithArgs(int64(3)).WillReturnError(errors.New("gone"))
	mock.ExpectQuery(lockQ).WithArgs(int64(4)).WillReturnError(errors.New("down"))

	New(db, 0, sweep("first", 1, nil), sweep("held", 2, nil), sweep("failing", 3, errors.New("boom")), sweep("unlockable", 4, nil)).
		RunOnce(context.Background())

	assert.Equal(t, []string{"first", "failing"}, ran)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRunOnceStopsOnACanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	New(db, 0, Sweep{Run: func(context.Context) (int64, error) { ran = true; return 0, nil }}).RunOnce(ctx)
	assert.False(t, ran)
}

// Start runs the first pass at once; Close stops the loop and is safe to call
// twice and on a loop that never started.
func TestStartAndClose(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(lockQ).WillReturnRows(sqlmock.NewRows([]string{"l"}).AddRow(true))
	mock.ExpectExec(unlockQ).WillReturnResult(sqlmock.NewResult(0, 0))

	done := make(chan struct{})
	loop := New(db, time.Hour, Sweep{Name: "s", LockKey: 9, Run: func(context.Context) (int64, error) {
		close(done)
		return 0, nil
	}})
	loop.Start()
	loop.Start() // a second call does nothing
	<-done
	require.NoError(t, loop.Close())
	require.NoError(t, loop.Close())
	assert.NoError(t, mock.ExpectationsWereMet())

	require.NoError(t, New(nil, 0).Close())
	New(nil, 0, Sweep{}).Start() // no database: nothing starts
}

// The sweeps run their statement with the cutoff and report the rows removed.
func TestSweepStatements(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta(archivedMemoryQuery)).WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 2))
	n, err := MemoryArchived(db, 90)[0].Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	mock.ExpectExec(regexp.QuoteMeta(orphanedProducersQuery)).WithArgs(sqlmock.AnyArg()).WillReturnError(errors.New("down"))
	_, err = OrphanedProducers(db, 90)[0].Run(context.Background())
	require.Error(t, err)

	mock.ExpectExec(regexp.QuoteMeta(supersededGraphQLQuery)).WillReturnResult(sqlmock.NewResult(0, 5))
	n, err = SupersededGraphQL(db)[0].Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(5), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The portal sweep reports every row the purge removed.
func TestPortalDeletedSweep(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	empty := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}) }
	mock.ExpectQuery("FROM portal_assets a").WillReturnRows(empty())
	mock.ExpectQuery("FROM portal_collections").WillReturnRows(empty())
	mock.ExpectExec("DELETE FROM portal_threads").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM portal_knowledge_pages").WillReturnResult(sqlmock.NewResult(0, 2))

	n, err := PortalDeleted(portalpurge.NewPurger(db, nil, "b"), 30)[0].Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Assemble builds the sweeps the configuration turns on.
func TestAssemble(t *testing.T) {
	assert.Nil(t, Assemble(Config{}), "no database, no loop")
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	all := Assemble(Config{DB: db, Portal: true, DeletedDays: 30, ArchivedDays: 90, ProducerDays: 90})
	assert.Len(t, all.sweeps, 4)
	assert.Equal(t, DefaultInterval, all.interval)

	portalOff := Assemble(Config{DB: db, ArchivedDays: 90, Every: time.Minute})
	assert.Len(t, portalOff.sweeps, 2, "memory and GraphQL; the portal is off and producer retention is 0")
	assert.Equal(t, time.Minute, portalOff.interval)
}
