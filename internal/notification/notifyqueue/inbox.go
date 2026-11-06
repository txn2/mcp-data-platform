package notifyqueue

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// The read state of a person's notifications in the portal (#2512), and the
// removal of a deleted message's text from the rows it produced (#2514).

// CountUnread returns how many of the recipient's notifications are unread.
func (s *PostgresStore) CountUnread(ctx context.Context, recipient string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE recipient = $1 AND read_at IS NULL`, recipient,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting unread notifications: %w", err)
	}
	return n, nil
}

// UnreadAmong reports which of ids are the recipient's unread rows.
func (s *PostgresStore) UnreadAmong(ctx context.Context, recipient string, ids []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM notifications WHERE recipient = $1 AND read_at IS NULL AND id = ANY($2)`,
		recipient, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("reading unread notifications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning unread notification: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating unread notifications: %w", err)
	}
	return out, nil
}

// MarkRead marks the recipient's rows read: those in ids, or all of them when
// ids is empty. The recipient is in every statement, so an id belonging to
// someone else matches nothing.
func (s *PostgresStore) MarkRead(ctx context.Context, recipient string, ids []int64) (int64, error) {
	query := `UPDATE notifications SET read_at = NOW() WHERE recipient = $1 AND read_at IS NULL`
	args := []any{recipient}
	if len(ids) > 0 {
		query += ` AND id = ANY($2)`
		args = append(args, pq.Array(ids))
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("marking notifications read: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting notifications marked read: %w", err)
	}
	return n, nil
}

// RetractEvent takes a deleted thread message's text out of the queue: the
// rows not yet sent are dropped, and the rest lose the snippet and read as
// read, so neither the mail worker nor any listing repeats what it said.
func (s *PostgresStore) RetractEvent(ctx context.Context, eventID string) error {
	if eventID == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE payload ? 'event_id' AND payload ->> 'event_id' = $1 AND status = $2`,
		eventID, notification.StatusPending); err != nil {
		return fmt.Errorf("dropping a deleted message's notifications: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE notifications
		    SET payload = payload - 'message', read_at = COALESCE(read_at, NOW())
		  WHERE payload ? 'event_id' AND payload ->> 'event_id' = $1`, eventID); err != nil {
		return fmt.Errorf("removing a deleted message's text: %w", err)
	}
	return nil
}

// Verify interface compliance.
var _ notification.InboxStore = (*PostgresStore)(nil)
