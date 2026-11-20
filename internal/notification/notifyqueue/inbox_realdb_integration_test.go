//go:build integration

package notifyqueue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// TestInboxRealDB proves the portal's unread list (#2512) and the retraction
// of a deleted message (#2514) against real Postgres: one person's ids never
// change another's rows, and a retracted message's text leaves every row.
func TestInboxRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	enqueue := func(recipient, eventID, message string) {
		require.NoError(t, store.Enqueue(ctx, notification.Notification{
			Recipient: recipient, Category: notification.CategoryComment,
			Payload: notification.Payload{Kind: notification.KindComment, ItemID: "thr_1", Message: message, EventID: eventID},
		}))
	}
	enqueue("b@example.com", "evt_1", "the secret number is 42")
	enqueue("b@example.com", "evt_2", "second")
	enqueue("c@example.com", "evt_1", "the secret number is 42")

	n, err := store.CountUnread(ctx, "b@example.com")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	rows, err := store.List(ctx, notification.HistoryFilter{Recipient: "b@example.com", Unread: true})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	cRows, err := store.List(ctx, notification.HistoryFilter{Recipient: "c@example.com"})
	require.NoError(t, err)
	require.Len(t, cRows, 1)
	marked, err := store.MarkRead(ctx, "b@example.com", []int64{cRows[0].ID})
	require.NoError(t, err)
	require.Zero(t, marked, "another person's id changes nothing")

	unread, err := store.UnreadAmong(ctx, "b@example.com", []int64{rows[0].ID, rows[1].ID, cRows[0].ID})
	require.NoError(t, err)
	require.True(t, unread[rows[0].ID])
	require.False(t, unread[cRows[0].ID])

	var evt2 int64
	for _, r := range rows {
		if r.Payload.EventID == "evt_2" {
			evt2 = r.ID
		}
	}
	marked, err = store.MarkRead(ctx, "b@example.com", []int64{evt2})
	require.NoError(t, err)
	require.EqualValues(t, 1, marked)
	n, err = store.CountUnread(ctx, "b@example.com")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// One row for evt_1 is sent, so it stays with its text removed; the
	// pending one is dropped.
	_, err = db.ExecContext(ctx, `UPDATE notifications SET status = 'sent' WHERE recipient = 'c@example.com'`)
	require.NoError(t, err)
	require.NoError(t, store.RetractEvent(ctx, "evt_1"))
	var left int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE payload::text LIKE '%secret number%'`).Scan(&left))
	require.Zero(t, left, "the deleted message's text is in no row")
	var survivors int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE payload ->> 'event_id' = 'evt_1'`).Scan(&survivors))
	require.Equal(t, 1, survivors, "the sent row stays, the pending one is dropped")
	var readAt any
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT read_at FROM notifications WHERE payload ->> 'event_id' = 'evt_1'`).Scan(&readAt))
	require.NotNil(t, readAt, "a retracted row no longer counts as unread")

	// b's evt_2 row is read and their evt_1 row was dropped: nothing left.
	marked, err = store.MarkRead(ctx, "b@example.com", nil)
	require.NoError(t, err)
	require.Zero(t, marked)
}

// TestDropPendingRealDB holds #2725 on a migrated database: a decided
// approval request's approver notices not sent yet are dropped, a sent one and
// another request's are kept.
func TestDropPendingRealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	ctx := context.Background()
	enqueue := func(recipient, kind, item string) {
		require.NoError(t, store.Enqueue(ctx, notification.Notification{
			Recipient: recipient, Category: notification.CategoryShare,
			Payload: notification.Payload{Kind: kind, ItemID: item},
		}))
	}
	enqueue("a1@example.com", notification.KindApprovalRequest, "apr_1")
	enqueue("a2@example.com", notification.KindApprovalRequest, "apr_1")
	enqueue("a1@example.com", notification.KindApprovalRequest, "apr_2")
	enqueue("j@example.com", notification.KindApprovalDecision, "apr_1")
	sent, err := store.List(ctx, notification.HistoryFilter{Recipient: "a2@example.com"})
	require.NoError(t, err)
	require.Len(t, sent, 1)
	require.NoError(t, store.MarkSent(ctx, []int64{sent[0].ID}))

	dropped, err := store.DropPending(ctx, notification.KindApprovalRequest, "apr_1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dropped, "only the unsent notice of apr_1 is dropped")
	for to, want := range map[string]int{"a1@example.com": 1, "a2@example.com": 1, "j@example.com": 1} {
		rows, err := store.List(ctx, notification.HistoryFilter{Recipient: to})
		require.NoError(t, err)
		assert.Len(t, rows, want, to)
	}
}
