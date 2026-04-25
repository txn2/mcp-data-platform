package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrTokenNotFound is returned by TokenStore.Get when no token row exists
// for a given connection. Callers treat this as "needs reauthentication"
// and surface a Connect button in the admin UI.
var ErrTokenNotFound = errors.New("gateway: oauth token not found")

// PersistedToken is the row shape for gateway_oauth_tokens. Tokens are
// stored encrypted at rest by the platform's FieldEncryptor; this struct
// carries plaintext values to/from the store API.
type PersistedToken struct {
	ConnectionName  string
	AccessToken     string
	RefreshToken    string
	ExpiresAt       time.Time
	Scope           string
	AuthenticatedBy string
	AuthenticatedAt time.Time
	UpdatedAt       time.Time
}

// TokenStore persists OAuth tokens for the authorization_code grant so a
// one-time browser-based authentication grants long-running background
// access (cron jobs, scheduled workloads). v1 stores a single shared
// identity per connection.
type TokenStore interface {
	// Get returns the persisted token for a connection or
	// ErrTokenNotFound when none exists.
	Get(ctx context.Context, connection string) (*PersistedToken, error)
	// Set inserts or replaces the token row for a connection.
	Set(ctx context.Context, t PersistedToken) error
	// Delete removes the token row, forcing a re-auth on the next call.
	Delete(ctx context.Context, connection string) error
}

// FieldEncryptor abstracts the platform's at-rest field encryption so
// this package doesn't import pkg/platform (which would create a
// cycle). The same interface shape is reused by other sub-package
// stores that need at-rest encryption (e.g. admin's PKCE state).
//
// TokenEncryptor is the legacy alias retained for callers that still
// reference it; new code should use FieldEncryptor.
type FieldEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// TokenEncryptor is an alias for FieldEncryptor kept for backward
// compatibility with the original gateway-token-store API.
type TokenEncryptor = FieldEncryptor

// noopEncryptor is used when ENCRYPTION_KEY is unset. Storing OAuth
// refresh tokens unencrypted is poor practice; the admin UI surfaces a
// warning when this code path is active so operators know to set the
// key.
type noopEncryptor struct{}

// Encrypt returns the plaintext unchanged.
func (noopEncryptor) Encrypt(s string) (string, error) { return s, nil }

// Decrypt returns the input unchanged.
func (noopEncryptor) Decrypt(s string) (string, error) { return s, nil }

// PostgresTokenStore is a sql-backed TokenStore. Pass enc=nil to disable
// at-rest encryption (refresh tokens stored in plain text — only
// acceptable for dev environments).
type PostgresTokenStore struct {
	db  *sql.DB
	enc TokenEncryptor
}

// NewPostgresTokenStore wires a TokenStore to the given database. enc
// may be nil; if so a no-op encryptor is used and the WARNING is logged
// once per process by the platform on startup.
func NewPostgresTokenStore(db *sql.DB, enc TokenEncryptor) *PostgresTokenStore {
	if enc == nil {
		enc = noopEncryptor{}
	}
	return &PostgresTokenStore{db: db, enc: enc}
}

// Get returns the persisted token for connection or ErrTokenNotFound.
func (s *PostgresTokenStore) Get(ctx context.Context, connection string) (*PersistedToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT connection_name, access_token, refresh_token, expires_at,
                scope, authenticated_by, authenticated_at, updated_at
         FROM gateway_oauth_tokens WHERE connection_name = $1`, connection)

	var (
		t          PersistedToken
		accessEnc  sql.NullString
		refreshEnc sql.NullString
		expiresAt  sql.NullTime
		authedAt   sql.NullTime
	)
	if err := row.Scan(&t.ConnectionName, &accessEnc, &refreshEnc, &expiresAt,
		&t.Scope, &t.AuthenticatedBy, &authedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("gateway: scan oauth token: %w", err)
	}

	if accessEnc.Valid {
		dec, derr := s.enc.Decrypt(accessEnc.String)
		if derr != nil {
			return nil, fmt.Errorf("gateway: decrypt access_token for %s: %w", connection, derr)
		}
		t.AccessToken = dec
	}
	if refreshEnc.Valid {
		dec, derr := s.enc.Decrypt(refreshEnc.String)
		if derr != nil {
			return nil, fmt.Errorf("gateway: decrypt refresh_token for %s: %w", connection, derr)
		}
		t.RefreshToken = dec
	}
	if expiresAt.Valid {
		t.ExpiresAt = expiresAt.Time
	}
	if authedAt.Valid {
		t.AuthenticatedAt = authedAt.Time
	}
	return &t, nil
}

// Set inserts or replaces the token row. Sensitive fields are encrypted
// before persistence.
func (s *PostgresTokenStore) Set(ctx context.Context, t PersistedToken) error {
	accessEnc, err := s.enc.Encrypt(t.AccessToken)
	if err != nil {
		return fmt.Errorf("gateway: encrypt access_token: %w", err)
	}
	refreshEnc, err := s.enc.Encrypt(t.RefreshToken)
	if err != nil {
		return fmt.Errorf("gateway: encrypt refresh_token: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO gateway_oauth_tokens
            (connection_name, access_token, refresh_token, expires_at,
             scope, authenticated_by, authenticated_at, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
         ON CONFLICT (connection_name) DO UPDATE
         SET access_token = $2, refresh_token = $3, expires_at = $4,
             scope = $5, authenticated_by = $6, authenticated_at = $7,
             updated_at = $8`,
		t.ConnectionName, accessEnc, refreshEnc, nullTime(t.ExpiresAt),
		t.Scope, t.AuthenticatedBy, nullTime(t.AuthenticatedAt), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("gateway: upsert oauth token: %w", err)
	}
	return nil
}

// Delete removes the token row, forcing the next call to surface
// "needs reauthentication" until the operator clicks Connect again.
func (s *PostgresTokenStore) Delete(ctx context.Context, connection string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM gateway_oauth_tokens WHERE connection_name = $1`, connection); err != nil {
		return fmt.Errorf("gateway: delete oauth token: %w", err)
	}
	return nil
}

// nullTime converts a Go time.Time into a sql.NullTime, treating zero
// values as SQL NULL (avoids storing the Go epoch as a real timestamp).
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// memoryTokenStore is a process-local store used in tests and as a
// placeholder when no database is configured. Tokens DO NOT survive
// restarts.
type memoryTokenStore struct {
	tokens map[string]PersistedToken
}

// NewMemoryTokenStore returns a TokenStore that lives only in process
// memory. Useful for tests; production deployments use PostgresTokenStore.
func NewMemoryTokenStore() TokenStore {
	return &memoryTokenStore{tokens: map[string]PersistedToken{}}
}

// Get returns the in-memory token for connection or ErrTokenNotFound.
func (s *memoryTokenStore) Get(_ context.Context, connection string) (*PersistedToken, error) {
	t, ok := s.tokens[connection]
	if !ok {
		return nil, ErrTokenNotFound
	}
	return &t, nil
}

// Set stores a token row in process memory, stamping UpdatedAt.
func (s *memoryTokenStore) Set(_ context.Context, t PersistedToken) error {
	t.UpdatedAt = time.Now().UTC()
	s.tokens[t.ConnectionName] = t
	return nil
}

// Delete removes the in-memory token row for connection.
func (s *memoryTokenStore) Delete(_ context.Context, connection string) error {
	delete(s.tokens, connection)
	return nil
}
