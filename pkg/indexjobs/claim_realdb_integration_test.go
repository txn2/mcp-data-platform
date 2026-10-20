//go:build integration

package indexjobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestClaim_RealDB_AWriteGoesAheadOfTheBackfill covers #2218: a job a write
// asked for is claimed before the reconciler's backfill, however long the
// backfill has waited, and the backfill is claimed once no write waits.
func TestClaim_RealDB_AWriteGoesAheadOfTheBackfill(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	earlier := time.Now().UTC().Add(-time.Hour)
	_, err := db.ExecContext(ctx, `INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status, next_run_at) VALUES
		('calls', 'gap_1', 'reconciler', 'pending', $1),
		('calls', 'gap_2', 'reconciler', 'pending', $1),
		('portal-assets', 'saved', 'write', 'pending', NOW()),
		('tools', 'retried', 'manual_retry', 'pending', NOW())`, earlier)
	require.NoError(t, err)

	store := NewPostgresStore(db)
	var order []string
	for range 4 {
		job, err := store.Claim(ctx, "w")
		require.NoError(t, err)
		order = append(order, job.SourceID)
	}
	assert.ElementsMatch(t, []string{"saved", "retried"}, order[:2], "what a write or an operator asked for goes first")
	assert.ElementsMatch(t, []string{"gap_1", "gap_2"}, order[2:])

	_, err = store.Claim(ctx, "w")
	assert.ErrorIs(t, err, ErrNoJob)
}

// TestClaim_RealDB_KindsTakeTurns covers #2218: a kind with one waiting job is
// claimed after at most one job of a kind with a long backlog, not after the
// whole backlog, and each kind's own jobs keep their order.
func TestClaim_RealDB_KindsTakeTurns(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	earlier := time.Now().UTC().Add(-time.Hour)
	_, err := db.ExecContext(ctx, `INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status, next_run_at) VALUES
		('calls', 'call_1', 'reconciler', 'pending', $1),
		('calls', 'call_2', 'reconciler', 'pending', $1 + interval '1 second'),
		('calls', 'call_3', 'reconciler', 'pending', $1 + interval '2 seconds'),
		('catalog-datasets', 'orders', 'reconciler', 'pending', NOW() - interval '1 second')`, earlier)
	require.NoError(t, err)

	store := NewPostgresStore(db)
	var order []string
	for range 4 {
		job, err := store.Claim(ctx, "w")
		require.NoError(t, err)
		order = append(order, job.SourceID)
	}
	assert.Equal(t, []string{"call_1", "orders", "call_2", "call_3"}, order)
}

// TestClaim_RealDB_AHeldCandidateIsSkipped: a claim made while another
// transaction holds the first-ranked job takes the next one, rather than
// finding nothing.
func TestClaim_RealDB_AHeldCandidateIsSkipped(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status, next_run_at) VALUES
		('calls', 'first', 'write', 'pending', NOW() - interval '2 seconds'),
		('calls', 'second', 'write', 'pending', NOW() - interval '1 second')`)
	require.NoError(t, err)

	holder, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback() })
	var held string
	require.NoError(t, holder.QueryRowContext(ctx,
		`SELECT source_id FROM index_jobs WHERE source_id = 'first' FOR UPDATE`).Scan(&held))

	job, err := NewPostgresStore(db).Claim(ctx, "w")
	require.NoError(t, err)
	assert.Equal(t, "second", job.SourceID)
}
