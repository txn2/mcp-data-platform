package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PostgresPKCEStore persists in-flight PKCE state to the
// oauth_pkce_states table. Used in multi-replica deployments where
// oauth-start may run on a different pod than /oauth/callback.
type PostgresPKCEStore struct {
	db *sql.DB

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewPostgresPKCEStore returns a Postgres-backed PKCE store that runs
// a background sweeper to delete expired rows.
func NewPostgresPKCEStore(db *sql.DB) *PostgresPKCEStore {
	s := &PostgresPKCEStore{db: db, stopCh: make(chan struct{})}
	go s.sweepLoop(pkceGCInterval)
	return s
}

// Put inserts (or overwrites) a state row.
func (s *PostgresPKCEStore) Put(ctx context.Context, state string, val *PKCEState) error {
	expiresAt := val.createdAt.Add(pkceTTL)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_pkce_states
            (state, connection, code_verifier, started_by, return_url, redirect_uri, created_at, expires_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (state) DO UPDATE
         SET connection    = EXCLUDED.connection,
             code_verifier = EXCLUDED.code_verifier,
             started_by    = EXCLUDED.started_by,
             return_url    = EXCLUDED.return_url,
             redirect_uri  = EXCLUDED.redirect_uri,
             created_at    = EXCLUDED.created_at,
             expires_at    = EXCLUDED.expires_at`,
		state, val.connection, val.codeVerifier, val.startedBy,
		val.returnURL, val.redirectURI, val.createdAt, expiresAt)
	if err != nil {
		return fmt.Errorf("pkce_store: put: %w", err)
	}
	return nil
}

// Take atomically reads-and-deletes the matching row using DELETE …
// RETURNING so two concurrent callbacks (a real one and a replay)
// race-cleanly: only one wins. Returns ErrPKCEStateNotFound for
// missing or expired rows.
func (s *PostgresPKCEStore) Take(ctx context.Context, state string) (*PKCEState, error) {
	row := s.db.QueryRowContext(ctx,
		`DELETE FROM oauth_pkce_states
         WHERE state = $1 AND expires_at > NOW()
         RETURNING connection, code_verifier, started_by, return_url, redirect_uri, created_at`,
		state)
	var v PKCEState
	if err := row.Scan(&v.connection, &v.codeVerifier, &v.startedBy,
		&v.returnURL, &v.redirectURI, &v.createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPKCEStateNotFound
		}
		return nil, fmt.Errorf("pkce_store: take: %w", err)
	}
	return &v, nil
}

// Close stops the background sweeper. Idempotent.
func (s *PostgresPKCEStore) Close() error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

func (s *PostgresPKCEStore) sweepLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := s.db.ExecContext(ctx,
				`DELETE FROM oauth_pkce_states WHERE expires_at <= NOW()`); err != nil {
				slog.Warn("pkce_store: sweep failed", "err", err)
			}
			cancel()
		case <-s.stopCh:
			return
		}
	}
}
