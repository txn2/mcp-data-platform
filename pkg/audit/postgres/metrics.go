package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// defaultMetricsWindow is the default lookback when no time range is specified.
const defaultMetricsWindow = 24 * time.Hour

// defaultBreakdownLimit is the default number of breakdown entries returned.
const defaultBreakdownLimit = 10

// maxBreakdownLimit caps the number of breakdown entries to prevent abuse.
const maxBreakdownLimit = 100

// Timeseries returns audit event counts bucketed by the given resolution.
func (s *Store) Timeseries(ctx context.Context, filter audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error) {
	if !audit.ValidResolutions[filter.Resolution] {
		return nil, fmt.Errorf("invalid resolution: %q", filter.Resolution)
	}

	start, end := defaultTimeRange(filter.StartTime, filter.EndTime)

	// Resolution is validated against ValidResolutions — safe for column expression.
	truncExpr := fmt.Sprintf("date_trunc('%s', timestamp) AS bucket", string(filter.Resolution))

	qb := psq.Select(
		truncExpr,
		"COUNT(*) AS count",
		"COUNT(*) FILTER (WHERE success = true) AS success_count",
		"COUNT(*) FILTER (WHERE success = false) AS error_count",
		"COALESCE(AVG(duration_ms), 0) AS avg_duration_ms",
	).From("audit_logs").
		Where(sq.GtOrEq{"timestamp": start}).
		Where(sq.LtOrEq{"timestamp": end}).
		GroupBy("bucket").
		OrderBy("bucket ASC")

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building timeseries query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying timeseries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var buckets []audit.TimeseriesBucket
	for rows.Next() {
		var bucket audit.TimeseriesBucket
		if err := rows.Scan(
			&bucket.Bucket,
			&bucket.Count,
			&bucket.SuccessCount,
			&bucket.ErrorCount,
			&bucket.AvgDurationMS,
		); err != nil {
			return nil, fmt.Errorf("scanning timeseries row: %w", err)
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating timeseries rows: %w", err)
	}

	if buckets == nil {
		buckets = []audit.TimeseriesBucket{}
	}
	return buckets, nil
}

// Breakdown returns audit event counts grouped by a dimension.
func (s *Store) Breakdown(ctx context.Context, filter audit.BreakdownFilter) ([]audit.BreakdownEntry, error) {
	if !audit.ValidBreakdownDimensions[filter.GroupBy] {
		return nil, fmt.Errorf("invalid breakdown dimension: %q", filter.GroupBy)
	}

	start, end := defaultTimeRange(filter.StartTime, filter.EndTime)
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultBreakdownLimit
	}
	if limit > maxBreakdownLimit {
		limit = maxBreakdownLimit
	}

	// col is validated against ValidBreakdownDimensions — safe for column reference.
	col := string(filter.GroupBy)

	qb := psq.Select(
		fmt.Sprintf("COALESCE(%s, '') AS dimension", col),
		"COUNT(*) AS count",
		"CASE WHEN COUNT(*) > 0 THEN CAST(COUNT(*) FILTER (WHERE success = true) AS FLOAT) / COUNT(*) ELSE 0 END AS success_rate",
		"COALESCE(AVG(duration_ms), 0) AS avg_duration_ms",
	).From("audit_logs").
		Where(sq.GtOrEq{"timestamp": start}).
		Where(sq.LtOrEq{"timestamp": end}).
		GroupBy(col).
		OrderBy("count DESC")
	if limit > 0 {
		qb = qb.Limit(uint64(limit))
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building breakdown query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying breakdown: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []audit.BreakdownEntry
	for rows.Next() {
		var entry audit.BreakdownEntry
		if err := rows.Scan(
			&entry.Dimension,
			&entry.Count,
			&entry.SuccessRate,
			&entry.AvgDurationMS,
		); err != nil {
			return nil, fmt.Errorf("scanning breakdown row: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating breakdown rows: %w", err)
	}

	if entries == nil {
		entries = []audit.BreakdownEntry{}
	}
	return entries, nil
}

// Overview returns aggregate statistics for the given time range.
func (s *Store) Overview(ctx context.Context, startTime, endTime *time.Time) (*audit.Overview, error) {
	start, end := defaultTimeRange(startTime, endTime)

	qb := psq.Select(
		"COUNT(*) AS total_calls",
		"CASE WHEN COUNT(*) > 0 THEN CAST(COUNT(*) FILTER (WHERE success = true) AS FLOAT) / COUNT(*) ELSE 0 END AS success_rate",
		"COALESCE(AVG(duration_ms), 0) AS avg_duration_ms",
		"COUNT(DISTINCT user_id) AS unique_users",
		"COUNT(DISTINCT tool_name) AS unique_tools",
		"CASE WHEN COUNT(*) > 0 THEN CAST(COUNT(*) FILTER (WHERE enrichment_applied = true) AS FLOAT) / COUNT(*) ELSE 0 END AS enrichment_rate",
		"COUNT(*) FILTER (WHERE success = false) AS error_count",
	).From("audit_logs").
		Where(sq.GtOrEq{"timestamp": start}).
		Where(sq.LtOrEq{"timestamp": end})

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building overview query: %w", err)
	}

	var o audit.Overview
	err = s.db.QueryRowContext(ctx, query, args...).Scan(
		&o.TotalCalls,
		&o.SuccessRate,
		&o.AvgDurationMS,
		&o.UniqueUsers,
		&o.UniqueTools,
		&o.EnrichmentRate,
		&o.ErrorCount,
	)
	if err != nil {
		return nil, fmt.Errorf("querying overview: %w", err)
	}
	return &o, nil
}

// Performance returns latency percentile statistics for the given time range.
func (s *Store) Performance(ctx context.Context, startTime, endTime *time.Time) (*audit.PerformanceStats, error) {
	start, end := defaultTimeRange(startTime, endTime)

	qb := psq.Select(
		"COALESCE(PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY duration_ms), 0) AS p50_ms",
		"COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95_ms",
		"COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY duration_ms), 0) AS p99_ms",
		"COALESCE(AVG(duration_ms), 0) AS avg_ms",
		"COALESCE(MAX(duration_ms), 0) AS max_ms",
		"COALESCE(AVG(response_chars), 0) AS avg_response_chars",
		"COALESCE(AVG(request_chars), 0) AS avg_request_chars",
	).From("audit_logs").
		Where(sq.GtOrEq{"timestamp": start}).
		Where(sq.LtOrEq{"timestamp": end})

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building performance query: %w", err)
	}

	var p audit.PerformanceStats
	err = s.db.QueryRowContext(ctx, query, args...).Scan(
		&p.P50MS,
		&p.P95MS,
		&p.P99MS,
		&p.AvgMS,
		&p.MaxMS,
		&p.AvgResponseChars,
		&p.AvgRequestChars,
	)
	if err != nil {
		// Return zeros when no rows exist.
		if errors.Is(err, sql.ErrNoRows) {
			return &audit.PerformanceStats{}, nil
		}
		return nil, fmt.Errorf("querying performance: %w", err)
	}
	return &p, nil
}

// defaultTimeRange returns the start and end times, defaulting to the last 24h.
func defaultTimeRange(start, end *time.Time) (startTime, endTime time.Time) {
	now := time.Now()
	startTime = now.Add(-defaultMetricsWindow)
	endTime = now
	if start != nil {
		startTime = *start
	}
	if end != nil {
		endTime = *end
	}
	return startTime, endTime
}
