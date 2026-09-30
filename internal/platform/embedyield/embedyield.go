// Package embedyield is the deployment-wide half of the embed gate (#1988):
// the embedding.Signal every replica publishes its interactive embeds to and
// every replica's index worker reads before it sends the embedding server
// background work, over the embed_interactive table (migration 000173).
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

// Compile-time interface check.
var _ embedding.Signal = (*Store)(nil)

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
