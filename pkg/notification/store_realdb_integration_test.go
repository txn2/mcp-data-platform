//go:build integration

package notification

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// passthrough is a no-op StringEncryptor for realdb round-trips.
type passthrough struct{}

func (passthrough) Encrypt(s string) (string, error) { return s, nil }
func (passthrough) Decrypt(s string) (string, error) { return s, nil }

// TestSettingsStoreRealDB round-trips SMTP settings against the real schema.
func TestSettingsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresSettingsStore(db, passthrough{})
	ctx := context.Background()

	_, err := store.GetSMTP(ctx)
	require.ErrorIs(t, err, ErrNotFound)

	in := SMTPSettings{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		Username: "mailer", Password: "secret", From: "p@example.com",
		FromName: "Platform", TLSMode: TLSModeStartTLS,
	}
	require.NoError(t, store.SetSMTP(ctx, in, "admin@example.com"))

	got, err := store.GetSMTP(ctx)
	require.NoError(t, err)
	require.Equal(t, "smtp.example.com", got.Host)
	require.Equal(t, "secret", got.Password)
	require.Equal(t, "admin@example.com", got.UpdatedBy)
	require.False(t, got.UpdatedAt.IsZero())

	// Empty password on update keeps the stored one.
	in.Password = ""
	in.Host = "smtp2.example.com"
	require.NoError(t, store.SetSMTP(ctx, in, "admin2@example.com"))
	got, err = store.GetSMTP(ctx)
	require.NoError(t, err)
	require.Equal(t, "smtp2.example.com", got.Host)
	require.Equal(t, "secret", got.Password)
}

// TestPrefsStoreRealDB round-trips preferences against the real schema,
// including the CHECK constraint on mode.
func TestPrefsStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresPrefsStore(db)
	ctx := context.Background()

	p, err := store.Get(ctx, "new@example.com")
	require.NoError(t, err)
	require.Equal(t, ModeImmediate, p.Mode)

	mode := ModeDaily
	off := false
	p, err = store.Set(ctx, "new@example.com", PrefsUpdate{Mode: &mode, CommentsEnabled: &off})
	require.NoError(t, err)
	require.Equal(t, ModeDaily, p.Mode)
	require.False(t, p.CommentsEnabled)
	require.True(t, p.SharesEnabled)

	p, err = store.Get(ctx, "new@example.com")
	require.NoError(t, err)
	require.Equal(t, ModeDaily, p.Mode)
	require.False(t, p.CommentsEnabled)
}

// TestQueueStoreRealDB exercises the full queue lifecycle against real
// Postgres: enqueue, immediate claim (FOR UPDATE SKIP LOCKED subquery),
// digest batch claim, retry scheduling, permanent failure, and the
// expired-lease reclaim clause.
func TestQueueStoreRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresQueueStore(db)
	ctx := context.Background()
	lease := time.Minute

	// Immediate row lifecycle: enqueue -> claim -> sent.
	require.NoError(t, store.Enqueue(ctx, Notification{
		Recipient: "a@example.com", Category: CategoryShare,
		Payload: Payload{Kind: KindAsset, ItemTitle: "Report", Actor: "o@example.com"},
	}))
	claimed, err := store.ClaimImmediate(ctx, lease)
	require.NoError(t, err)
	require.Equal(t, "a@example.com", claimed.Recipient)
	require.Equal(t, StatusSending, claimed.Status)
	require.Equal(t, 1, claimed.Attempts)
	require.Equal(t, "Report", claimed.Payload.ItemTitle)

	// A second claim finds nothing (the row is leased).
	_, err = store.ClaimImmediate(ctx, lease)
	require.ErrorIs(t, err, ErrNoWork)

	require.NoError(t, store.MarkSent(ctx, []int64{claimed.ID}))
	var status string
	var sentAt *time.Time
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status, sent_at FROM notifications WHERE id = $1`, claimed.ID).
		Scan(&status, &sentAt))
	require.Equal(t, StatusSent, status)
	require.NotNil(t, sentAt)

	// Digest batch: two due rows for one recipient claim together; a not-yet-due
	// row for another recipient stays untouched.
	past := time.Now().UTC().Add(-time.Minute)
	future := time.Now().UTC().Add(time.Hour)
	for _, title := range []string{"One", "Two"} {
		require.NoError(t, store.Enqueue(ctx, Notification{
			Recipient: "d@example.com", Category: CategoryShare, Digest: true,
			ScheduledFor: past, Payload: Payload{Kind: KindAsset, ItemTitle: title},
		}))
	}
	require.NoError(t, store.Enqueue(ctx, Notification{
		Recipient: "later@example.com", Category: CategoryShare, Digest: true,
		ScheduledFor: future, Payload: Payload{Kind: KindAsset, ItemTitle: "Later"},
	}))

	batch, err := store.ClaimDigest(ctx, lease)
	require.NoError(t, err)
	require.Len(t, batch, 2)
	for _, n := range batch {
		require.Equal(t, "d@example.com", n.Recipient)
	}
	_, err = store.ClaimDigest(ctx, lease)
	require.ErrorIs(t, err, ErrNoWork, "future-scheduled digest must not be claimable")

	// Retry returns the batch to pending with a future schedule.
	require.NoError(t, store.Retry(ctx, []int64{batch[0].ID, batch[1].ID}, "smtp down", time.Hour))
	_, err = store.ClaimDigest(ctx, lease)
	require.ErrorIs(t, err, ErrNoWork, "retried rows are scheduled in the future")

	// Fail marks rows permanently failed with the error recorded.
	require.NoError(t, store.Fail(ctx, []int64{batch[0].ID}, "gave up"))
	var lastErr string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status, last_error FROM notifications WHERE id = $1`, batch[0].ID).
		Scan(&status, &lastErr))
	require.Equal(t, StatusFailed, status)
	require.Equal(t, "gave up", lastErr)

	// Expired-lease reclaim: a sending row whose locked_until passed is
	// claimable again.
	require.NoError(t, store.Enqueue(ctx, Notification{
		Recipient: "crash@example.com", Category: CategoryShare,
		Payload: Payload{Kind: KindAsset, ItemTitle: "Orphan"},
	}))
	orphan, err := store.ClaimImmediate(ctx, lease)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`UPDATE notifications SET locked_until = NOW() - INTERVAL '1 second' WHERE id = $1`, orphan.ID)
	require.NoError(t, err)
	reclaimed, err := store.ClaimImmediate(ctx, lease)
	require.NoError(t, err)
	require.Equal(t, orphan.ID, reclaimed.ID)
	require.Equal(t, 2, reclaimed.Attempts)
}

// TestQueuePurgeRealDB proves the retention DELETE against real Postgres:
// old resolved and stale unresolved rows go; fresh rows stay.
func TestQueuePurgeRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresQueueStore(db)
	ctx := context.Background()

	seed := func(status string, age time.Duration) int64 {
		var id int64
		require.NoError(t, db.QueryRowContext(ctx,
			`INSERT INTO notifications (recipient, category, payload, status, created_at)
			 VALUES ('p@example.com', 'share', '{}', $1, NOW() - ($2 || ' seconds')::INTERVAL)
			 RETURNING id`, status, int(age.Seconds())).Scan(&id))
		return id
	}
	oldSent := seed(StatusSent, 40*24*time.Hour)
	stalePending := seed(StatusPending, 10*24*time.Hour)
	freshSent := seed(StatusSent, time.Hour)
	freshPending := seed(StatusPending, time.Hour)

	purged, err := store.PurgeOld(ctx, DefaultResolvedRetention, DefaultPendingTTL)
	require.NoError(t, err)
	require.Equal(t, int64(2), purged)

	var remaining int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE id = ANY($1)`,
		pq.Array([]int64{oldSent, stalePending})).Scan(&remaining))
	require.Zero(t, remaining, "aged rows must be purged")
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE id = ANY($1)`,
		pq.Array([]int64{freshSent, freshPending})).Scan(&remaining))
	require.Equal(t, 2, remaining, "fresh rows must remain")
}
