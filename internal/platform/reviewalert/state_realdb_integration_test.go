//go:build integration

package reviewalert

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestStateStoreRealDB proves the de-duplication policy against the real
// schema. The whole policy lives in one conditional upsert, so sqlmock can
// only show that it was issued -- whether it actually suppresses the second
// alert is a question for Postgres.
func TestStateStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db, KnowledgeTarget())
	ctx := context.Background()
	now := time.Now().UTC()

	won, err := store.ClaimAlert(ctx, 24*time.Hour, now)
	require.NoError(t, err)
	assert.True(t, won, "the first crossing alerts, seeding the single state row")

	won, err = store.ClaimAlert(ctx, 24*time.Hour, now.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, won, "a queue that stays stale does not re-alert inside the cooldown")

	won, err = store.ClaimAlert(ctx, 24*time.Hour, now.Add(25*time.Hour))
	require.NoError(t, err)
	assert.True(t, won, "past the cooldown the still-stale queue alerts again")

	// Worked back under threshold, then over again inside the cooldown.
	require.NoError(t, store.Clear(ctx))
	won, err = store.ClaimAlert(ctx, 24*time.Hour, now.Add(26*time.Hour))
	require.NoError(t, err)
	assert.True(t, won, "a recovered queue that crosses again is news, not repetition")

	// Clear is idempotent and safe when nothing is outstanding.
	require.NoError(t, store.Clear(ctx))
	require.NoError(t, store.Clear(ctx))

	// The table holds exactly one row for this queue: the queue-keyed primary
	// key is what makes "one row per queue" a constraint rather than a
	// convention.
	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM review_alert_state`).Scan(&rows))
	assert.Equal(t, 1, rows)
}

// TestSettingsStoreRealDB round-trips the alert configuration through the
// platform_settings section, alongside the SMTP section rather than over it.
func TestSettingsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db, KnowledgeTarget())
	ctx := context.Background()

	_, err := store.Get(ctx)
	assert.ErrorIs(t, err, ErrNotFound, "an unwritten section reads as not found")

	in := Settings{
		Enabled: true, PendingThreshold: 25, OldestPendingDays: 14, CooldownHours: 6,
		Recipients: []string{"ops@example.com", "lead@example.com"},
	}
	require.NoError(t, store.Set(ctx, in, "admin@example.com"))

	got, err := store.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, in.Enabled, got.Enabled)
	assert.Equal(t, in.PendingThreshold, got.PendingThreshold)
	assert.Equal(t, in.OldestPendingDays, got.OldestPendingDays)
	assert.Equal(t, in.CooldownHours, got.CooldownHours)
	assert.Equal(t, in.Recipients, got.Recipients)
	assert.Equal(t, "admin@example.com", got.UpdatedBy)
	assert.False(t, got.UpdatedAt.IsZero())

	// A second write updates in place rather than conflicting on the section.
	in.CooldownHours = 12
	require.NoError(t, store.Set(ctx, in, "other@example.com"))
	got, err = store.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, 12, got.CooldownHours)
	assert.Equal(t, "other@example.com", got.UpdatedBy)
}

// TestPerQueueClaimsAreIndependentRealDB is the single-fire proof for a
// deployment watching more than one review queue (#1287). The claim is what
// makes an alert a cluster-wide singleton, and it is keyed by queue: one
// queue's cooldown must not silence another's alert, and each queue must still
// alert exactly once per window however many replicas check it. The second
// queue is synthetic because only the knowledge queue ships today; the store
// stays queue-keyed, and this pins that keying.
func TestPerQueueClaimsAreIndependentRealDB(t *testing.T) {
	db := testdb.New(t)
	knowledge := NewPostgresStore(db, KnowledgeTarget())
	other := NewPostgresStore(db, Target{
		Queue:           "other_review",
		SettingsSection: "other_review_alert",
	})
	ctx := context.Background()
	now := time.Now().UTC()

	won, err := knowledge.ClaimAlert(ctx, 24*time.Hour, now)
	require.NoError(t, err)
	assert.True(t, won)

	// The second queue crossing in the same window is news, not repetition.
	won, err = other.ClaimAlert(ctx, 24*time.Hour, now)
	require.NoError(t, err)
	assert.True(t, won, "one queue's claim must not consume another queue's window")

	// Two replicas checking the second queue inside its cooldown: one alert.
	for _, at := range []time.Time{now.Add(time.Minute), now.Add(time.Hour)} {
		won, err = other.ClaimAlert(ctx, 24*time.Hour, at)
		require.NoError(t, err)
		assert.False(t, won, "a second replica's check inside the cooldown loses the claim")
	}

	// Clearing one queue leaves the other's marker outstanding.
	require.NoError(t, other.Clear(ctx))
	won, err = knowledge.ClaimAlert(ctx, 24*time.Hour, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.False(t, won, "clearing one queue must not re-arm the knowledge queue")

	won, err = other.ClaimAlert(ctx, 24*time.Hour, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.True(t, won, "a queue worked back under threshold alerts again on the next crossing")

	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM review_alert_state`).Scan(&rows))
	assert.Equal(t, 2, rows, "one row per queue, not one row per deployment")
}
