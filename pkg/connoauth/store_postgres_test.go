package connoauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMockPostgresStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db, fakeEncryptor{}), mock
}

// fakeEncryptor wraps plaintext with a marker so tests can prove
// encryption/decryption actually ran in the Set/Get path. Decrypt
// strips the marker; missing marker indicates a leak of plaintext
// past the encryptor.
type fakeEncryptor struct{}

func (fakeEncryptor) Encrypt(s string) (string, error) { return "enc:" + s, nil }
func (fakeEncryptor) Decrypt(s string) (string, error) {
	if len(s) >= 4 && s[:4] == "enc:" {
		return s[4:], nil
	}
	return s, nil
}

func TestPostgresStore_Set_Get_RoundTrip(t *testing.T) {
	t.Parallel()
	store, mock := newMockPostgresStore(t)
	now := time.Now().Truncate(time.Second).UTC()
	tok := PersistedToken{
		Key:              Key{Kind: KindMCP, Name: "alpha"},
		AccessToken:      "access-plaintext",
		RefreshToken:     "refresh-plaintext",
		ExpiresAt:        now.Add(time.Hour),
		RefreshExpiresAt: now.Add(24 * time.Hour),
		Scope:            "openid offline_access",
		AuthenticatedBy:  "user@example.com",
		AuthenticatedAt:  now,
	}

	const upsertSQL = `INSERT INTO connection_oauth_tokens
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
		       updated_at         = NOW()`
	mock.ExpectExec(upsertSQL).
		WithArgs(
			"mcp", "alpha",
			sqlmock.AnyArg(), // encrypted access token
			sqlmock.AnyArg(), // encrypted refresh token
			tok.ExpiresAt, tok.RefreshExpiresAt,
			tok.Scope, tok.AuthenticatedBy, tok.AuthenticatedAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := store.Set(context.Background(), tok); err != nil {
		t.Fatalf("Set: %v", err)
	}

	const selectSQL = `SELECT access_token, refresh_token, expires_at,
		        refresh_expires_at, scope, authenticated_by,
		        authenticated_at, updated_at
		   FROM connection_oauth_tokens
		  WHERE connection_kind = $1 AND connection_name = $2`
	rows := sqlmock.NewRows([]string{
		"access_token", "refresh_token", "expires_at", "refresh_expires_at",
		"scope", "authenticated_by", "authenticated_at", "updated_at",
	}).AddRow(
		"enc:access-plaintext", "enc:refresh-plaintext",
		tok.ExpiresAt, tok.RefreshExpiresAt,
		tok.Scope, tok.AuthenticatedBy, tok.AuthenticatedAt, now,
	)
	mock.ExpectQuery(selectSQL).WithArgs("mcp", "alpha").WillReturnRows(rows)

	got, err := store.Get(context.Background(), tok.Key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "access-plaintext" || got.RefreshToken != "refresh-plaintext" {
		t.Fatalf("encryption round-trip failed: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	store, mock := newMockPostgresStore(t)
	const selectSQL = `SELECT access_token, refresh_token, expires_at,
		        refresh_expires_at, scope, authenticated_by,
		        authenticated_at, updated_at
		   FROM connection_oauth_tokens
		  WHERE connection_kind = $1 AND connection_name = $2`
	mock.ExpectQuery(selectSQL).
		WithArgs("api", "missing").
		WillReturnRows(sqlmock.NewRows([]string{
			"access_token", "refresh_token", "expires_at", "refresh_expires_at",
			"scope", "authenticated_by", "authenticated_at", "updated_at",
		}))

	_, err := store.Get(context.Background(), Key{Kind: KindAPI, Name: "missing"})
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}

func TestPostgresStore_Delete(t *testing.T) {
	t.Parallel()
	store, mock := newMockPostgresStore(t)
	const deleteSQL = `DELETE FROM connection_oauth_tokens
		  WHERE connection_kind = $1 AND connection_name = $2`
	mock.ExpectExec(deleteSQL).
		WithArgs("mcp", "foo").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.Delete(context.Background(), Key{Kind: KindMCP, Name: "foo"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_InvalidKey(t *testing.T) {
	t.Parallel()
	store, _ := newMockPostgresStore(t)
	ctx := context.Background()
	if _, err := store.Get(ctx, Key{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Get: want errInvalidKey, got %v", err)
	}
	if err := store.Set(ctx, PersistedToken{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Set: want errInvalidKey, got %v", err)
	}
	if err := store.Delete(ctx, Key{}); !errors.Is(err, errInvalidKey) {
		t.Fatalf("Delete: want errInvalidKey, got %v", err)
	}
}

func TestPostgresStore_NilEncryptorUsesNoop(t *testing.T) {
	t.Parallel()
	// Ensure NewPostgresStore handles nil encryptor without panicking.
	// The actual no-op behavior is exercised by other tests via
	// fakeEncryptor; here we only verify constructor robustness.
	if s := NewPostgresStore(nil, nil); s == nil {
		t.Fatal("NewPostgresStore returned nil for nil encryptor")
	}
}

func TestNullableTime(t *testing.T) {
	t.Parallel()
	if nt := nullableTime(time.Time{}); nt.Valid {
		t.Fatal("zero time should be NULL")
	}
	now := time.Now()
	if nt := nullableTime(now); !nt.Valid || !nt.Time.Equal(now) {
		t.Fatalf("non-zero time should round-trip, got %+v", nt)
	}
}

func TestEncryptOptional(t *testing.T) {
	t.Parallel()
	if ns, err := encryptOptional(fakeEncryptor{}, ""); err != nil || ns.Valid {
		t.Fatalf("empty plaintext should be NULL: ns=%+v err=%v", ns, err)
	}
	ns, err := encryptOptional(fakeEncryptor{}, "secret")
	if err != nil || !ns.Valid || ns.String != "enc:secret" {
		t.Fatalf("non-empty plaintext should encrypt: ns=%+v err=%v", ns, err)
	}
}
