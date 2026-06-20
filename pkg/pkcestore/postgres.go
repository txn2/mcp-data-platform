package pkcestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// ensure putError satisfies the standard error chain we expect.
var _ error = (*putError)(nil)

// ErrStateCollision is returned by Store.Put when a state token already
// exists in the store. State tokens are 256 bits of entropy so a genuine
// collision is statistically impossible — in practice this means the
// caller's state generator is broken or someone is replaying a state.
// Either way, the second oauth-start must fail loudly rather than
// silently overwriting the first.
var ErrStateCollision = errors.New("pkcestore: state token collision")

// ErrStorePut wraps any DB-side failure from Put. Tests assert with
// errors.Is rather than coupling to a specific message.
var ErrStorePut = errors.New("pkcestore: put failed")

// PostgresStore persists in-flight PKCE state to the oauth_pkce_states
// table. Used in multi-replica deployments where oauth-start may run on
// a different pod than /oauth/callback.
//
// code_verifier is encrypted at rest via the platform's
// connoauth.FieldEncryptor — verifiers are short-lived (≤TTL) but they
// are paired secrets, so a DB read of them while still in flight is
// roughly equivalent to leaking a short-window OAuth refresh token.
//
// We reuse connoauth's encryptor interface rather than declaring a
// duplicate to keep one canonical interface for at-rest field encryption
// across sub-package stores.
type PostgresStore struct {
	db  *sql.DB
	enc connoauth.FieldEncryptor

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewPostgresStore returns a Postgres-backed PKCE store that runs a
// background sweeper to delete expired rows. Pass nil for enc to skip
// at-rest encryption (dev-only).
func NewPostgresStore(db *sql.DB, enc connoauth.FieldEncryptor) *PostgresStore {
	if enc == nil {
		enc = passThroughEncryptor{}
	}
	s := &PostgresStore{db: db, enc: enc, stopCh: make(chan struct{})}
	go s.sweepLoop(gcInterval)
	return s
}

// Put inserts a state row. The expires_at column is computed server-side
// (NOW() + interval matching TTL) so the take-side filter NOW()
// comparison can't be defeated by Go-process / Postgres clock drift.
// ON CONFLICT (state) DO NOTHING rejects collisions loudly via
// ErrStateCollision.
func (s *PostgresStore) Put(ctx context.Context, state string, val *State) error {
	verifierEnc, err := s.enc.Encrypt(val.CodeVerifier)
	if err != nil {
		return &putError{op: "encrypt code_verifier", err: err}
	}
	kind := val.Kind
	if kind == "" {
		// Legacy callers that don't set Kind are MCP-gateway flows by
		// construction (only kind that used this table at the time).
		// Migration 000039's column default is identical; we set the
		// field explicitly here so the take side never sees an empty
		// kind even if a future migration drops the default.
		kind = connoauth.KindMCP
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_pkce_states
            (state, connection, connection_kind, code_verifier, started_by, return_url, redirect_uri, created_at, expires_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW() + ($9 || ' seconds')::interval)
         ON CONFLICT (state) DO NOTHING`,
		state, val.Connection, kind, verifierEnc, val.StartedBy,
		val.ReturnURL, val.RedirectURI, val.CreatedAt,
		fmt.Sprintf("%d", int64(TTL.Seconds())))
	if err != nil {
		return &putError{op: "insert", err: err}
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// Either a real collision (negligible probability with 256-bit
		// state) or the caller's state generator produced a duplicate.
		// Refuse — the operator's flow is broken regardless of cause.
		slog.Error("pkcestore: state token collision rejected",
			"connection", val.Connection)
		return ErrStateCollision
	}
	return nil
}

// putError is a single-line, errors.Is-aware wrapper that preserves both
// the ErrStorePut sentinel and the underlying cause. Used in place of
// errors.Join (which separates messages with newlines and breaks
// line-oriented log aggregators).
type putError struct {
	op  string
	err error
}

func (p *putError) Error() string {
	return fmt.Sprintf("pkcestore: put failed (%s): %v", p.op, p.err)
}

func (p *putError) Unwrap() error { return p.err }

// Is reports both the wrapped error AND the ErrStorePut sentinel as
// matches so callers can errors.Is(err, ErrStorePut).
func (*putError) Is(target error) bool {
	return target == ErrStorePut //nolint:errorlint // sentinel comparison is intentional
}

// Take atomically reads-and-deletes the matching row using DELETE …
// RETURNING so two concurrent callbacks (a real one and a replay)
// race-cleanly: only one wins. Returns ErrStateNotFound for missing or
// expired rows.
func (s *PostgresStore) Take(ctx context.Context, state string) (*State, error) {
	row := s.db.QueryRowContext(ctx,
		`DELETE FROM oauth_pkce_states
         WHERE state = $1 AND expires_at > NOW()
         RETURNING connection, connection_kind, code_verifier, started_by, return_url, redirect_uri, created_at`,
		state)
	var (
		v           State
		verifierEnc string
	)
	if err := row.Scan(&v.Connection, &v.Kind, &verifierEnc, &v.StartedBy,
		&v.ReturnURL, &v.RedirectURI, &v.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStateNotFound
		}
		return nil, fmt.Errorf("pkcestore: take: %w", err)
	}
	verifier, err := s.enc.Decrypt(verifierEnc)
	if err != nil {
		return nil, fmt.Errorf("pkcestore: decrypt code_verifier: %w", err)
	}
	v.CodeVerifier = verifier
	return &v, nil
}

// Close stops the background sweeper. Idempotent.
func (s *PostgresStore) Close() error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

func (s *PostgresStore) sweepLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := s.db.ExecContext(ctx,
				`DELETE FROM oauth_pkce_states WHERE expires_at <= NOW()`); err != nil {
				slog.Warn("pkcestore: sweep failed", "err", err)
			}
			cancel()
		case <-s.stopCh:
			return
		}
	}
}

// passThroughEncryptor is the dev-mode "no encryption" stand-in. Used
// when ENCRYPTION_KEY is unset. The platform separately logs a warning
// so operators don't deploy this path to production unaware.
type passThroughEncryptor struct{}

// Encrypt returns the plaintext unchanged.
func (passThroughEncryptor) Encrypt(s string) (string, error) { return s, nil }

// Decrypt returns the input unchanged.
func (passThroughEncryptor) Decrypt(s string) (string, error) { return s, nil }
