package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
)

// ensure putError satisfies the standard error chain we expect.
var _ error = (*putError)(nil)

// ErrPKCEStateCollision is returned by PKCEStore.Put when a state token
// already exists in the store. State tokens are 256 bits of entropy so a
// genuine collision is statistically impossible — in practice this means
// generatePKCEState is broken or someone is replaying a state. Either
// way, the second oauth-start must fail loudly rather than silently
// overwriting the first.
var ErrPKCEStateCollision = errors.New("admin: pkce state token collision")

// ErrPKCEStorePut wraps any DB-side failure from Put. Tests assert with
// errors.Is rather than coupling to a specific message.
var ErrPKCEStorePut = errors.New("admin: pkce store put failed")

// PostgresPKCEStore persists in-flight PKCE state to the
// oauth_pkce_states table. Used in multi-replica deployments where
// oauth-start may run on a different pod than /oauth/callback.
//
// code_verifier is encrypted at rest via the platform's
// gatewaykit.FieldEncryptor — verifiers are short-lived (≤pkceTTL) but
// they are paired secrets, so a DB read of them while still in flight
// is roughly equivalent to leaking a short-window OAuth refresh token.
//
// We reuse gatewaykit's encryptor interface rather than declaring a
// duplicate to keep one canonical interface for at-rest field
// encryption across sub-package stores.
type PostgresPKCEStore struct {
	db  *sql.DB
	enc gatewaykit.FieldEncryptor

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewPostgresPKCEStore returns a Postgres-backed PKCE store that runs
// a background sweeper to delete expired rows. Pass nil for enc to
// skip at-rest encryption (dev-only).
func NewPostgresPKCEStore(db *sql.DB, enc gatewaykit.FieldEncryptor) *PostgresPKCEStore {
	if enc == nil {
		enc = passThroughPKCEEncryptor{}
	}
	s := &PostgresPKCEStore{db: db, enc: enc, stopCh: make(chan struct{})}
	go s.sweepLoop(pkceGCInterval)
	return s
}

// Put inserts a state row. The expires_at column is computed
// server-side (NOW() + interval matching pkceTTL) so the take-side
// filter NOW() comparison can't be defeated by Go-process / Postgres
// clock drift. ON CONFLICT (state) DO NOTHING rejects collisions
// loudly via ErrPKCEStateCollision.
func (s *PostgresPKCEStore) Put(ctx context.Context, state string, val *PKCEState) error {
	verifierEnc, err := s.enc.Encrypt(val.codeVerifier)
	if err != nil {
		return &putError{op: "encrypt code_verifier", err: err}
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_pkce_states
            (state, connection, code_verifier, started_by, return_url, redirect_uri, created_at, expires_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, NOW() + ($8 || ' seconds')::interval)
         ON CONFLICT (state) DO NOTHING`,
		state, val.connection, verifierEnc, val.startedBy,
		val.returnURL, val.redirectURI, val.createdAt,
		fmt.Sprintf("%d", int64(pkceTTL.Seconds())))
	if err != nil {
		return &putError{op: "insert", err: err}
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Either a real collision (negligible probability with 256-bit
		// state) or generatePKCEState produced a duplicate. Refuse —
		// the operator's flow is broken regardless of cause.
		slog.Error("pkce_store: state token collision rejected",
			"connection", val.connection)
		return ErrPKCEStateCollision
	}
	return nil
}

// putError is a single-line, errors.Is-aware wrapper that preserves
// both the ErrPKCEStorePut sentinel and the underlying cause. Used in
// place of errors.Join (which separates messages with newlines and
// breaks line-oriented log aggregators).
type putError struct {
	op  string
	err error
}

func (p *putError) Error() string {
	return fmt.Sprintf("admin: pkce store put failed (%s): %v", p.op, p.err)
}

func (p *putError) Unwrap() error { return p.err }

// Is reports both the wrapped error AND the ErrPKCEStorePut sentinel
// as matches so callers can errors.Is(err, ErrPKCEStorePut).
func (*putError) Is(target error) bool {
	return target == ErrPKCEStorePut //nolint:errorlint // sentinel comparison is intentional
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
	var (
		v           PKCEState
		verifierEnc string
	)
	if err := row.Scan(&v.connection, &verifierEnc, &v.startedBy,
		&v.returnURL, &v.redirectURI, &v.createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPKCEStateNotFound
		}
		return nil, fmt.Errorf("pkce_store: take: %w", err)
	}
	verifier, err := s.enc.Decrypt(verifierEnc)
	if err != nil {
		return nil, fmt.Errorf("pkce_store: decrypt code_verifier: %w", err)
	}
	v.codeVerifier = verifier
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

// passThroughPKCEEncryptor is the dev-mode "no encryption" stand-in.
// Used when ENCRYPTION_KEY is unset. The platform separately logs a
// warning so operators don't deploy this path to production unaware.
type passThroughPKCEEncryptor struct{}

// Encrypt returns the plaintext unchanged.
func (passThroughPKCEEncryptor) Encrypt(s string) (string, error) { return s, nil }

// Decrypt returns the input unchanged.
func (passThroughPKCEEncryptor) Decrypt(s string) (string, error) { return s, nil }
