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
	store := NewPostgresStore(db)
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

	// The table holds exactly one row: the CHECK (id) primary key is what
	// makes "one row per deployment" a constraint rather than a convention.
	var rows int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM knowledge_review_alert_state`).Scan(&rows))
	assert.Equal(t, 1, rows)
}

// TestSettingsStoreRealDB round-trips the alert configuration through the
// platform_settings section, alongside the SMTP section rather than over it.
func TestSettingsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
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
