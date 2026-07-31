package notifyqueue

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// historyWhere builds the WHERE clause and argument list shared by the three
// history queries. Every filter value is bound as a parameter; nothing the
// caller supplies is ever concatenated into the SQL, so the returned clause
// text is assembled purely from fixed column names and $N placeholders.
func historyWhere(filter notification.HistoryFilter) (clause string, args []any) {
	var clauses []string
	add := func(column, value string) {
		if value == "" {
			return
		}
		args = append(args, value)
		clauses = append(clauses, column+" = $"+strconv.Itoa(len(args)))
	}
	add("recipient", filter.Recipient)
	add("status", filter.Status)
	add("category", filter.Category)
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// List returns one page of notification history, newest first.
func (s *PostgresStore) List(ctx context.Context, filter notification.HistoryFilter) ([]notification.Notification, error) {
	where, args := historyWhere(filter)
	args = append(args, filter.EffectiveLimit(), max(filter.Offset, 0))
	// #nosec G202 -- every part is fixed text: notificationColumns is a
	// package constant, `where` is built by historyWhere from constant column
	// names and $N placeholders, and the two trailing placeholder numbers are
	// derived from len(args). Filter values reach the database only as bound
	// parameters below.
	query := `SELECT ` + notificationColumns + ` FROM notifications` + where +
		` ORDER BY created_at DESC, id DESC` +
		` LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing notifications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []notification.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning notification row: %w", err)
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating notification rows: %w", err)
	}
	return out, nil
}

// Count returns how many rows match the filter, ignoring its paging fields.
func (s *PostgresStore) Count(ctx context.Context, filter notification.HistoryFilter) (int, error) {
	where, args := historyWhere(filter)
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`+where, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("counting notifications: %w", err)
	}
	return total, nil
}

// CountsByStatus returns the per-status row counts for the filter. Statuses
// with no rows are absent from the map; callers render a zero for them.
func (s *PostgresStore) CountsByStatus(ctx context.Context, filter notification.HistoryFilter) (map[string]int, error) {
	where, args := historyWhere(filter)
	// #nosec G202 -- `where` is historyWhere's constant clause text; the
	// filter values are bound as args.
	query := `SELECT status, COUNT(*) FROM notifications` + where + ` GROUP BY status`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("counting notifications by status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scanning notification status count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating notification status counts: %w", err)
	}
	return counts, nil
}

// Verify interface compliance.
var _ notification.HistoryStore = (*PostgresStore)(nil)
