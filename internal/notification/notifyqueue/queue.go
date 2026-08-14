// Package notifyqueue is the durable delivery queue: the PostgreSQL
// implementation of notification.QueueStore over the notifications table, and
// the NOTIFY side of the pg_notify wakeup it fires on every enqueue.
//
// The store and the listener are two halves of one mechanism — the store
// NOTIFYs on NotifyChannel, a listener LISTENs on it and wakes the send worker
// — so the channel name that binds them lives here. The listener itself is
// internal/pglisten, shared with every other queue that wants the same wakeup.
package notifyqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/pglisten"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// NotifyChannel is the pg_notify channel that wakes the send worker when a
// row is enqueued. Producers fire it best-effort; the worker also polls.
const NotifyChannel = "notifications"

// NewListener builds the LISTEN adapter for this queue's channel. It binds the
// channel name so callers wire a listener without repeating it.
func NewListener(dsn string, notifiers ...pglisten.Notifier) *pglisten.Listener {
	return pglisten.New(dsn, NotifyChannel, notifiers...)
}

// dueClause matches rows ready for a worker: pending rows whose schedule has
// arrived, plus sending rows whose lease expired (crashed worker reclaim).
const dueClause = `((status = 'pending' AND scheduled_for <= NOW())
	OR (status = 'sending' AND locked_until < NOW()))`

// PostgresStore implements notification.QueueStore backed by the notifications table.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed notification queue.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Enqueue inserts a pending notification row and fires a best-effort
// pg_notify so a listening worker wakes without waiting for the next poll.
func (s *PostgresStore) Enqueue(ctx context.Context, n notification.Notification) error {
	payload, err := json.Marshal(n.Payload)
	if err != nil {
		return fmt.Errorf("encoding notification payload: %w", err)
	}
	// A zero ScheduledFor means "now": stamp it with the database clock,
	// not the Go clock, so the claim predicate (scheduled_for <= NOW())
	// sees the row immediately regardless of host/DB clock skew.
	var scheduled any
	if !n.ScheduledFor.IsZero() {
		scheduled = n.ScheduledFor
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO notifications (recipient, category, payload, digest, scheduled_for)
		 VALUES ($1, $2, $3, $4, COALESCE($5, NOW()))`,
		n.Recipient, n.Category, payload, n.Digest, scheduled)
	if err != nil {
		return fmt.Errorf("enqueueing notification: %w", err)
	}
	// Best-effort wakeup; the worker's poll ticker is the fallback.
	_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, '')`, NotifyChannel)
	return nil
}

// notificationColumns is the scan list shared by the claim queries.
const notificationColumns = `id, recipient, category, payload, digest, status,
	attempts, last_error, scheduled_for, sent_at, created_at`

// ClaimImmediate claims the next due non-digest row.
func (s *PostgresStore) ClaimImmediate(ctx context.Context, lease time.Duration) (*notification.Notification, error) {
	rows, err := s.claim(ctx, lease,
		`UPDATE notifications
		   SET status = 'sending', attempts = attempts + 1,
		       locked_until = NOW() + ($1 || ' seconds')::INTERVAL
		 WHERE id = (
		     SELECT id FROM notifications
		      WHERE digest = FALSE AND `+dueClause+`
		      ORDER BY scheduled_for, id
		      LIMIT 1
		      FOR UPDATE SKIP LOCKED)
		 RETURNING `+notificationColumns)
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// ClaimDigest claims all due digest rows for the recipient with the oldest
// due digest row. Concurrent workers racing for the same recipient are safe:
// the loser's UPDATE matches zero rows and reports notification.ErrNoWork.
func (s *PostgresStore) ClaimDigest(ctx context.Context, lease time.Duration) ([]notification.Notification, error) {
	return s.claim(ctx, lease,
		`UPDATE notifications
		   SET status = 'sending', attempts = attempts + 1,
		       locked_until = NOW() + ($1 || ' seconds')::INTERVAL
		 WHERE digest = TRUE AND `+dueClause+`
		   AND recipient = (
		     SELECT recipient FROM notifications
		      WHERE digest = TRUE AND `+dueClause+`
		      ORDER BY scheduled_for, id
		      LIMIT 1)
		 RETURNING `+notificationColumns)
}

// claim runs one of the claim UPDATE queries and scans the returned rows.
func (s *PostgresStore) claim(ctx context.Context, lease time.Duration, query string) ([]notification.Notification, error) {
	rows, err := s.db.QueryContext(ctx, query, int(lease.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("claiming notifications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var claimed []notification.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning notification row: %w", err)
		}
		claimed = append(claimed, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating notification rows: %w", err)
	}
	if len(claimed) == 0 {
		return nil, notification.ErrNoWork
	}
	return claimed, nil
}

// MarkSent transitions claimed rows to sent.
func (s *PostgresStore) MarkSent(ctx context.Context, ids []int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications
		    SET status = 'sent', sent_at = NOW(), locked_until = NULL, last_error = ''
		  WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("marking notifications sent: %w", err)
	}
	return nil
}

// Retry returns claimed rows to pending, scheduled after the backoff.
func (s *PostgresStore) Retry(ctx context.Context, ids []int64, sendErr string, backoff time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications
		    SET status = 'pending', locked_until = NULL, last_error = $2,
		        scheduled_for = NOW() + ($3 || ' seconds')::INTERVAL
		  WHERE id = ANY($1)`, pq.Array(ids), sendErr, int(backoff.Seconds()))
	if err != nil {
		return fmt.Errorf("retrying notifications: %w", err)
	}
	return nil
}

// Fail marks claimed rows permanently failed.
func (s *PostgresStore) Fail(ctx context.Context, ids []int64, sendErr string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications
		    SET status = 'failed', locked_until = NULL, last_error = $2
		  WHERE id = ANY($1)`, pq.Array(ids), sendErr)
	if err != nil {
		return fmt.Errorf("failing notifications: %w", err)
	}
	return nil
}

// PurgeOld deletes resolved rows past retention and unresolved rows past
// their delivery-relevance window.
func (s *PostgresStore) PurgeOld(ctx context.Context, resolvedRetention, pendingTTL time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM notifications
		  WHERE (status IN ('sent', 'failed') AND created_at < NOW() - ($1 || ' seconds')::INTERVAL)
		     OR (status IN ('pending', 'sending') AND created_at < NOW() - ($2 || ' seconds')::INTERVAL)`,
		int(resolvedRetention.Seconds()), int(pendingTTL.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("purging notifications: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting purged notifications: %w", err)
	}
	return n, nil
}

// scanNotification reads a full notifications row.
func scanNotification(row interface{ Scan(dest ...any) error }) (*notification.Notification, error) {
	var n notification.Notification
	var payload []byte
	var sentAt sql.NullTime
	if err := row.Scan(&n.ID, &n.Recipient, &n.Category, &payload, &n.Digest,
		&n.Status, &n.Attempts, &n.LastError, &n.ScheduledFor, &sentAt, &n.CreatedAt); err != nil {
		return nil, err //nolint:wrapcheck // callers add context per call site
	}
	if err := json.Unmarshal(payload, &n.Payload); err != nil {
		return nil, fmt.Errorf("decoding notification payload: %w", err)
	}
	if sentAt.Valid {
		n.SentAt = &sentAt.Time
	}
	return &n, nil
}

// Verify interface compliance.
var _ notification.QueueStore = (*PostgresStore)(nil)
