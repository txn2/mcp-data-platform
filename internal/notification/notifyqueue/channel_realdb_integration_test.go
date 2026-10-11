//go:build integration

package notifyqueue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyprefs"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// monitorChannel is a chat channel with a short window and the default cap.
func monitorChannel() notification.Channel {
	return notification.Channel{
		Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
		Mode: notification.ChannelModeImmediate, Connection: "mm", Target: "T",
		RepeatAfter: time.Hour,
	}
}

func keyedSend(ch notification.Channel, key string) notification.ChannelSend {
	return notification.ChannelSend{Channel: ch, Document: notification.Document{Title: "Feed stale", Key: key}}
}

func channelRows(t *testing.T, ctx context.Context, store *PostgresStore, channel string) int {
	t.Helper()
	var n int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE channel = $1`, channel).Scan(&n))
	return n
}

// TestChannelKey_ConcurrentSendsOfOneKeyDeliverOnce_RealDB is the cross-replica
// guarantee: senders racing on one key leave exactly one queued row, and every
// other send is counted on the key.
func TestChannelKey_ConcurrentSendsOfOneKeyDeliverOnce_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
	t.Cleanup(e.Close)

	const senders = 8
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		queued     int
		suppressed int
	)
	for range senders {
		wg.Go(func() {
			// Separate enqueuers stand in for separate replicas: nothing in
			// process is shared but the database.
			r := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), NewPostgresStore(db), 13)
			defer r.Close()
			res, err := r.NotifyChannel(ctx, keyedSend(monitorChannel(), "feed:orders"))
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			queued += res.Queued
			if res.Suppressed {
				suppressed++
			}
		})
	}
	wg.Wait()
	require.Equal(t, 1, queued)
	require.Equal(t, senders-1, suppressed)
	require.Equal(t, 1, channelRows(t, ctx, store, "ops"))

	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT suppressed FROM notification_channel_keys WHERE channel = 'ops' AND key = 'feed:orders'`).Scan(&count))
	require.Equal(t, senders-1, count)
}

// TestChannelKey_ASendAfterTheWindowCarriesTheSuppressedCount_RealDB: the
// window ends, the next send goes through stating what was held back since
// when, and the count starts again.
func TestChannelKey_ASendAfterTheWindowCarriesTheSuppressedCount_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
	t.Cleanup(e.Close)
	ch := monitorChannel()

	for i := range 3 {
		res, err := e.NotifyChannel(ctx, keyedSend(ch, "k"))
		require.NoError(t, err)
		require.Equal(t, i > 0, res.Suppressed, "send %d", i)
	}
	// End the window by moving the last delivery back past it.
	_, err := db.ExecContext(ctx,
		`UPDATE notification_channel_keys SET last_sent_at = NOW() - INTERVAL '2 hours',
		        first_suppressed_at = NOW() - INTERVAL '90 minutes'`)
	require.NoError(t, err)

	res, err := e.NotifyChannel(ctx, keyedSend(ch, "k"))
	require.NoError(t, err)
	require.Equal(t, 1, res.Queued)

	claimed, err := store.ClaimImmediate(ctx, time.Minute, allTransports)
	require.NoError(t, err)
	claimed, err = store.ClaimImmediate(ctx, time.Minute, allTransports)
	require.NoError(t, err)
	doc := claimed.Payload.Document
	require.NotNil(t, doc)
	require.Equal(t, 2, doc.Suppressed)
	require.NotNil(t, doc.SuppressedSince)
	require.WithinDuration(t, time.Now().Add(-90*time.Minute), *doc.SuppressedSince, 5*time.Minute)

	var count int
	var first *time.Time
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT suppressed, first_suppressed_at FROM notification_channel_keys WHERE key = 'k'`).Scan(&count, &first))
	require.Zero(t, count)
	require.Nil(t, first)
}

// TestChannelKey_ALongerWindowHoldsAgainstAShorterOne_RealDB: a sender that
// lengthened a key's window keeps it lengthened; a later send asking for the
// channel's shorter window does not cut it short.
func TestChannelKey_ALongerWindowHoldsAgainstAShorterOne_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
	t.Cleanup(e.Close)
	ch := monitorChannel()

	long := keyedSend(ch, "k")
	long.RepeatAfter = 6 * time.Hour
	_, err := e.NotifyChannel(ctx, long)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE notification_channel_keys SET last_sent_at = NOW() - INTERVAL '2 hours'`)
	require.NoError(t, err)

	res, err := e.NotifyChannel(ctx, keyedSend(ch, "k"))
	require.NoError(t, err)
	require.True(t, res.Suppressed, "a send two hours into a six-hour window went through")
}

// TestChannelCap_The61stSendInAnHourIsRefused_RealDB: the hourly cap is
// counted in the database, and the refusal names it.
func TestChannelCap_The61stSendInAnHourIsRefused_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	ch := monitorChannel()
	for i := range notification.DefaultChannelMaxPerHour {
		// An enqueuer per send keeps the per-actor rate limit, which is a
		// separate in-process bound, out of what this test measures.
		e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
		res, err := e.NotifyChannel(ctx, notification.ChannelSend{Channel: ch, Document: notification.Document{Title: "t"}})
		e.Close()
		require.NoError(t, err, "send %d", i+1)
		require.Equal(t, 1, res.Queued)
	}
	e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
	t.Cleanup(e.Close)
	_, err := e.NotifyChannel(ctx, notification.ChannelSend{Channel: ch, Document: notification.Document{Title: "t"}})
	require.ErrorIs(t, err, notification.ErrChannelOverCap)
	require.Contains(t, err.Error(), "cap of 60 messages in the last hour")
	require.Equal(t, notification.DefaultChannelMaxPerHour, channelRows(t, ctx, store, "ops"))

	// A send older than the hour does not count.
	_, err = db.ExecContext(ctx, `UPDATE notifications SET created_at = NOW() - INTERVAL '61 minutes' WHERE id = (SELECT MIN(id) FROM notifications)`)
	require.NoError(t, err)
	_, err = e.NotifyChannel(ctx, notification.ChannelSend{Channel: ch, Document: notification.Document{Title: "t"}})
	require.NoError(t, err)
}

// TestChannelCap_AnEmailListCountsOneSendNotOneRowPerRecipient_RealDB: the cap
// bounds what a sender does, and the length of the operator's list is not
// something the sender chose.
func TestChannelCap_AnEmailListCountsOneSendNotOneRowPerRecipient_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	ch := notification.Channel{
		Name: "list", Kind: notification.ChannelKindEmail, Enabled: true, Mode: notification.ChannelModeImmediate,
		Recipients: []string{"a@example.com", "b@example.com", "c@example.com"}, MaxPerHour: 2,
	}
	for range 2 {
		e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
		res, err := e.NotifyChannel(ctx, notification.ChannelSend{Channel: ch, Document: notification.Document{Title: "t"}})
		e.Close()
		require.NoError(t, err)
		require.Equal(t, 3, res.Queued)
	}
	e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
	t.Cleanup(e.Close)
	_, err := e.NotifyChannel(ctx, notification.ChannelSend{Channel: ch, Document: notification.Document{Title: "t"}})
	require.True(t, errors.Is(err, notification.ErrChannelOverCap), "third send of a cap-2 list: %v", err)
}

// TestChannelDigest_OneChannelsRowsClaimAsOneBatch_RealDB: a daily channel's
// rows are addressed to the channel, so the digest claim gathers them into the
// one batch the worker posts as a bulletin. Keys do not apply to them.
func TestChannelDigest_OneChannelsRowsClaimAsOneBatch_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	ch := monitorChannel()
	ch.Mode = notification.ChannelModeDaily
	e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
	t.Cleanup(e.Close)
	for range 3 {
		res, err := e.NotifyChannel(ctx, keyedSend(ch, "same-key"))
		require.NoError(t, err)
		require.Equal(t, 1, res.Queued, "a daily channel's send was suppressed")
	}
	_, err := db.ExecContext(ctx, `UPDATE notifications SET scheduled_for = NOW() - INTERVAL '1 minute'`)
	require.NoError(t, err)

	batch, err := store.ClaimDigest(ctx, time.Minute, allTransports)
	require.NoError(t, err)
	require.Len(t, batch, 3)
	for _, n := range batch {
		require.Equal(t, "channel:ops", n.Recipient)
	}
}

// TestPurgeChannelKeys_RemovesOnlyKeysLongPastTheirWindow_RealDB.
func TestPurgeChannelKeys_RemovesOnlyKeysLongPastTheirWindow_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO notification_channel_keys (channel, key, last_sent_at, window_seconds) VALUES
		('ops', 'stale', NOW() - INTERVAL '40 days', 3600),
		('ops', 'long-window', NOW() - INTERVAL '40 days', 1728000),
		('ops', 'fresh', NOW(), 3600)`)
	require.NoError(t, err)

	n, err := store.PurgeChannelKeys(ctx, 30*24*time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	var left []string
	rows, err := db.QueryContext(ctx, `SELECT key FROM notification_channel_keys ORDER BY key`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k string
		require.NoError(t, rows.Scan(&k))
		left = append(left, k)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"fresh", "long-window"}, left)
}

// TestChannelActivity_ReadsWhatTheStatusRouteShows_RealDB: the one statement
// behind the admin status route, against rows in each state it reads.
func TestChannelActivity_ReadsWhatTheStatusRouteShows_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	quiet, err := store.ChannelActivity(ctx, "ops")
	require.NoError(t, err)
	require.Equal(t, notification.ChannelActivity{}, quiet)

	ch := monitorChannel()
	for _, key := range []string{"a", "a", "a", "b"} {
		e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
		_, err := e.NotifyChannel(ctx, keyedSend(ch, key))
		e.Close()
		require.NoError(t, err)
	}
	// Two rows queued (keys a and b), two repeats of a held back. Deliver one,
	// fail the other with the upstream's words.
	first, err := store.ClaimImmediate(ctx, time.Minute, allTransports)
	require.NoError(t, err)
	require.NoError(t, store.MarkSent(ctx, []int64{first.ID}))
	second, err := store.ClaimImmediate(ctx, time.Minute, allTransports)
	require.NoError(t, err)
	require.NoError(t, store.Retry(ctx, []int64{second.ID}, "upstream answered HTTP 503", time.Minute))

	act, err := store.ChannelActivity(ctx, "ops")
	require.NoError(t, err)
	require.NotNil(t, act.LastDeliveredAt)
	require.Equal(t, "upstream answered HTTP 503", act.LastError)
	require.NotNil(t, act.LastErrorAt)
	require.Equal(t, 2, act.SentLastHour)
	require.Equal(t, 1, act.Pending)
	require.Equal(t, 2, act.Suppressed)

	// The history listing narrows to the channel.
	rows, err := store.List(ctx, notification.HistoryFilter{Channel: "ops"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	none, err := store.Count(ctx, notification.HistoryFilter{Channel: "elsewhere"})
	require.NoError(t, err)
	require.Zero(t, none)
}

// TestRunPosts_GathersARunsPostsByChannelAndSend_RealDB: an email channel's
// send is one post however many recipients it fanned out to, and another
// run's posts are not this run's.
func TestRunPosts_GathersARunsPostsByChannelAndSend_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	send := func(ch notification.Channel, title, runID string) {
		e := notification.NewEnqueuer(notifyprefs.NewPostgresStore(db), store, 13)
		defer e.Close()
		_, err := e.NotifyChannel(ctx, notification.ChannelSend{Channel: ch, Document: notification.Document{
			Title: title, Source: &notification.DocumentSource{Kind: notification.DocumentSourceScript, RunID: runID},
		}})
		require.NoError(t, err)
	}
	list := notification.Channel{
		Name: "list", Kind: notification.ChannelKindEmail, Enabled: true,
		Mode: notification.ChannelModeImmediate, Recipients: []string{"a@example.com", "b@example.com"},
	}
	send(monitorChannel(), "Feed stale", "run_1")
	send(list, "Weekly", "run_1")
	send(monitorChannel(), "Other run", "run_2")

	claimed, err := store.ClaimImmediate(ctx, time.Minute, allTransports)
	require.NoError(t, err)
	require.NoError(t, store.MarkSent(ctx, []int64{claimed.ID}))

	posts, err := store.RunPosts(ctx, "run_1")
	require.NoError(t, err)
	require.Len(t, posts, 2)
	require.Equal(t, "ops", posts[0].Channel)
	require.Equal(t, "Feed stale", posts[0].Title)
	require.Equal(t, 1, posts[0].Sent)
	require.NotNil(t, posts[0].LastSentAt)
	require.Equal(t, "list", posts[1].Channel)
	require.Equal(t, 2, posts[1].Deliveries)
	require.Zero(t, posts[1].Sent)

	none, err := store.RunPosts(ctx, "run_404")
	require.NoError(t, err)
	require.NotNil(t, none)
	require.Empty(t, none)
}
