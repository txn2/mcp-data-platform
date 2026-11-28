// Package embedyield is the deployment-wide half of the embed gate (#1988):
// the embedding.Signal every replica publishes its interactive embeds to and
// every replica's index worker reads before it sends the embedding server
// background work, over the embed_interactive table (migration 000173); and
// the embedding.BackgroundSignal every replica's index worker publishes the
// background input it has at the server to and every replica's search reads
// before it bounds its embed, over the embed_background table (migration
// 000274, #2846).
//
// A replica is one row, keyed by an id this process draws at startup, present
// while the replica has an interactive embed in flight. Rows are small and
// short-lived: a clear deletes the replica's row, and with it any row a
// stopped replica left past its hold.
package embedyield

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// DefaultHold is how long a mark holds without being cleared: the request-path
// embed timeout, so a replica that stops mid-embed is waited on no longer than
// its embed could have lasted.
const DefaultHold = embedding.DefaultTimeout * time.Second

// Store is the embed_interactive table as an embedding.Signal.
type Store struct {
	db       *sql.DB
	instance string
	hold     time.Duration
}

// Compile-time interface checks.
var (
	_ embedding.Signal           = (*Store)(nil)
	_ embedding.BackgroundSignal = (*Store)(nil)
)

// New returns the signal for this process over db, marking for hold
// (DefaultHold when not positive). The process's id is random, so two
// replicas never share a row.
func New(db *sql.DB, hold time.Duration) *Store {
	if hold <= 0 {
		hold = DefaultHold
	}
	return &Store{db: db, instance: rand.Text(), hold: hold}
}

// Mark says this replica has an interactive embed in flight until the hold
// runs out.
func (s *Store) Mark(ctx context.Context) error {
	const q = `
		INSERT INTO embed_interactive (instance, until)
		VALUES ($1, now() + make_interval(secs => $2))
		ON CONFLICT (instance) DO UPDATE SET until = EXCLUDED.until`
	if _, err := s.db.ExecContext(ctx, q, s.instance, s.hold.Seconds()); err != nil {
		return fmt.Errorf("embedyield: mark: %w", err)
	}
	return nil
}

// Clear says this replica has no interactive embed in flight, and removes any
// mark a stopped replica left past its hold.
func (s *Store) Clear(ctx context.Context) error {
	const q = `DELETE FROM embed_interactive WHERE instance = $1 OR until < now()`
	if _, err := s.db.ExecContext(ctx, q, s.instance); err != nil {
		return fmt.Errorf("embedyield: clear: %w", err)
	}
	return nil
}

// Busy reports whether any replica, this one included, has an interactive
// embed in flight.
func (s *Store) Busy(ctx context.Context) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM embed_interactive WHERE until > now())`
	var busy bool
	if err := s.db.QueryRowContext(ctx, q).Scan(&busy); err != nil {
		return false, fmt.Errorf("embedyield: busy: %w", err)
	}
	return busy, nil
}

// MarkBackground says this replica has a background call at the embedding
// server whose largest input is expected to take input, until
// embedding.DefaultBackgroundHold runs out.
func (s *Store) MarkBackground(ctx context.Context, input time.Duration) error {
	const q = `
		INSERT INTO embed_background (instance, input_ms, until)
		VALUES ($1, $2, now() + make_interval(secs => $3))
		ON CONFLICT (instance) DO UPDATE SET input_ms = EXCLUDED.input_ms, until = EXCLUDED.until`
	if _, err := s.db.ExecContext(ctx, q, s.instance, input.Milliseconds(), embedding.DefaultBackgroundHold.Seconds()); err != nil {
		return fmt.Errorf("embedyield: mark background: %w", err)
	}
	return nil
}

// ClearBackground says this replica has no background call at the server, and
// removes any mark a stopped replica left past its hold.
func (s *Store) ClearBackground(ctx context.Context) error {
	const q = `DELETE FROM embed_background WHERE instance = $1 OR until < now()`
	if _, err := s.db.ExecContext(ctx, q, s.instance); err != nil {
		return fmt.Errorf("embedyield: clear background: %w", err)
	}
	return nil
}

// BackgroundInput is how long the embedding server needs to finish the
// background inputs every replica has at it now: the sum of the live marks,
// since the server serves them one after another.
func (s *Store) BackgroundInput(ctx context.Context) (time.Duration, error) {
	const q = `SELECT COALESCE(SUM(input_ms), 0) FROM embed_background WHERE until > now()`
	var ms int64
	if err := s.db.QueryRowContext(ctx, q).Scan(&ms); err != nil {
		return 0, fmt.Errorf("embedyield: background input: %w", err)
	}
	return time.Duration(ms) * time.Millisecond, nil
}
