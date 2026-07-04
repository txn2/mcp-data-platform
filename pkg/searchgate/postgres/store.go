// Package postgres provides a PostgreSQL-backed searchgate.Store so the
// search-first gate's per-session discovery signal is shared across replicas.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store implements searchgate.Store using PostgreSQL.
type Store struct {
	db  *sql.DB
	ttl time.Duration
}

// New creates a PostgreSQL discovery store. Discovery records expire ttl after
// the most recent MarkDiscovered.
func New(db *sql.DB, ttl time.Duration) *Store {
	return &Store{db: db, ttl: ttl}
}

// MarkDiscovered records discovery for the session, upserting its expiry.
func (s *Store) MarkDiscovered(ctx context.Context, sessionID string) error {
	const q = `
		INSERT INTO search_gate_discovery (session_id, discovered_at, expires_at)
		VALUES ($1, now(), now() + $2::interval)
		ON CONFLICT (session_id) DO UPDATE SET
			discovered_at = EXCLUDED.discovered_at,
			expires_at = EXCLUDED.expires_at
	`
	if _, err := s.db.ExecContext(ctx, q, sessionID, intervalString(s.ttl)); err != nil {
		return fmt.Errorf("marking discovery: %w", err)
	}
	return nil
}

// HasDiscovered reports whether the session has a non-expired discovery record.
func (s *Store) HasDiscovered(ctx context.Context, sessionID string) (bool, error) {
	const q = `SELECT EXISTS (
		SELECT 1 FROM search_gate_discovery
		WHERE session_id = $1 AND expires_at > now()
	)`
	var exists bool
	if err := s.db.QueryRowContext(ctx, q, sessionID).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking discovery: %w", err)
	}
	return exists, nil
}

// Cleanup deletes expired discovery records.
func (s *Store) Cleanup(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_gate_discovery WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("cleaning up discovery: %w", err)
	}
	return nil
}

// Close is a no-op; the underlying *sql.DB is owned by the platform.
func (*Store) Close() error { return nil }

// intervalString renders a duration as a PostgreSQL interval literal (seconds),
// so the TTL is applied server-side against now() rather than a client clock.
func intervalString(d time.Duration) string {
	secs := max(int64(d/time.Second), 1)
	return fmt.Sprintf("%d seconds", secs)
}
