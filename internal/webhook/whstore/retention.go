package whstore

import (
	"context"
	"fmt"
	"time"
)

// RawDeletable returns the windows of source whose raw segments may be deleted:
// compacted at the generation they now hold, so every segment's events are in
// the Parquet file, and whose last segment was written at or before cutoff.
// A window owed a compaction is never returned, which is what keeps retention
// from deleting an event that has not been compacted.
func (s *Store) RawDeletable(ctx context.Context, source string, cutoff time.Time) ([]Window, error) {
	return s.windows(ctx,
		`SELECT source, window_start, window_seconds, generation, resource_id, location, attempts
		   FROM webhook_windows
		  WHERE source = $1
		    AND expired_at IS NULL
		    AND compacted_at IS NOT NULL
		    AND generation = compacted_generation
		    AND last_segment_at <= $2
		    AND (raw_deleted_at IS NULL OR raw_deleted_at < last_segment_at)
		  ORDER BY window_start`,
		source, cutoff.UTC())
}

// Expirable returns the windows of source that ended at or before cutoff and
// are not yet expired, and the expired ones a segment has landed in since,
// whose raw objects have to be deleted again so the view does not serve them.
func (s *Store) Expirable(ctx context.Context, source string, cutoff time.Time) ([]Window, error) {
	return s.windows(ctx,
		`SELECT source, window_start, window_seconds, generation, resource_id, location, attempts
		   FROM webhook_windows
		  WHERE source = $1
		    AND window_start + make_interval(secs => window_seconds) <= $2
		    AND (expired_at IS NULL OR last_segment_at > expired_at)
		  ORDER BY window_start`,
		source, cutoff.UTC())
}

// MarkRawDeleted records that a window's raw segments were deleted.
func (s *Store) MarkRawDeleted(ctx context.Context, w Window) error {
	return s.stamp(ctx, "raw_deleted_at", w)
}

// MarkUnregistered records that a window's compacted partition was removed from
// the table. It is written before the window's objects are deleted, so a pass
// that stops between the two finds the partition already gone and deletes
// what is left.
func (s *Store) MarkUnregistered(ctx context.Context, w Window) error {
	return s.stamp(ctx, "unregistered_at", w)
}

// MarkExpired records that a window is gone: its partition, its resource and
// its raw segments.
func (s *Store) MarkExpired(ctx context.Context, w Window) error {
	return s.stamp(ctx, "expired_at", w)
}

// stamp sets one retention column of a window to now.
func (s *Store) stamp(ctx context.Context, column string, w Window) error {
	var q string
	switch column {
	case "raw_deleted_at":
		q = `UPDATE webhook_windows SET raw_deleted_at = NOW() WHERE source = $1 AND window_start = $2`
	case "unregistered_at":
		q = `UPDATE webhook_windows SET unregistered_at = NOW() WHERE source = $1 AND window_start = $2`
	default:
		q = `UPDATE webhook_windows SET expired_at = NOW(), resource_id = '', location = '' WHERE source = $1 AND window_start = $2`
	}
	res, err := s.db.ExecContext(ctx, q, w.Source, w.Start.UTC())
	if err != nil {
		return fmt.Errorf("recording webhook retention (%s): %w", column, err)
	}
	return oneRow(res)
}

// windows runs a query returning window rows.
func (s *Store) windows(ctx context.Context, q string, args ...any) ([]Window, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("reading webhook windows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanWindows(rows, 0)
}
