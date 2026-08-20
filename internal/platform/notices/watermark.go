package notices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WatermarkStore records, per caller, when a notice digest was last delivered
// to them. Get answers nil (not an error) for a caller who has never been
// briefed; Set is what makes delivery single-shot.
type WatermarkStore interface {
	// Now returns the clock the watermark is kept on.
	//
	// A digest compares its boundary against timestamps the database stamped
	// (portal_shares.created_at defaults to NOW()), so the boundary has to be
	// read from that same clock. Stamping it from the process clock instead
	// compares two clocks: a few milliseconds of skew between the application
	// host and the database server is enough to place an already-delivered
	// share after the watermark and read it out to the caller a second time.
	Now(ctx context.Context) (time.Time, error)
	Get(ctx context.Context, userKey string) (*time.Time, error)
	Set(ctx context.Context, userKey string, at time.Time) error
}

// PostgresWatermarkStore is the user_notice_watermarks-backed WatermarkStore.
type PostgresWatermarkStore struct {
	db *sql.DB
}

// NewPostgresWatermarkStore returns a watermark store over db.
func NewPostgresWatermarkStore(db *sql.DB) *PostgresWatermarkStore {
	return &PostgresWatermarkStore{db: db}
}

// Now returns the database server's current time, which is the clock every
// timestamp a digest compares against was stamped from.
func (s *PostgresWatermarkStore) Now(ctx context.Context) (time.Time, error) {
	var at time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT NOW()`).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("reading the database clock: %w", err)
	}
	return at, nil
}

// Get returns when this caller was last briefed, or nil if never.
func (s *PostgresWatermarkStore) Get(ctx context.Context, userKey string) (*time.Time, error) {
	var at time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT delivered_at FROM user_notice_watermarks WHERE user_key = $1`, userKey).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // never briefed is an absence, not a failure
	}
	if err != nil {
		return nil, fmt.Errorf("reading notice watermark: %w", err)
	}
	return &at, nil
}

// Set advances this caller's watermark to at. It never moves the watermark
// backwards: two sessions can brief the same person concurrently, and the later
// delivery is the one that decides what the next session has already seen.
func (s *PostgresWatermarkStore) Set(ctx context.Context, userKey string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_notice_watermarks (user_key, delivered_at)
		 VALUES ($1, $2)
		 ON CONFLICT (user_key) DO UPDATE
		   SET delivered_at = EXCLUDED.delivered_at, updated_at = NOW()
		   WHERE user_notice_watermarks.delivered_at < EXCLUDED.delivered_at`,
		userKey, at)
	if err != nil {
		return fmt.Errorf("advancing notice watermark: %w", err)
	}
	return nil
}
