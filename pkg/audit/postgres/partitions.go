package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// partitionPrefix names the table family used for monthly partitions of
	// audit_logs (e.g. audit_logs_2026_05). audit_logs_default keeps the
	// non-rotating fallback partition declared by migration 000002.
	partitionPrefix        = "audit_logs_"
	defaultPartitionSuffix = "default"
	partitionDateFormat    = "2006_01"
	partitionDateSQLLayout = "2006-01-02"
	listPartitionsQuery    = `
		SELECT c.relname
		FROM pg_class p
		JOIN pg_inherits i ON i.inhparent = p.oid
		JOIN pg_class c    ON c.oid = i.inhrelid
		WHERE p.relname = 'audit_logs'
		ORDER BY c.relname
	`
	createPartitionTemplate = `CREATE TABLE IF NOT EXISTS %s PARTITION OF audit_logs FOR VALUES FROM ('%s') TO ('%s')`
	dropPartitionTemplate   = `DROP TABLE IF EXISTS %s`
)

// minPartitionYear and maxPartitionYear bound the year component of a
// partition name. Below 2000 and above 9999 fall outside any plausible
// deployment lifetime and almost certainly indicate a corrupt or
// adversarial table name. monthMin and monthMax mirror the Gregorian
// calendar; values outside this range can never appear in time.Time.Month().
const (
	minPartitionYear = 2000
	maxPartitionYear = 9999
	monthMin         = 1
	monthMax         = 12
)

// monthStart returns the first day of the month containing t in UTC.
func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// EnsureMonthlyPartitions creates named monthly partitions of audit_logs
// for the next monthsAhead months starting from next month. The current
// month is intentionally skipped so a brownfield deployment (where rows
// for the current month already exist in audit_logs_default) does not
// fail with "partition key conflicts with row in default partition".
// Existing rows in audit_logs_default age out through the standard
// retention DELETE; once the first named partition's window opens, new
// rows route into it automatically.
//
// Safe to call repeatedly; CREATE TABLE IF NOT EXISTS makes each
// statement idempotent.
func (s *Store) EnsureMonthlyPartitions(ctx context.Context, monthsAhead int) error {
	if monthsAhead < 1 {
		return nil
	}
	base := monthStart(time.Now().UTC()).AddDate(0, 1, 0)
	for i := range monthsAhead {
		from := base.AddDate(0, i, 0)
		to := from.AddDate(0, 1, 0)
		name := partitionPrefix + from.Format(partitionDateFormat)
		// G201: the only %s placeholders are the generated partition name
		// (constructed from an internal time.Format with no user input) and
		// two date literals likewise produced by time.Format. None of these
		// inputs are operator- or model-supplied, so SQL string formatting
		// is safe here.
		// #nosec G201 -- inputs are time.Format outputs, not user input
		stmt := fmt.Sprintf(createPartitionTemplate, //nolint:gosec // G201: inputs are time.Format outputs, not user input
			name,
			from.Format(partitionDateSQLLayout),
			to.Format(partitionDateSQLLayout),
		)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensuring partition %s: %w", name, err)
		}
	}
	return nil
}

// DropExpiredPartitions removes named monthly partitions whose entire
// date range has aged past the retention window. The default partition
// (audit_logs_default) is never dropped; rows that landed there are
// removed by the row-level DELETE in Cleanup.
//
// A partition is considered expired when its end date (the exclusive
// upper bound) is at or before the retention cutoff. For example, with
// retention=90 days and today=2026-09-30, partitions covering ranges
// ending on or before 2026-07-02 are dropped.
func (s *Store) DropExpiredPartitions(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	partitions, err := s.listMonthlyPartitions(ctx)
	if err != nil {
		return err
	}
	for _, p := range partitions {
		end, ok := parseMonthlyPartitionEnd(p)
		if !ok {
			continue
		}
		if !end.After(cutoff) {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf(dropPartitionTemplate, p)); err != nil {
				return fmt.Errorf("dropping expired partition %s: %w", p, err)
			}
		}
	}
	return nil
}

// listMonthlyPartitions returns the names of monthly partitions of
// audit_logs, excluding audit_logs_default. The order is name-ascending
// (which is chronological because the suffix is YYYY_MM).
func (s *Store) listMonthlyPartitions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, listPartitionsQuery)
	if err != nil {
		return nil, fmt.Errorf("listing audit_logs partitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning partition row: %w", err)
		}
		if strings.HasSuffix(name, "_"+defaultPartitionSuffix) {
			continue
		}
		if !strings.HasPrefix(name, partitionPrefix) {
			continue
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating partition rows: %w", err)
	}
	return names, nil
}

// parseMonthlyPartitionEnd parses a partition name of the form
// audit_logs_YYYY_MM and returns the exclusive upper bound of its
// date range (the first day of the following month, UTC). Returns
// ok=false for names that do not match the expected pattern.
func parseMonthlyPartitionEnd(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, partitionPrefix) {
		return time.Time{}, false
	}
	suffix := strings.TrimPrefix(name, partitionPrefix)
	parts := strings.Split(suffix, "_")
	if len(parts) != 2 {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil || year < minPartitionYear || year > maxPartitionYear {
		return time.Time{}, false
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil || month < monthMin || month > monthMax {
		return time.Time{}, false
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	return start.AddDate(0, 1, 0), true
}
