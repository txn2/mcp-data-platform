package apigateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrTokenNotFound is returned by TokenStore.Get when no row exists
// for a connection. Distinct from a transport error so callers can
// distinguish a never-authorized connection from a database that is
// merely unreachable.
var ErrTokenNotFound = errors.New("apigateway: oauth token not found")

// PersistedToken is the row shape stored in apigateway_oauth_tokens.
// Mirror of the MCP gateway's PersistedToken — kept separate so the
// two systems remain independent and changes to one don't ripple.
//
// RefreshExpiresAt is optional — only populated when the IdP
// returned refresh_expires_in (Keycloak does, others may not). Zero
// means the database column is NULL; callers must NOT interpret a
// zero value as "no expiry"; it means the IdP did not disclose one.
type PersistedToken struct {
	ConnectionName   string
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	Scope            string
	AuthenticatedBy  string
	AuthenticatedAt  time.Time
	UpdatedAt        time.Time
}

// TokenStore persists OAuth tokens for the authorization_code grant
// so a one-time browser-based authentication grants long-running
// background access. v1 stores a single shared identity per
// connection (matches the MCP gateway's design).
type TokenStore interface {
	Get(ctx context.Context, connection string) (*PersistedToken, error)
	Set(ctx context.Context, t PersistedToken) error
	Delete(ctx context.Context, connection string) error
}

// FieldEncryptor abstracts the platform's at-rest field encryption.
// Used here so this package doesn't import pkg/platform (which would
// create a cycle via the registry's factory wiring). The concrete
// implementation comes from pkg/platform.FieldEncryptor at startup.
type FieldEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// noopEncryptor is the fallback when ENCRYPTION_KEY is unset. The
// platform logs a startup warning when this code path is active so
// operators know refresh tokens are stored unencrypted (poor
// practice; only acceptable for dev environments).
type noopEncryptor struct{}

// Encrypt is the no-op pass-through used when at-rest encryption is disabled.
func (noopEncryptor) Encrypt(s string) (string, error) { return s, nil }

// Decrypt is the no-op pass-through used when at-rest encryption is disabled.
func (noopEncryptor) Decrypt(s string) (string, error) { return s, nil }

// PostgresTokenStore is a sql-backed TokenStore against the
// apigateway_oauth_tokens table (migration #38).
type PostgresTokenStore struct {
	db  *sql.DB
	enc FieldEncryptor
}

// NewPostgresTokenStore builds a TokenStore against the supplied
// database. enc may be nil; if so, refresh tokens are stored
// unencrypted (callers that want at-rest encryption should pass
// the platform's FieldEncryptor).
func NewPostgresTokenStore(db *sql.DB, enc FieldEncryptor) *PostgresTokenStore {
	if enc == nil {
		enc = noopEncryptor{}
	}
	return &PostgresTokenStore{db: db, enc: enc}
}

// Get returns the persisted token for connection or ErrTokenNotFound
// when no row matches. Refresh and access tokens are decrypted via
// the configured FieldEncryptor.
func (s *PostgresTokenStore) Get(ctx context.Context, connection string) (*PersistedToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT connection_name, access_token, refresh_token, expires_at,
		        refresh_expires_at, scope, authenticated_by, authenticated_at,
		        updated_at
		   FROM apigateway_oauth_tokens WHERE connection_name = $1`, connection)

	var (
		t                PersistedToken
		accessEnc        sql.NullString
		refreshEnc       sql.NullString
		expiresAt        sql.NullTime
		refreshExpiresAt sql.NullTime
		authenticatedAt  sql.NullTime
	)
	err := row.Scan(&t.ConnectionName, &accessEnc, &refreshEnc, &expiresAt,
		&refreshExpiresAt, &t.Scope, &t.AuthenticatedBy, &authenticatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("apigateway: tokenstore get: %w", err)
	}

	if accessEnc.Valid {
		dec, derr := s.enc.Decrypt(accessEnc.String)
		if derr != nil {
			return nil, fmt.Errorf("apigateway: decrypt access token: %w", derr)
		}
		t.AccessToken = dec
	}
	if refreshEnc.Valid {
		dec, derr := s.enc.Decrypt(refreshEnc.String)
		if derr != nil {
			return nil, fmt.Errorf("apigateway: decrypt refresh token: %w", derr)
		}
		t.RefreshToken = dec
	}
	if expiresAt.Valid {
		t.ExpiresAt = expiresAt.Time
	}
	if refreshExpiresAt.Valid {
		t.RefreshExpiresAt = refreshExpiresAt.Time
	}
	if authenticatedAt.Valid {
		t.AuthenticatedAt = authenticatedAt.Time
	}
	return &t, nil
}

// Set inserts or replaces the token row for a connection. Access
// and refresh tokens are encrypted via the configured FieldEncryptor
// before reaching the database.
func (s *PostgresTokenStore) Set(ctx context.Context, t PersistedToken) error {
	accessEnc, err := encryptOptional(s.enc, t.AccessToken)
	if err != nil {
		return fmt.Errorf("apigateway: encrypt access token: %w", err)
	}
	refreshEnc, err := encryptOptional(s.enc, t.RefreshToken)
	if err != nil {
		return fmt.Errorf("apigateway: encrypt refresh token: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO apigateway_oauth_tokens
		   (connection_name, access_token, refresh_token, expires_at,
		    refresh_expires_at, scope, authenticated_by, authenticated_at,
		    updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		 ON CONFLICT (connection_name) DO UPDATE
		   SET access_token       = EXCLUDED.access_token,
		       refresh_token      = EXCLUDED.refresh_token,
		       expires_at         = EXCLUDED.expires_at,
		       refresh_expires_at = EXCLUDED.refresh_expires_at,
		       scope              = EXCLUDED.scope,
		       authenticated_by   = EXCLUDED.authenticated_by,
		       authenticated_at   = EXCLUDED.authenticated_at,
		       updated_at         = NOW()`,
		t.ConnectionName, accessEnc, refreshEnc,
		nullableTime(t.ExpiresAt), nullableTime(t.RefreshExpiresAt),
		t.Scope, t.AuthenticatedBy, nullableTime(t.AuthenticatedAt))
	if err != nil {
		return fmt.Errorf("apigateway: tokenstore set: %w", err)
	}
	return nil
}

// Delete removes the token row, forcing the next call to surface a
// "needs reauth" signal. Used by the admin reauth path and by the
// Authenticator when the IdP rejects a refresh token (revoked or
// expired beyond refresh_expires_at).
func (s *PostgresTokenStore) Delete(ctx context.Context, connection string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM apigateway_oauth_tokens WHERE connection_name = $1`, connection)
	if err != nil {
		return fmt.Errorf("apigateway: tokenstore delete: %w", err)
	}
	return nil
}

// encryptOptional encrypts a non-empty plaintext or returns "" for
// empty input. NULL columns are represented as empty strings in the
// PersistedToken struct, so an empty access_token is stored as NULL
// (via nullableString) without ever invoking the encryptor.
func encryptOptional(enc FieldEncryptor, plaintext string) (sql.NullString, error) {
	if plaintext == "" {
		return sql.NullString{}, nil
	}
	enced, err := enc.Encrypt(plaintext)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("apigateway: encrypt field: %w", err)
	}
	return sql.NullString{String: enced, Valid: true}, nil
}

// nullableTime returns sql.NullTime{Valid: false} for the zero time
// so the database column is set to NULL rather than 0001-01-01.
func nullableTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// memoryTokenStore is the in-process implementation for tests and
// stateless single-replica deployments. Production multi-replica
// setups should use PostgresTokenStore so all replicas see the same
// refresh-token state.
type memoryTokenStore struct {
	tokens map[string]PersistedToken
}

// NewMemoryTokenStore returns an in-process TokenStore. Not safe
// across process restarts — refresh tokens are lost.
func NewMemoryTokenStore() TokenStore {
	return &memoryTokenStore{tokens: make(map[string]PersistedToken)}
}

// Get returns the cached token or ErrTokenNotFound.
func (s *memoryTokenStore) Get(_ context.Context, connection string) (*PersistedToken, error) {
	t, ok := s.tokens[connection]
	if !ok {
		return nil, ErrTokenNotFound
	}
	return &t, nil
}

// Set inserts or replaces the token row in memory.
func (s *memoryTokenStore) Set(_ context.Context, t PersistedToken) error {
	t.UpdatedAt = time.Now()
	s.tokens[t.ConnectionName] = t
	return nil
}

// Delete removes the cached token entry.
func (s *memoryTokenStore) Delete(_ context.Context, connection string) error {
	delete(s.tokens, connection)
	return nil
}
