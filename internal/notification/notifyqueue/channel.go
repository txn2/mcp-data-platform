package notifyqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// channelLockPrefix namespaces the advisory lock a channel send takes, so it
// cannot collide with another subsystem's lock on a hash of the same name.
const channelLockPrefix = "notification-channel:"

// EnqueueChannel writes one channel send's rows in one transaction behind the
// channel's gate.
//
// The transaction takes a per-channel advisory lock first. That is what makes
// the cap and the key exact across replicas: the count of the last hour's
// sends and the key row are read and written by one sender at a time, and the
// lock is released by the commit that makes the new rows visible to the next.
//
// The cap counts sends, not rows: an email channel fans one send out to a row
// per recipient, and those rows are inserted here in one transaction, so they
// share its NOW() as their created_at. Counting distinct created_at counts
// each send once whatever the length of the list.
func (s *PostgresStore) EnqueueChannel(ctx context.Context, rows []notification.Notification, gate notification.ChannelGate) (notification.ChannelOutcome, error) {
	if len(rows) == 0 {
		return notification.ChannelOutcome{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return notification.ChannelOutcome{}, fmt.Errorf("beginning channel send: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockUnderCap(ctx, tx, gate); err != nil {
		return notification.ChannelOutcome{}, err
	}
	rows, deliver, err := keyed(ctx, tx, gate, rows)
	if err != nil {
		return notification.ChannelOutcome{}, err
	}
	if !deliver {
		if err := tx.Commit(); err != nil {
			return notification.ChannelOutcome{}, fmt.Errorf("recording a suppressed send: %w", err)
		}
		return notification.ChannelOutcome{Suppressed: true}, nil
	}
	for _, n := range rows {
		if err := insertRow(ctx, tx, n); err != nil {
			return notification.ChannelOutcome{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return notification.ChannelOutcome{}, fmt.Errorf("committing channel send: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, '')`, NotifyChannel)
	return notification.ChannelOutcome{}, nil
}

// keyed applies the send's key, when it has one: it reports whether the send
// goes through, and returns the rows stamped with what the key held back.
func keyed(ctx context.Context, tx *sql.Tx, gate notification.ChannelGate, rows []notification.Notification) ([]notification.Notification, bool, error) {
	if gate.Key == "" {
		return rows, true, nil
	}
	decision, err := claimKey(ctx, tx, gate)
	if err != nil {
		return nil, false, err
	}
	if !decision.deliver {
		return nil, false, nil
	}
	return withSuppression(rows, decision), true, nil
}

// lockUnderCap takes the channel's lock, then refuses the send when the
// channel has been sent its hourly cap.
func lockUnderCap(ctx context.Context, tx *sql.Tx, gate notification.ChannelGate) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		channelLockPrefix+gate.Channel); err != nil {
		return fmt.Errorf("locking channel %s: %w", gate.Channel, err)
	}
	return underCap(ctx, tx, gate)
}

// underCap refuses the send when the channel has been sent its hourly cap.
func underCap(ctx context.Context, tx *sql.Tx, gate notification.ChannelGate) error {
	if gate.MaxPerHour <= 0 {
		return nil
	}
	var sent int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT created_at) FROM notifications
		  WHERE channel = $1 AND created_at > NOW() - INTERVAL '1 hour'`, gate.Channel).Scan(&sent); err != nil {
		return fmt.Errorf("counting channel %s's sends: %w", gate.Channel, err)
	}
	if sent >= gate.MaxPerHour {
		return notification.ErrChannelOverCap
	}
	return nil
}

// keyState is a key's row as the send found it.
type keyState struct {
	found           bool
	lastSentAt      time.Time
	window          time.Duration
	suppressed      int
	firstSuppressed *time.Time
}

// keyDecision is what a keyed send does: go through, carrying the count of the
// sends absorbed since the last one that did, or be absorbed.
type keyDecision struct {
	deliver    bool
	suppressed int
	since      *time.Time
}

// decideKey applies the repeat window. The window measured against is the
// longer of the one the last delivered send recorded and the one this send
// asks for, so a sender that lengthened the window for a key keeps it
// lengthened until a send goes through.
func decideKey(st keyState, now time.Time, window time.Duration) keyDecision {
	if !st.found {
		return keyDecision{deliver: true}
	}
	if now.Before(st.lastSentAt.Add(max(st.window, window))) {
		return keyDecision{}
	}
	return keyDecision{deliver: true, suppressed: st.suppressed, since: st.firstSuppressed}
}

// claimKey reads the key's row, decides, and writes the decision.
func claimKey(ctx context.Context, tx *sql.Tx, gate notification.ChannelGate) (keyDecision, error) {
	var (
		st    keyState
		now   time.Time
		first sql.NullTime
		secs  int64
	)
	err := tx.QueryRowContext(ctx,
		`SELECT NOW(), last_sent_at, window_seconds, suppressed, first_suppressed_at
		   FROM notification_channel_keys WHERE channel = $1 AND key = $2`,
		gate.Channel, gate.Key).Scan(&now, &st.lastSentAt, &secs, &st.suppressed, &first)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&now); err != nil {
			return keyDecision{}, fmt.Errorf("reading the database clock: %w", err)
		}
	case err != nil:
		return keyDecision{}, fmt.Errorf("reading channel key: %w", err)
	default:
		st.found = true
		st.window = time.Duration(secs) * time.Second
		if first.Valid {
			st.firstSuppressed = &first.Time
		}
	}
	decision := decideKey(st, now, gate.Window)
	if err := writeKey(ctx, tx, gate, decision); err != nil {
		return keyDecision{}, err
	}
	return decision, nil
}

// writeKey records the decision: a delivered send resets the key's count and
// window, an absorbed one adds to the count.
func writeKey(ctx context.Context, tx *sql.Tx, gate notification.ChannelGate, d keyDecision) error {
	var err error
	if d.deliver {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO notification_channel_keys (channel, key, last_sent_at, window_seconds, suppressed, first_suppressed_at)
			 VALUES ($1, $2, NOW(), $3, 0, NULL)
			 ON CONFLICT (channel, key) DO UPDATE
			   SET last_sent_at = NOW(), window_seconds = $3, suppressed = 0, first_suppressed_at = NULL`,
			gate.Channel, gate.Key, int64(gate.Window/time.Second))
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE notification_channel_keys
			    SET suppressed = suppressed + 1, first_suppressed_at = COALESCE(first_suppressed_at, NOW())
			  WHERE channel = $1 AND key = $2`, gate.Channel, gate.Key)
	}
	if err != nil {
		return fmt.Errorf("recording channel key: %w", err)
	}
	return nil
}

// withSuppression stamps the count of absorbed sends on the document the rows
// carry, so every kind states it and the history shows it.
func withSuppression(rows []notification.Notification, d keyDecision) []notification.Notification {
	if d.suppressed <= 0 {
		return rows
	}
	out := make([]notification.Notification, len(rows))
	for i, n := range rows {
		if n.Payload.Document != nil {
			doc := *n.Payload.Document
			doc.Suppressed, doc.SuppressedSince = d.suppressed, d.since
			n.Payload.Document = &doc
		}
		out[i] = n
	}
	return out
}

// insertRow writes one queue row inside the send's transaction.
func insertRow(ctx context.Context, tx *sql.Tx, n notification.Notification) error {
	payload, err := json.Marshal(n.Payload)
	if err != nil {
		return fmt.Errorf("encoding notification payload: %w", err)
	}
	var scheduled any
	if !n.ScheduledFor.IsZero() {
		scheduled = n.ScheduledFor
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notifications (recipient, category, payload, digest, scheduled_for, channel)
		 VALUES ($1, $2, $3, $4, COALESCE($5, NOW()), $6)`,
		n.Recipient, n.Category, payload, n.Digest, scheduled, n.Channel); err != nil {
		return fmt.Errorf("enqueueing notification: %w", err)
	}
	return nil
}

// PurgeChannelKeys deletes keys whose window ended more than retention ago. A
// key that old has no suppressed count anyone will see -- the next send with
// it goes through either way -- so the row is only weight.
func (s *PostgresStore) PurgeChannelKeys(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_channel_keys
		  WHERE last_sent_at + make_interval(secs => window_seconds) < NOW() - ($1 || ' seconds')::INTERVAL`,
		int64(retention/time.Second))
	if err != nil {
		return 0, fmt.Errorf("purging channel keys: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting purged channel keys: %w", err)
	}
	return n, nil
}

// ChannelActivity reads what one channel has done recently, in one statement.
func (s *PostgresStore) ChannelActivity(ctx context.Context, channel string) (notification.ChannelActivity, error) {
	var (
		out       notification.ChannelActivity
		delivered sql.NullTime
		lastErr   sql.NullString
		errAt     sql.NullTime
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT
		    (SELECT MAX(sent_at) FROM notifications WHERE channel = $1 AND status = 'sent'),
		    last.last_error, last.created_at,
		    (SELECT COUNT(DISTINCT created_at) FROM notifications
		      WHERE channel = $1 AND created_at > NOW() - INTERVAL '1 hour'),
		    (SELECT COUNT(*) FROM notifications WHERE channel = $1 AND status IN ('pending', 'sending')),
		    (SELECT COALESCE(SUM(suppressed), 0) FROM notification_channel_keys WHERE channel = $1)
		   FROM (SELECT 1) AS one
		   LEFT JOIN LATERAL (
		     SELECT last_error, created_at FROM notifications
		      WHERE channel = $1 AND last_error <> ''
		      ORDER BY created_at DESC, id DESC LIMIT 1) AS last ON TRUE`,
		channel).Scan(&delivered, &lastErr, &errAt, &out.SentLastHour, &out.Pending, &out.Suppressed)
	if err != nil {
		return notification.ChannelActivity{}, fmt.Errorf("reading channel %s's activity: %w", channel, err)
	}
	if delivered.Valid {
		out.LastDeliveredAt = &delivered.Time
	}
	if errAt.Valid {
		out.LastError, out.LastErrorAt = lastErr.String, &errAt.Time
	}
	return out, nil
}

// RunPosts reads the posts one managed-script run made, oldest first. A post
// is identified by the run recorded on its document and the instant it was
// queued: the rows of one send share the transaction's NOW().
func (s *PostgresStore) RunPosts(ctx context.Context, runID string) ([]notification.RunPost, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel, created_at, MIN(payload->'document'->>'title'), COUNT(*),
		        COUNT(*) FILTER (WHERE status = 'sent'), COUNT(*) FILTER (WHERE status = 'failed'),
		        MAX(sent_at), COALESCE(MAX(NULLIF(last_error, '')), '')
		   FROM notifications
		  WHERE channel IS NOT NULL AND payload->'document'->'source'->>'run_id' = $1
		  GROUP BY channel, created_at
		  ORDER BY created_at, channel`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading run %s's posts: %w", runID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []notification.RunPost{}
	for rows.Next() {
		var (
			p      notification.RunPost
			title  sql.NullString
			sentAt sql.NullTime
		)
		if err := rows.Scan(&p.Channel, &p.QueuedAt, &title, &p.Deliveries, &p.Sent, &p.Failed, &sentAt, &p.LastError); err != nil {
			return nil, fmt.Errorf("scanning a run post: %w", err)
		}
		p.Title = title.String
		if sentAt.Valid {
			p.LastSentAt = &sentAt.Time
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating run posts: %w", err)
	}
	return out, nil
}
