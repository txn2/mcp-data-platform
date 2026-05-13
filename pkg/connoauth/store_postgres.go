package connoauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PostgresStore is the SQL-backed Store against connection_oauth_tokens
// (migration 000039). Replaces the two per-kind stores from earlier
// (gateway_oauth_tokens + apigateway_oauth_tokens) — both old kinds now
// live in this single table keyed by (connection_kind, connection_name).
type PostgresStore struct {
	db  *sql.DB
	enc FieldEncryptor
}

// NewPostgresStore wires a Store to the supplied database. Pass enc=nil
// to disable at-rest encryption (refresh tokens stored unencrypted —
// dev-only). The platform's startup code passes its FieldEncryptor when
// ENCRYPTION_KEY is set and logs a warning when it isn't.
func NewPostgresStore(db *sql.DB, enc FieldEncryptor) *PostgresStore {
	if enc == nil {
		enc = noopEncryptor{}
	}
	return &PostgresStore{db: db, enc: enc}
}

// Get reads the row for key, decrypting access/refresh tokens via the
// configured FieldEncryptor.
func (s *PostgresStore) Get(ctx context.Context, key Key) (*PersistedToken, error) {
	if !key.IsValid() {
		return nil, errInvalidKey
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT access_token, refresh_token, expires_at,
		        refresh_expires_at, scope, authenticated_by,
		        authenticated_at, updated_at
		   FROM connection_oauth_tokens
		  WHERE connection_kind = $1 AND connection_name = $2`,
		key.Kind, key.Name)
	return s.scanTokenRow(row, key)
}

// scanTokenRow extracts the per-row values, decrypts the encrypted
// columns, and assembles a PersistedToken. Extracted from Get so the
// caller's cyclomatic complexity stays under the project's ceiling
// and the decrypt + null-time assembly logic can be unit-tested in
// isolation.
func (s *PostgresStore) scanTokenRow(row *sql.Row, key Key) (*PersistedToken, error) {
	var (
		t                PersistedToken
		accessEnc        sql.NullString
		refreshEnc       sql.NullString
		expiresAt        sql.NullTime
		refreshExpiresAt sql.NullTime
		authedAt         sql.NullTime
	)
	t.Key = key
	err := row.Scan(&accessEnc, &refreshEnc, &expiresAt,
		&refreshExpiresAt, &t.Scope, &t.AuthenticatedBy,
		&authedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("connoauth: scan token row: %w", err)
	}
	if err := s.decryptInto(&t, key, accessEnc, refreshEnc); err != nil {
		return nil, err
	}
	applyNullTimes(&t, expiresAt, refreshExpiresAt, authedAt)
	return &t, nil
}

// decryptInto decrypts the access and refresh tokens (if present) and
// writes them into t. Returns an error wrapping the column name and
// key so a misconfigured FieldEncryptor (or a corrupted row) is
// reported with enough context to find the bad row.
func (s *PostgresStore) decryptInto(t *PersistedToken, key Key, accessEnc, refreshEnc sql.NullString) error {
	if accessEnc.Valid {
		dec, derr := s.enc.Decrypt(accessEnc.String)
		if derr != nil {
			return fmt.Errorf("connoauth: decrypt access_token for %s/%s: %w", key.Kind, key.Name, derr)
		}
		t.AccessToken = dec
	}
	if refreshEnc.Valid {
		dec, derr := s.enc.Decrypt(refreshEnc.String)
		if derr != nil {
			return fmt.Errorf("connoauth: decrypt refresh_token for %s/%s: %w", key.Kind, key.Name, derr)
		}
		t.RefreshToken = dec
	}
	return nil
}

// applyNullTimes copies non-NULL sql.NullTime values into t. Zero
// values stay zero — callers must not interpret zero as a real time.
func applyNullTimes(t *PersistedToken, expiresAt, refreshExpiresAt, authedAt sql.NullTime) {
	if expiresAt.Valid {
		t.ExpiresAt = expiresAt.Time
	}
	if refreshExpiresAt.Valid {
		t.RefreshExpiresAt = refreshExpiresAt.Time
	}
	if authedAt.Valid {
		t.AuthenticatedAt = authedAt.Time
	}
}

// Set upserts the row for t.Key, encrypting access and refresh tokens
// before they reach the database. Empty token strings are stored as
// SQL NULL via encryptOptional rather than encrypted empty-strings —
// this matches the prior per-kind stores so the migration backfill
// preserves shape.
func (s *PostgresStore) Set(ctx context.Context, t PersistedToken) error {
	if !t.Key.IsValid() {
		return errInvalidKey
	}
	accessEnc, err := encryptOptional(s.enc, t.AccessToken)
	if err != nil {
		return fmt.Errorf("connoauth: encrypt access_token: %w", err)
	}
	refreshEnc, err := encryptOptional(s.enc, t.RefreshToken)
	if err != nil {
		return fmt.Errorf("connoauth: encrypt refresh_token: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO connection_oauth_tokens
		   (connection_kind, connection_name, access_token, refresh_token,
		    expires_at, refresh_expires_at, scope, authenticated_by,
		    authenticated_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		 ON CONFLICT (connection_kind, connection_name) DO UPDATE
		   SET access_token       = EXCLUDED.access_token,
		       refresh_token      = EXCLUDED.refresh_token,
		       expires_at         = EXCLUDED.expires_at,
		       refresh_expires_at = EXCLUDED.refresh_expires_at,
		       scope              = EXCLUDED.scope,
		       authenticated_by   = EXCLUDED.authenticated_by,
		       authenticated_at   = EXCLUDED.authenticated_at,
		       updated_at         = NOW()`,
		t.Key.Kind, t.Key.Name, accessEnc, refreshEnc,
		nullableTime(t.ExpiresAt), nullableTime(t.RefreshExpiresAt),
		t.Scope, t.AuthenticatedBy, nullableTime(t.AuthenticatedAt))
	if err != nil {
		return fmt.Errorf("connoauth: upsert token row: %w", err)
	}
	return nil
}

// Delete removes the row for key. Idempotent — missing rows do not
// produce an error. Used by Source on revoked-refresh cleanup and by
// the admin reacquire path.
func (s *PostgresStore) Delete(ctx context.Context, key Key) error {
	if !key.IsValid() {
		return errInvalidKey
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM connection_oauth_tokens
		  WHERE connection_kind = $1 AND connection_name = $2`,
		key.Kind, key.Name)
	if err != nil {
		return fmt.Errorf("connoauth: delete token row: %w", err)
	}
	return nil
}

// encryptOptional encrypts a non-empty plaintext or returns a NULL
// sql.NullString for the empty input. Empty access/refresh tokens
// reach NULL columns rather than encrypted empty strings — matches
// the prior store shape so migration 000039's backfill preserves
// NULL semantics.
func encryptOptional(enc FieldEncryptor, plaintext string) (sql.NullString, error) {
	if plaintext == "" {
		return sql.NullString{}, nil
	}
	enced, err := enc.Encrypt(plaintext)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("connoauth: encrypt field: %w", err)
	}
	return sql.NullString{String: enced, Valid: true}, nil
}

// nullableTime returns sql.NullTime{Valid: false} for the zero time
// so the column is stored as NULL rather than 0001-01-01.
func nullableTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
