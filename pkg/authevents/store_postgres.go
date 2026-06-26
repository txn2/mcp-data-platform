package authevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// PostgresStore writes connection_auth_events rows via the supplied
// *sql.DB. The schema is migration 000040.
type PostgresStore struct {
	db *sql.DB

	pruneCancel context.CancelFunc
	pruneDone   chan struct{}
}

// NewPostgresStore wires a Store to the platform's database.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// StartPruneRoutine launches a background goroutine that calls Prune
// once every `interval` with cutoff = now() - retention. The goroutine
// is stopped by Close. interval is typically 24h in production;
// retention is typically 90d. The first prune fires after one
// interval so a freshly-started replica doesn't immediately churn the
// DB.
func (s *PostgresStore) StartPruneRoutine(interval, retention time.Duration) {
	if s.pruneCancel != nil {
		// Already started — idempotent. The lifecycle hook may call
		// this on re-init paths during dev hot-reload.
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.pruneCancel = cancel
	s.pruneDone = make(chan struct{})
	go s.pruneLoop(ctx, interval, retention)
}

// Close stops the prune goroutine and waits for it. Safe to call when
// StartPruneRoutine was never called.
func (s *PostgresStore) Close() error {
	if s.pruneCancel == nil {
		return nil
	}
	s.pruneCancel()
	<-s.pruneDone
	s.pruneCancel = nil
	return nil
}

func (s *PostgresStore) pruneLoop(ctx context.Context, interval, retention time.Duration) {
	defer close(s.pruneDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-retention)
			n, err := s.Prune(ctx, cutoff)
			if err != nil {
				slog.Warn("authevents: prune failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("authevents: prune complete", "removed", n,
					"retention", retention)
			}
		}
	}
}

// Insert appends ev. RETURNING id populates ev.ID server-side so the
// caller can reference the row in subsequent logs.
func (s *PostgresStore) Insert(ctx context.Context, ev Event) error {
	if !ev.IsValid() {
		return ErrInvalidEvent
	}
	detail := ev.Detail
	if len(detail) == 0 {
		detail = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO connection_auth_events
		    (connection_kind, connection_name, event_type, actor, idp_host, detail)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		ev.Kind, ev.Name, string(ev.Type), ev.Actor, ev.IDPHost, []byte(detail))
	if err != nil {
		return fmt.Errorf("authevents: insert: %w", err)
	}
	return nil
}

// List runs a per-filter query against the (kind, name, occurred_at)
// index and decodes each row into an Event. ORDER BY occurred_at DESC
// matches the index direction so the query is index-only.
func (s *PostgresStore) List(ctx context.Context, f Filter) ([]Event, error) {
	if f.Limit <= 0 {
		return nil, fmt.Errorf("authevents: List requires positive Limit")
	}
	if f.Limit > maxListLimit {
		f.Limit = maxListLimit
	}
	args := []any{f.Limit}
	q := `SELECT id, occurred_at, connection_kind, connection_name,
	             event_type, actor, idp_host, detail
	        FROM connection_auth_events
	       WHERE 1=1`
	// #nosec G202 -- fragments below are static format strings with
	// $N placeholders; user input never lands in the query, only
	// in args. gosec's pattern match can't see that distinction.
	if f.Kind != "" {
		args = append(args, f.Kind)
		q += fmt.Sprintf(" AND connection_kind = $%d", len(args)) //nolint:gosec // see G202 note
	}
	if f.Name != "" {
		args = append(args, f.Name)
		q += fmt.Sprintf(" AND connection_name = $%d", len(args)) //nolint:gosec // see G202 note
	}
	if !f.Since.IsZero() {
		args = append(args, f.Since)
		// #nosec G202 -- only a $N placeholder is formatted; user input goes to args
		q += fmt.Sprintf(" AND occurred_at >= $%d", len(args)) //nolint:gosec // see G202 note
	}
	q += " ORDER BY occurred_at DESC LIMIT $1"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("authevents: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Event, 0, f.Limit)
	for rows.Next() {
		ev, err := s.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authevents: list iterate: %w", err)
	}
	return out, nil
}

// scanRow extracts one row from a *sql.Rows. Extracted from List so
// the row-mapping is testable in isolation and List's cyclomatic
// complexity stays under the project ceiling.
func (*PostgresStore) scanRow(rows *sql.Rows) (Event, error) {
	var (
		ev      Event
		typeStr string
		detail  []byte
	)
	if err := rows.Scan(&ev.ID, &ev.OccurredAt, &ev.Kind, &ev.Name,
		&typeStr, &ev.Actor, &ev.IDPHost, &detail); err != nil {
		return Event{}, fmt.Errorf("authevents: scan: %w", err)
	}
	ev.Type = Type(typeStr)
	if len(detail) > 0 {
		ev.Detail = json.RawMessage(detail)
	}
	return ev, nil
}

// Prune deletes rows whose occurred_at < cutoff. Returns the rowcount
// so the caller can log the daily prune size.
func (s *PostgresStore) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM connection_auth_events WHERE occurred_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("authevents: prune: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("authevents: prune rows affected: %w", err)
	}
	return n, nil
}

// Verify interface compliance.
var _ Store = (*PostgresStore)(nil)
