package whstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// spoolStateKept is how long a replica's spool state is kept after its last
// report: long enough to show a replica that stopped with events waiting.
const spoolStateKept = 7 * 24 * time.Hour

// SpoolState is one receiver's local spool as it last reported it (#2121):
// what waits to be drained to object storage, and how the drain is going.
type SpoolState struct {
	Replica string
	Dir     string
	Bytes   int64
	Events  int64
	Files   int
	// Oldest is when the oldest event waiting was received; nil when the
	// spool is empty.
	Oldest   *time.Time
	MaxBytes int64
	MaxAge   time.Duration
	// Drained and DrainFailures count since the receiver started.
	Drained       int64
	DrainFailures int64
	LastDrainAt   *time.Time
	LastError     string
	StartedAt     time.Time
	UpdatedAt     time.Time
}

// RecordSpool writes a receiver's spool state, replacing what it wrote last,
// and removes the state of replicas that stopped reporting a week ago.
func (s *Store) RecordSpool(ctx context.Context, st SpoolState) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_spool_state
		        (replica, dir, bytes, events, files, oldest_at, max_bytes, max_age_seconds,
		         drained_events, drain_failures, last_drain_at, last_error, started_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 ON CONFLICT (replica) DO UPDATE SET
		    dir = EXCLUDED.dir, bytes = EXCLUDED.bytes, events = EXCLUDED.events, files = EXCLUDED.files,
		    oldest_at = EXCLUDED.oldest_at, max_bytes = EXCLUDED.max_bytes,
		    max_age_seconds = EXCLUDED.max_age_seconds, drained_events = EXCLUDED.drained_events,
		    drain_failures = EXCLUDED.drain_failures, last_drain_at = EXCLUDED.last_drain_at,
		    last_error = EXCLUDED.last_error, started_at = EXCLUDED.started_at, updated_at = EXCLUDED.updated_at`,
		st.Replica, st.Dir, st.Bytes, st.Events, st.Files, utcPtr(st.Oldest), st.MaxBytes,
		int64(st.MaxAge/time.Second), st.Drained, st.DrainFailures, utcPtr(st.LastDrainAt), st.LastError,
		st.StartedAt.UTC(), st.UpdatedAt.UTC()); err != nil {
		return fmt.Errorf("recording the webhook spool state: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM webhook_spool_state WHERE updated_at < $1`, st.UpdatedAt.Add(-spoolStateKept).UTC()); err != nil {
		return fmt.Errorf("removing stale webhook spool states: %w", err)
	}
	return nil
}

// SpoolStates reads every receiver's spool state, by replica.
func (s *Store) SpoolStates(ctx context.Context) ([]SpoolState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT replica, dir, bytes, events, files, oldest_at, max_bytes, max_age_seconds,
		        drained_events, drain_failures, last_drain_at, last_error, started_at, updated_at
		   FROM webhook_spool_state ORDER BY replica`)
	if err != nil {
		return nil, fmt.Errorf("reading webhook spool states: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only
	out := make([]SpoolState, 0)
	for rows.Next() {
		var (
			st              SpoolState
			oldest, drained sql.NullTime
			maxAge          int64
		)
		if err := rows.Scan(&st.Replica, &st.Dir, &st.Bytes, &st.Events, &st.Files, &oldest, &st.MaxBytes,
			&maxAge, &st.Drained, &st.DrainFailures, &drained, &st.LastError, &st.StartedAt, &st.UpdatedAt); err != nil {
			return nil, fmt.Errorf("reading a webhook spool state: %w", err)
		}
		st.MaxAge = time.Duration(maxAge) * time.Second
		if oldest.Valid {
			st.Oldest = &oldest.Time
		}
		if drained.Valid {
			st.LastDrainAt = &drained.Time
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading webhook spool states: %w", err)
	}
	return out, nil
}

// utcPtr is t in UTC, or nil.
func utcPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
