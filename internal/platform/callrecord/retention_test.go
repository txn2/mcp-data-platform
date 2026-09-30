package callrecord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestRetentionDaysTakesTheDefaultWhenUnset(t *testing.T) {
	t.Parallel()

	// Zero or negative takes the default, matching every other retention this
	// platform configures.
	assert.Equal(t, DefaultRetentionDays, RetentionDays(0))
	assert.Equal(t, DefaultRetentionDays, RetentionDays(-30))
	assert.Equal(t, 30, RetentionDays(30))
}

func TestSweepKeepsEveryKindOfEvidence(t *testing.T) {
	t.Parallel()

	// The sweep is what stops the catalog growing without bound, so what it
	// refuses to delete is the part worth pinning: a record something was
	// built from, one that was published, one that was declined, and one
	// another session re-ran.
	for _, clause := range []string{
		"promoted_urn = ''",
		"rejected_at IS NULL",
		"FROM call_record_reuse u WHERE u.call_record_id = r.id",
		"jsonb_build_object('sources'",
		"FROM portal_assets a",
	} {
		assert.Contains(t, sweepQuery, clause)
	}
	assert.Contains(t, sweepQuery, "r.created_at < $2", "and only records past the window")
	assert.Contains(t, sweepQuery, "lower(r.persona) = ANY($3)",
		"or a record an excluded persona made, whatever its age")
	assert.Contains(t, sweepQuery, "r.user_id LIKE $4",
		"or a record a managed script run made, whatever its age (#1624)")
}

func TestSweepMatchesAnExcludedPersonaTheWayTheRuleDoes(t *testing.T) {
	t.Parallel()

	// The SQL folds the persona to lower case because NewExclusion
	// folds the configured name the same way. Two spellings of one fold would
	// be two rules, and the half that never matched would be the sweep.
	assert.Contains(t, sweepQuery, "lower(r.persona)")
	assert.Equal(t, []string{"ingest-service"},
		NewExclusion([]string{" Ingest-Service "}).Personas())
}

// sweepRows is one batch's answer: how many records it removed.
func sweepRows(removed int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"count"}).AddRow(removed)
}

func TestCleanupBindsTheExcludedPersonas(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	store := NewPostgresStore(db, Config{
		ExcludePersonas: []string{"Ingest-Service", "etl"},
		ServiceAccounts: fakeAccounts{"crm-sync": true, "etl": true},
	})

	// The configured names and the personas marked as service accounts reach
	// the statement as one list, normalized, sorted and without repeats, which
	// is what makes the delete the same on every replica. The batch bound and
	// the index kind whose units go with the records are bound too.
	mock.ExpectQuery("WITH doomed AS").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), pq.Array([]string{"crm-sync", "etl", "ingest-service"}),
			script.PrincipalPrefix+"%", sweepBatchSize, IndexSourceKind).
		WillReturnRows(sweepRows(9))

	removed, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(9), removed)
}

func TestCleanupBindsAnEmptyArrayWhenNothingIsExcluded(t *testing.T) {
	store, mock := newMock(t)

	// A deployment that declared nothing binds an empty array rather than a
	// NULL: `= ANY('{}')` is false for every row, so the age half of the sweep
	// is the whole rule and the catalog behaves exactly as it did before.
	mock.ExpectQuery("WITH doomed AS").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), pq.Array([]string{}), script.PrincipalPrefix+"%",
			sweepBatchSize, IndexSourceKind).
		WillReturnRows(sweepRows(1))

	_, err := store.Cleanup(context.Background())
	require.NoError(t, err)
}

func TestCleanupReportsWhatItRemoved(t *testing.T) {
	store, mock := newMock(t)
	store.retentionDays = 30

	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(4))

	removed, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4), removed)
}

func TestCleanupDeletesInBoundedBatchesUntilOneComesBackShort(t *testing.T) {
	store, mock := newMock(t)

	// A backlog larger than one batch is removed by several statements, each
	// its own transaction, and the sweep stops at the first batch that removed
	// fewer than the bound: there is nothing left for another.
	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(sweepBatchSize))
	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(sweepBatchSize))
	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(12))

	removed, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2*sweepBatchSize+12), removed)
}

func TestCleanupKeepsWhatCommittedWhenALaterBatchFails(t *testing.T) {
	store, mock := newMock(t)

	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(sweepBatchSize))
	mock.ExpectQuery("WITH doomed AS").WillReturnError(errors.New("db down"))

	removed, err := store.Cleanup(context.Background())
	require.Error(t, err)
	assert.Equal(t, int64(sweepBatchSize), removed, "the committed batch is reported with the failure")
}

func TestCleanupStopsQuietlyWhenCanceled(t *testing.T) {
	store, _ := newMock(t)

	// A sweep canceled before its next batch is a clean partial pass, not a
	// failure: what committed stands and the next sweep resumes.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	removed, err := store.Cleanup(ctx)
	require.NoError(t, err)
	assert.Zero(t, removed)
}

func TestSweepRemovesTheIndexUnitsOfTheRecordsItDeletes(t *testing.T) {
	t.Parallel()

	// index_jobs has no foreign key to call_records, so a unit outlives its
	// record unless the sweep deletes it in the same statement.
	assert.Contains(t, sweepQuery, "DELETE FROM index_jobs j USING gone g")
	assert.Contains(t, sweepQuery, "j.source_kind = $6 AND j.source_id = g.id::text")
	assert.Contains(t, sweepQuery, "LIMIT $5", "one statement removes at most one batch")
}

func TestCleanupReportsAFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectQuery("WITH doomed AS").WillReturnError(errors.New("db down"))

	_, err := store.Cleanup(context.Background())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "sweeping"), "the error says what was being done: %v", err)
}

func TestSweeperSweepsOnceAtStartup(t *testing.T) {
	store, mock := newMock(t)

	// The first sweep is at start, not an interval later: a deployment that
	// has just excluded a persona restarts to apply it, and the rows that
	// persona wrote go at that restart.
	mock.ExpectQuery("pg_try_advisory_lock").
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(3))
	mock.ExpectExec("pg_advisory_unlock").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// The sweep is stopped only once it has happened: closing first would
	// cancel the context the sweep runs under and prove nothing.
	store.StartCleanupRoutine(time.Hour)
	t.Cleanup(func() { _ = store.Close() })
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil },
		5*time.Second, 5*time.Millisecond,
		"the sweeper did not take the lock, delete, and release it at startup")
}

func TestSweeperStartsOnceAndStopsCleanly(t *testing.T) {
	store, mock := newMock(t)

	// A long interval means no tick beyond the startup sweep fires during the
	// test: what is asserted here is the lifecycle. The startup sweep finds no
	// lock and gives up, which is the same path a replica losing the race takes.
	mock.ExpectQuery("pg_try_advisory_lock").
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

	store.StartCleanupRoutine(time.Hour)
	store.StartCleanupRoutine(time.Hour) // second call must not start a second goroutine
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil },
		5*time.Second, 5*time.Millisecond, "the startup sweep did not try the lock")
	require.NoError(t, store.Close())
	// Close is idempotent: shutdown paths run it whether or not it started.
	require.NoError(t, store.Close())
}

func TestSweeperNeedsADatabase(t *testing.T) {
	t.Parallel()

	// A deployment with no database keeps no catalog and sweeps nothing.
	var store *PostgresStore
	store.StartCleanupRoutine(time.Hour)
	require.NoError(t, store.Close())
}

func TestNewPostgresStoreResolvesRetention(t *testing.T) {
	store, _ := newMock(t)
	assert.Equal(t, DefaultRetentionDays, store.retentionDays,
		"a store built with no retention stated takes the default")
}

func TestSweeperSweepsWhenAsked(t *testing.T) {
	store, mock := newMock(t)

	// The startup sweep finds nothing. The asked-for sweep is the second one,
	// and it happens without the hour-long interval passing: that is what lets
	// a persona saved as a service account lose its records promptly.
	for _, removed := range []int{0, 7} {
		mock.ExpectQuery("pg_try_advisory_lock").
			WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
		mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(removed))
		mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 1))
	}

	store.StartCleanupRoutine(time.Hour)
	t.Cleanup(func() { _ = store.Close() })
	store.RequestSweep()
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil },
		5*time.Second, 5*time.Millisecond, "the asked-for sweep did not run")
}

func TestRequestSweepNeverBlocks(t *testing.T) {
	t.Parallel()

	// Nothing is draining the request, so a second ask must fold into the
	// first rather than wait; a nil catalog asks nothing.
	store := NewPostgresStore(nil, Config{})
	store.RequestSweep()
	store.RequestSweep()
	assert.Len(t, store.kick, 1)

	var none *PostgresStore
	none.RequestSweep()
}

// An asked-for sweep that finds another replica holding the lock asks again
// until it gets it, so the persona just marked is swept by a sweep that read it.
func TestSweeperRetriesAnAskedForSweepThatLostTheLock(t *testing.T) {
	store, mock := newMock(t)
	store.kickRetry = time.Millisecond

	// The startup sweep and the asked-for one lose the race; the retry wins.
	for range 2 {
		mock.ExpectQuery("pg_try_advisory_lock").
			WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))
	}
	mock.ExpectQuery("pg_try_advisory_lock").
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery("WITH doomed AS").WillReturnRows(sweepRows(4))
	mock.ExpectExec("pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 1))

	store.StartCleanupRoutine(time.Hour)
	t.Cleanup(func() { _ = store.Close() })
	// The sweeper takes the startup sweep and the request in that order,
	// whenever the request arrives.
	store.RequestSweep()
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil },
		5*time.Second, 5*time.Millisecond, "the asked-for sweep was not retried")
}
