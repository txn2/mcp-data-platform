// Package postgres provides PostgreSQL storage for sessions.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// Store implements session.Store using PostgreSQL.
type Store struct {
	db     *sql.DB
	ttl    time.Duration
	cancel context.CancelFunc
	done   chan struct{}
}

// Config configures the PostgreSQL session store.
type Config struct {
	TTL time.Duration
}

// New creates a new PostgreSQL session store.
func New(db *sql.DB, cfg Config) *Store {
	return &Store{
		db:  db,
		ttl: cfg.TTL,
	}
}

// Create persists a new session.
func (s *Store) Create(ctx context.Context, sess *session.Session) error {
	stateJSON, err := json.Marshal(sess.State)
	if err != nil {
		stateJSON = []byte("{}")
	}

	query := `
		INSERT INTO sessions (id, user_id, created_at, last_active_at, expires_at, state)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err = s.db.ExecContext(ctx, query,
		sess.ID, sess.UserID, sess.CreatedAt, sess.LastActiveAt, sess.ExpiresAt, stateJSON,
	)
	if err != nil {
		return fmt.Errorf("inserting session: %w", err)
	}
	return nil
}

// Get retrieves a session by ID. Returns nil, nil if not found or expired.
func (s *Store) Get(ctx context.Context, id string) (*session.Session, error) {
	query := `
		SELECT id, user_id, created_at, last_active_at, expires_at, state
		FROM sessions
		WHERE id = $1 AND expires_at > NOW()
	`
	row := s.db.QueryRowContext(ctx, query, id)
	return s.scanSession(row)
}

// Touch updates LastActiveAt and extends ExpiresAt by the store's TTL.
func (s *Store) Touch(ctx context.Context, id string) error {
	query := `
		UPDATE sessions
		SET last_active_at = NOW(), expires_at = NOW() + $2::interval
		WHERE id = $1 AND expires_at > NOW()
	`
	_, err := s.db.ExecContext(ctx, query, id, fmt.Sprintf("%d seconds", int(s.ttl.Seconds())))
	if err != nil {
		return fmt.Errorf("touching session: %w", err)
	}
	return nil
}

// Delete removes a session.
func (s *Store) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM sessions WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// List returns all non-expired sessions.
func (s *Store) List(ctx context.Context) ([]*session.Session, error) {
	query := `
		SELECT id, user_id, created_at, last_active_at, expires_at, state
		FROM sessions
		WHERE expires_at > NOW()
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []*session.Session
	for rows.Next() {
		sess, err := s.scanSessionRow(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session rows: %w", err)
	}
	return sessions, nil
}

// UpdateState merges state into the session's State map using JSONB concatenation.
func (s *Store) UpdateState(ctx context.Context, id string, state map[string]any) error {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	query := `
		UPDATE sessions
		SET state = state || $2::jsonb
		WHERE id = $1
	`
	_, err = s.db.ExecContext(ctx, query, id, stateJSON)
	if err != nil {
		return fmt.Errorf("updating session state: %w", err)
	}
	return nil
}

// Cleanup removes expired sessions.
func (s *Store) Cleanup(ctx context.Context) error {
	query := `DELETE FROM sessions WHERE expires_at <= NOW()`
	_, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("cleaning up sessions: %w", err)
	}
	return nil
}

// StartCleanupRoutine starts a background goroutine that periodically removes
// expired sessions. The goroutine is stopped when Close is called.
func (s *Store) StartCleanupRoutine(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Cleanup(ctx); err != nil {
					slog.Warn("session cleanup failed", "error", err)
				}
			}
		}
	}()
}

// Close stops the cleanup goroutine and waits for it to exit.
// It is safe to call Close even if StartCleanupRoutine was never called.
func (s *Store) Close() error {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	return nil
}

// scanSession scans a single row into a Session.
func (*Store) scanSession(row *sql.Row) (*session.Session, error) {
	var sess session.Session
	var stateJSON []byte

	err := row.Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastActiveAt, &sess.ExpiresAt, &stateJSON)
	if err == sql.ErrNoRows {
		return nil, nil //nolint:nilnil // Store interface specifies nil,nil for not-found
	}
	if err != nil {
		return nil, fmt.Errorf("scanning session: %w", err)
	}

	sess.State = make(map[string]any)
	if len(stateJSON) > 0 {
		_ = json.Unmarshal(stateJSON, &sess.State)
	}
	return &sess, nil
}

// scanSessionRow scans a row from sql.Rows into a Session.
func (*Store) scanSessionRow(rows *sql.Rows) (*session.Session, error) {
	var sess session.Session
	var stateJSON []byte

	err := rows.Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastActiveAt, &sess.ExpiresAt, &stateJSON)
	if err != nil {
		return nil, fmt.Errorf("scanning session row: %w", err)
	}

	sess.State = make(map[string]any)
	if len(stateJSON) > 0 {
		_ = json.Unmarshal(stateJSON, &sess.State)
	}
	return &sess, nil
}

// Verify interface compliance.
var _ session.Store = (*Store)(nil)
