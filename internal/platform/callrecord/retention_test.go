package callrecord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}

func TestCleanupReportsWhatItRemoved(t *testing.T) {
	store, mock := newMock(t)
	store.retentionDays = 30

	mock.ExpectExec("DELETE FROM call_records").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 4))

	removed, err := store.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4), removed)
}

func TestCleanupReportsAFailure(t *testing.T) {
	store, mock := newMock(t)
	mock.ExpectExec("DELETE FROM call_records").WillReturnError(errors.New("db down"))

	_, err := store.Cleanup(context.Background())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "sweeping"), "the error says what was being done: %v", err)
}

func TestSweeperStartsOnceAndStopsCleanly(t *testing.T) {
	store, _ := newMock(t)

	// A long interval means no tick fires during the test: what is asserted
	// here is the lifecycle, not the sweep.
	store.StartCleanupRoutine(time.Hour)
	store.StartCleanupRoutine(time.Hour) // second call must not start a second goroutine
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
