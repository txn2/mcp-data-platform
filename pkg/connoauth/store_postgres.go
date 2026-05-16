package connoauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
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

// List returns metadata for every row in connection_oauth_tokens.
// Access/refresh tokens are NOT returned — the refresher uses this
// to find rows that need refresh; the per-row Get loads the secret
// material when it actually refreshes. Avoiding the decrypt path on
// the listing query keeps the refresher cheap (no per-row encryption
// round-trip just to enumerate).
func (s *PostgresStore) List(ctx context.Context) ([]PersistedToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT connection_kind, connection_name,
		       expires_at, refresh_expires_at, scope,
		       authenticated_by, authenticated_at, updated_at,
		       (refresh_token IS NOT NULL) AS has_refresh
		  FROM connection_oauth_tokens`)
	if err != nil {
		return nil, fmt.Errorf("connoauth: list rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]PersistedToken, 0)
	for rows.Next() {
		var (
			t          PersistedToken
			expAt      sql.NullTime
			refExpAt   sql.NullTime
			authedAt   sql.NullTime
			hasRefresh bool
		)
		if err := rows.Scan(&t.Key.Kind, &t.Key.Name,
			&expAt, &refExpAt, &t.Scope,
			&t.AuthenticatedBy, &authedAt, &t.UpdatedAt,
			&hasRefresh); err != nil {
			return nil, fmt.Errorf("connoauth: list scan: %w", err)
		}
		applyNullTimes(&t, expAt, refExpAt, authedAt)
		// Mark RefreshToken non-empty when the row has one so the
		// refresher's skip-no-refresh check works without a second
		// query per row. The actual value is loaded by the refresh
		// path's Get call, not from this listing. Sentinel string
		// is shared with MemoryStore.List so both backends present
		// the same surface.
		if hasRefresh {
			t.RefreshToken = refreshTokenSentinel
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("connoauth: list iterate: %w", err)
	}
	return out, nil
}

// Lock acquires a Postgres session-scoped advisory lock for key.
// Held across processes: two replicas calling Lock for the same key
// serialize at the database level. The lock is auto-released if the
// holding session disconnects, so a crashed holder cannot deadlock
// the row. The returned release function MUST be deferred; it sends
// an explicit pg_advisory_unlock and returns the dedicated
// connection to the pool. It is idempotent and safe to call after
// the request context has been canceled (the unlock uses a
// background context so cancellation does not strand the lock).
//
// Lock ID derivation: a 64-bit FNV-1a hash of "connoauth:" + kind +
// "/" + name. The "connoauth:" namespace prefix prevents collision
// with any other code that might also use pg_advisory_lock in the
// same database. Hash collisions across distinct (kind, name) pairs
// are mathematically possible but vanishingly rare for any realistic
// connection count; a collision would only cause unnecessary
// serialization between two unrelated connections, never incorrect
// behavior.
func (s *PostgresStore) Lock(ctx context.Context, key Key) (func(), error) {
	if !key.IsValid() {
		return nil, errInvalidKey
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("connoauth: acquire conn for advisory lock: %w", err)
	}
	lockID := advisoryLockID(key)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("connoauth: pg_advisory_lock(%s/%s): %w", key.Kind, key.Name, err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			// Background context: unlock must run even if the request
			// context was canceled. pg_advisory_unlock against a
			// session-scoped lock is local and fast.
			_, _ = conn.ExecContext(context.Background(),
				"SELECT pg_advisory_unlock($1)", lockID)
			_ = conn.Close()
		})
	}
	return release, nil
}

// advisoryLockID computes the 64-bit pg_advisory_lock key for a
// connection. Pure function so the lock IDs are deterministic across
// process restarts; a token row's lock identity does not change as
// long as its (kind, name) does not change.
func advisoryLockID(key Key) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("connoauth:" + key.Kind + "/" + key.Name))
	// Convert uint64 to int64 via two's complement. pg_advisory_lock
	// accepts any int64 value; we don't care about the sign.
	return int64(h.Sum64()) // #nosec G115 -- intentional bit reinterpretation: pg_advisory_lock accepts any int64; the negative/positive distinction is meaningless for a hash-derived lock identifier.
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
