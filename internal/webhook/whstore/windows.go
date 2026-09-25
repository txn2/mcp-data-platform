// Package whstore is the control data of inbound webhooks (#1870): which
// compaction windows of each source have segments, which of them are compacted and where, what
// retention has done to them, and the request counts and rejections a
// source's page reports. Events are never written here.
package whstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store reads and writes the webhook control tables.
type Store struct {
	db *sql.DB
}

// New creates a store over the platform database.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Window is one compaction window of one source.
type Window struct {
	Source string
	Start  time.Time
	Length time.Duration
	// Generation is the window's segment counter when it was read. Compacting
	// it records this number, so a segment recorded while the compaction ran
	// leaves the window owed another one.
	Generation int64
	// ResourceID and Location are the last compaction's resource and the
	// directory its partition is registered at, empty before the first.
	ResourceID string
	Location   string
	Attempts   int
}

// MarkSegment records that a segment for the window starting at start and
// length long was written. A window already compacted becomes owed another
// compaction, because its Parquet file no longer holds every event its
// segments do. A window recorded with a shorter length is lengthened: a
// source whose compact_every was raised can land a segment in a window the
// old setting had already ended.
func (s *Store) MarkSegment(ctx context.Context, source string, start time.Time, length time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_windows (source, window_start, window_seconds, generation, last_segment_at)
		 VALUES ($1, $2, $3, 1, NOW())
		 ON CONFLICT (source, window_start) DO UPDATE
		    SET generation = webhook_windows.generation + 1,
		        window_seconds = GREATEST(webhook_windows.window_seconds, EXCLUDED.window_seconds),
		        last_segment_at = NOW()`,
		source, start.UTC(), int(length.Seconds()))
	if err != nil {
		return fmt.Errorf("recording webhook segment: %w", err)
	}
	return nil
}

// ClaimOwed claims up to limit windows owed a compaction that ended at or
// before endedBy, for lease. A window another replica holds is skipped until
// its lease runs out, and a window that failed is held back until the backoff
// recorded against it passes.
func (s *Store) ClaimOwed(ctx context.Context, endedBy time.Time, lease time.Duration, limit int) ([]Window, error) {
	rows, err := s.db.QueryContext(ctx,
		`UPDATE webhook_windows h
		    SET claimed_until = NOW() + make_interval(secs => $2),
		        attempts = h.attempts + 1
		  WHERE (h.source, h.window_start) IN (
		        SELECT source, window_start FROM webhook_windows
		         WHERE generation <> compacted_generation
		           AND expired_at IS NULL
		           AND window_start + make_interval(secs => window_seconds) <= $1
		           AND (claimed_until IS NULL OR claimed_until < NOW())
		         ORDER BY window_start
		         LIMIT $3
		           FOR UPDATE SKIP LOCKED)
		 RETURNING h.source, h.window_start, h.window_seconds, h.generation, h.resource_id, h.location, h.attempts`,
		endedBy.UTC(), lease.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("claiming webhook windows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanWindows(rows, limit)
}

// scanWindows reads rows of source, window_start, window_seconds,
// generation, resource_id, location and attempts.
func scanWindows(rows *sql.Rows, capacity int) ([]Window, error) {
	out := make([]Window, 0, capacity)
	for rows.Next() {
		var (
			w       Window
			seconds int64
		)
		if err := rows.Scan(&w.Source, &w.Start, &seconds, &w.Generation, &w.ResourceID, &w.Location, &w.Attempts); err != nil {
			return nil, fmt.Errorf("scanning webhook window: %w", err)
		}
		w.Start = w.Start.UTC()
		w.Length = time.Duration(seconds) * time.Second
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook windows: %w", err)
	}
	return out, nil
}

// Compaction is what one compaction of a window produced.
type Compaction struct {
	Segments   int
	Events     int64
	Duplicates int64
	Digest     string
	ResourceID string
	Location   string
}

// RecordCompacted records a compaction of w at the generation it was claimed
// at, and releases the claim. If a segment was recorded since the claim, the
// window's generation is ahead of the one recorded here and it is claimed
// again.
func (s *Store) RecordCompacted(ctx context.Context, w Window, c Compaction) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhook_windows
		    SET compacted_generation = $3, compacted_at = NOW(), segments = $4, events = $5,
		        duplicates = $6, digest = $7, resource_id = $8, location = $9,
		        claimed_until = NULL, attempts = 0, last_error = ''
		  WHERE source = $1 AND window_start = $2`,
		w.Source, w.Start.UTC(), w.Generation, c.Segments, c.Events, c.Duplicates, c.Digest,
		c.ResourceID, c.Location)
	if err != nil {
		return fmt.Errorf("recording webhook compaction: %w", err)
	}
	return oneRow(res)
}

// RecordLocation records the resource and partition location of a window
// before its compaction is recorded, so a failure after the partition moved
// leaves the record describing where the partition is.
func (s *Store) RecordLocation(ctx context.Context, w Window, resourceID, location string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhook_windows SET resource_id = $3, location = $4 WHERE source = $1 AND window_start = $2`,
		w.Source, w.Start.UTC(), resourceID, location)
	if err != nil {
		return fmt.Errorf("recording webhook window location: %w", err)
	}
	return oneRow(res)
}

// RecordFailure keeps why a compaction of w failed and holds the window back
// for hold.
func (s *Store) RecordFailure(ctx context.Context, w Window, reason string, hold time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webhook_windows
		    SET last_error = $3, claimed_until = NOW() + make_interval(secs => $4)
		  WHERE source = $1 AND window_start = $2`,
		w.Source, w.Start.UTC(), reason, hold.Seconds())
	if err != nil {
		return fmt.Errorf("recording webhook compaction failure: %w", err)
	}
	return nil
}

// ErrNotFound is returned when a write names a window no row holds.
var ErrNotFound = errors.New("webhook window not found")

// oneRow maps a write that matched nothing to ErrNotFound.
func oneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
