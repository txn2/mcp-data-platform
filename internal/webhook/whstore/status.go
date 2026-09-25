package whstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Status is what a source's page reports about it.
type Status struct {
	// LastHour and LastDay are request counts by outcome.
	LastHour map[string]int64 `json:"last_hour"`
	LastDay  map[string]int64 `json:"last_day"`
	// LastSegmentAt is when a segment of accepted events was last written,
	// which is when the source last received an event. A sender that stops
	// produces no error on the receiving side; this is how that shows.
	LastSegmentAt *time.Time `json:"last_segment_at"`
	// LastCompactedWindow is the start of the newest window whose Parquet
	// file holds every event its segments do.
	LastCompactedWindow *time.Time `json:"last_compacted_window"`
	// Pending counts windows owed a compaction: not yet compacted, or
	// dirtied by a segment written after they were.
	Pending int `json:"pending"`
	// Failing counts pending windows whose last compaction attempt failed.
	Failing   int    `json:"failing"`
	LastError string `json:"last_error,omitempty"`
	// OldestWindow is the start of the oldest window still held.
	OldestWindow *time.Time  `json:"oldest_window"`
	Rejections   []Rejection `json:"rejections"`
}

// Status reads a source's status at now.
func (s *Store) Status(ctx context.Context, source string, now time.Time) (Status, error) {
	st := Status{LastHour: map[string]int64{}, LastDay: map[string]int64{}, Rejections: []Rejection{}}
	if err := s.readCounts(ctx, source, now, &st); err != nil {
		return Status{}, err
	}
	var lastSeg, lastCompacted, oldest sql.NullTime
	var lastError sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(last_segment_at),
		        MAX(window_start) FILTER (WHERE compacted_at IS NOT NULL AND generation = compacted_generation),
		        COUNT(*) FILTER (WHERE generation <> compacted_generation),
		        COUNT(*) FILTER (WHERE generation <> compacted_generation AND last_error <> ''),
		        MIN(window_start),
		        (SELECT last_error FROM webhook_windows e
		          WHERE e.source = $1 AND e.expired_at IS NULL AND e.last_error <> ''
		          ORDER BY e.window_start DESC LIMIT 1)
		   FROM webhook_windows
		  WHERE source = $1 AND expired_at IS NULL`, source,
	).Scan(&lastSeg, &lastCompacted, &st.Pending, &st.Failing, &oldest, &lastError); err != nil {
		return Status{}, fmt.Errorf("reading webhook window status: %w", err)
	}
	st.LastSegmentAt = timePtr(lastSeg)
	st.LastCompactedWindow = timePtr(lastCompacted)
	st.OldestWindow = timePtr(oldest)
	st.LastError = lastError.String

	rows, err := s.db.QueryContext(ctx,
		`SELECT rejected_at, outcome, reason FROM webhook_rejections
		  WHERE source = $1 ORDER BY id DESC LIMIT $2`, source, maxRejections)
	if err != nil {
		return Status{}, fmt.Errorf("reading webhook rejections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r Rejection
		if err := rows.Scan(&r.At, &r.Outcome, &r.Reason); err != nil {
			return Status{}, fmt.Errorf("scanning webhook rejection: %w", err)
		}
		r.At = r.At.UTC()
		st.Rejections = append(st.Rejections, r)
	}
	if err := rows.Err(); err != nil {
		return Status{}, fmt.Errorf("iterating webhook rejections: %w", err)
	}
	return st, nil
}

// readCounts fills the last hour's and last day's counts.
func (s *Store) readCounts(ctx context.Context, source string, now time.Time, st *Status) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT outcome,
		        COALESCE(SUM(count) FILTER (WHERE minute >= $2), 0),
		        SUM(count)
		   FROM webhook_request_counts
		  WHERE source = $1 AND minute >= $3
		  GROUP BY outcome`,
		source, now.Add(-time.Hour).UTC(), now.Add(-24*time.Hour).UTC())
	if err != nil {
		return fmt.Errorf("reading webhook request counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			outcome   string
			hour, day int64
		)
		if err := rows.Scan(&outcome, &hour, &day); err != nil {
			return fmt.Errorf("scanning webhook request count: %w", err)
		}
		if hour > 0 {
			st.LastHour[outcome] = hour
		}
		st.LastDay[outcome] = day
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating webhook request counts: %w", err)
	}
	return nil
}

// SourcesForResources returns, for each resource id that holds a compacted
// window, the source the window belongs to.
func (s *Store) SourcesForResources(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT resource_id, source FROM webhook_windows
		  WHERE resource_id = ANY($1::text[]) AND expired_at IS NULL`, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("reading webhook window resources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, source string
		if err := rows.Scan(&id, &source); err != nil {
			return nil, fmt.Errorf("scanning webhook window resource: %w", err)
		}
		out[id] = source
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook window resources: %w", err)
	}
	return out, nil
}

// ResourceIDs returns the resources a source's windows were written as.
func (s *Store) ResourceIDs(ctx context.Context, source string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT resource_id FROM webhook_windows WHERE source = $1 AND resource_id <> ''`, source)
	if err != nil {
		return nil, fmt.Errorf("reading webhook window resources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning webhook window resource: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook window resources: %w", err)
	}
	return out, nil
}

// timePtr returns a nullable time as a pointer in UTC.
func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}
