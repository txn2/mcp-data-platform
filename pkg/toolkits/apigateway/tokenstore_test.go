package apigateway

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryTokenStore_CRUD(t *testing.T) {
	s := NewMemoryTokenStore()
	ctx := context.Background()

	// Get on missing connection returns ErrTokenNotFound.
	if _, err := s.Get(ctx, "ghost"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("Get(missing) = %v; want ErrTokenNotFound", err)
	}

	// Set + Get round-trip.
	now := time.Now()
	tok := PersistedToken{
		ConnectionName:  "c1",
		AccessToken:     "access-abc",
		RefreshToken:    "refresh-xyz",
		ExpiresAt:       now.Add(time.Hour),
		Scope:           "read:users",
		AuthenticatedBy: "admin@example.com",
		AuthenticatedAt: now,
	}
	if err := s.Set(ctx, tok); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "access-abc" || got.RefreshToken != "refresh-xyz" {
		t.Errorf("round-trip lost tokens: %+v", got)
	}
	if got.Scope != "read:users" || got.AuthenticatedBy != "admin@example.com" {
		t.Errorf("round-trip lost metadata: %+v", got)
	}

	// Delete + verify absent.
	if err := s.Delete(ctx, "c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "c1"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("after Delete: Get = %v; want ErrTokenNotFound", err)
	}
}

func TestEncryptOptional_EmptyPlaintextDoesNotCallEncryptor(t *testing.T) {
	called := false
	enc := stubEncryptor{
		encrypt: func(s string) (string, error) {
			called = true
			return "ENC:" + s, nil
		},
	}
	got, err := encryptOptional(enc, "")
	if err != nil {
		t.Fatalf("encryptOptional(\"\"): %v", err)
	}
	if got.Valid {
		t.Errorf("empty plaintext should produce NULL (Valid=false), got %+v", got)
	}
	if called {
		t.Error("encryptor invoked for empty plaintext")
	}
}

func TestEncryptOptional_RoundTrip(t *testing.T) {
	enc := stubEncryptor{
		encrypt: func(s string) (string, error) { return "ENC:" + s, nil },
	}
	got, err := encryptOptional(enc, "secret")
	if err != nil {
		t.Fatalf("encryptOptional: %v", err)
	}
	if !got.Valid || got.String != "ENC:secret" {
		t.Errorf("unexpected encrypted value: %+v", got)
	}
}

func TestNullableTime_ZeroProducesNull(t *testing.T) {
	got := nullableTime(time.Time{})
	if got.Valid {
		t.Error("zero time should produce NULL (Valid=false)")
	}
	now := time.Now()
	got = nullableTime(now)
	if !got.Valid || !got.Time.Equal(now) {
		t.Errorf("non-zero time lost: %+v", got)
	}
}

// stubEncryptor lets tests inject a recordable / failable
// encryptor without standing up the full platform AES wrapper.
type stubEncryptor struct {
	encrypt func(string) (string, error)
	decrypt func(string) (string, error)
}

func (s stubEncryptor) Encrypt(p string) (string, error) {
	if s.encrypt == nil {
		return p, nil
	}
	return s.encrypt(p)
}

func (s stubEncryptor) Decrypt(c string) (string, error) {
	if s.decrypt == nil {
		return c, nil
	}
	return s.decrypt(c)
}

func TestNoopEncryptor_PassesThrough(t *testing.T) {
	e := noopEncryptor{}
	if got, _ := e.Encrypt("plaintext"); got != "plaintext" {
		t.Errorf("noop Encrypt mutated value: %q", got)
	}
	if got, _ := e.Decrypt("ciphertext"); got != "ciphertext" {
		t.Errorf("noop Decrypt mutated value: %q", got)
	}
}

// reverseEncryptor is a deterministic stand-in for the platform's
// FieldEncryptor: it reverses input bytes so tests can verify that
// values were transformed on the way in and again on the way out.
// (Mirror of pkg/toolkits/gateway's helper of the same name — kept
// local rather than shared so the apigateway package has no
// cross-package test imports.)
type reverseEncryptor struct {
	encErr error
	decErr error
}

func (r reverseEncryptor) Encrypt(s string) (string, error) {
	if r.encErr != nil {
		return "", r.encErr
	}
	return reverseString(s), nil
}

func (r reverseEncryptor) Decrypt(s string) (string, error) {
	if r.decErr != nil {
		return "", r.decErr
	}
	return reverseString(s), nil
}

func reverseString(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func TestPostgresTokenStore_NewWithNilEncryptor_UsesNoop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s := NewPostgresTokenStore(db, nil)
	if _, ok := s.enc.(noopEncryptor); !ok {
		t.Errorf("nil encryptor should default to noopEncryptor; got %T", s.enc)
	}
}

func TestPostgresTokenStore_Get_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expiresAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	refreshExpiresAt := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	authedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{
		"connection_name", "access_token", "refresh_token", "expires_at",
		"refresh_expires_at", "scope", "authenticated_by", "authenticated_at",
		"updated_at",
	}).AddRow("vendor", reverseString("acc"), reverseString("ref"), expiresAt,
		refreshExpiresAt, "api", "alice@example.com", authedAt, updatedAt)

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("vendor").
		WillReturnRows(rows)

	store := NewPostgresTokenStore(db, reverseEncryptor{})
	got, err := store.Get(context.Background(), "vendor")
	require.NoError(t, err)
	assert.Equal(t, "vendor", got.ConnectionName)
	assert.Equal(t, "acc", got.AccessToken)
	assert.Equal(t, "ref", got.RefreshToken)
	assert.Equal(t, "api", got.Scope)
	assert.Equal(t, "alice@example.com", got.AuthenticatedBy)
	assert.Equal(t, expiresAt, got.ExpiresAt)
	assert.Equal(t, refreshExpiresAt, got.RefreshExpiresAt)
	assert.Equal(t, authedAt, got.AuthenticatedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresTokenStore_Get_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	store := NewPostgresTokenStore(db, nil)
	_, err = store.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestPostgresTokenStore_Get_ScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("vendor").
		WillReturnError(errors.New("connection refused"))

	store := NewPostgresTokenStore(db, nil)
	_, err = store.Get(context.Background(), "vendor")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTokenNotFound)
}

func TestPostgresTokenStore_Get_DecryptAccessError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{
		"connection_name", "access_token", "refresh_token", "expires_at",
		"refresh_expires_at", "scope", "authenticated_by", "authenticated_at",
		"updated_at",
	}).AddRow("vendor", "ciphertext", nil, nil, nil, "", "", nil, time.Now())

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("vendor").
		WillReturnRows(rows)

	store := NewPostgresTokenStore(db, reverseEncryptor{decErr: errors.New("bad key")})
	_, err = store.Get(context.Background(), "vendor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt access token")
}

func TestPostgresTokenStore_Get_DecryptRefreshError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{
		"connection_name", "access_token", "refresh_token", "expires_at",
		"refresh_expires_at", "scope", "authenticated_by", "authenticated_at",
		"updated_at",
	}).AddRow("vendor", nil, "ref-cipher", nil, nil, "", "", nil, time.Now())

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("vendor").
		WillReturnRows(rows)

	store := NewPostgresTokenStore(db, reverseEncryptor{decErr: errors.New("bad refresh key")})
	_, err = store.Get(context.Background(), "vendor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt refresh token")
}

func TestPostgresTokenStore_Set_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO apigateway_oauth_tokens").
		WithArgs(
			"vendor",
			sqlmock.AnyArg(), // access_token (encrypted)
			sqlmock.AnyArg(), // refresh_token (encrypted)
			sqlmock.AnyArg(), // expires_at
			nil,              // refresh_expires_at: zero → NULL
			"api",
			"alice@example.com",
			sqlmock.AnyArg(), // authenticated_at
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	store := NewPostgresTokenStore(db, reverseEncryptor{})
	err = store.Set(context.Background(), PersistedToken{
		ConnectionName:  "vendor",
		AccessToken:     "access-x",
		RefreshToken:    "refresh-x",
		ExpiresAt:       time.Now().Add(time.Hour),
		Scope:           "api",
		AuthenticatedBy: "alice@example.com",
		AuthenticatedAt: time.Now(),
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresTokenStore_Set_AccessEncryptError(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	store := NewPostgresTokenStore(db, reverseEncryptor{encErr: errors.New("crypto fail")})
	err = store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "x",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt access token")
}

func TestPostgresTokenStore_Set_RefreshEncryptError(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	calls := 0
	store := NewPostgresTokenStore(db, &flakyEncryptor{
		encrypt: func(s string) (string, error) {
			calls++
			if calls == 2 {
				return "", errors.New("boom")
			}
			return s, nil
		},
	})
	err = store.Set(context.Background(), PersistedToken{
		ConnectionName: "vendor",
		AccessToken:    "a",
		RefreshToken:   "r",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt refresh token")
}

func TestPostgresTokenStore_Set_ExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO apigateway_oauth_tokens").
		WillReturnError(errors.New("db down"))

	store := NewPostgresTokenStore(db, nil)
	err = store.Set(context.Background(), PersistedToken{ConnectionName: "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tokenstore set")
}

func TestPostgresTokenStore_Delete_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("DELETE FROM apigateway_oauth_tokens").
		WithArgs("vendor").
		WillReturnResult(sqlmock.NewResult(0, 1))

	store := NewPostgresTokenStore(db, nil)
	require.NoError(t, store.Delete(context.Background(), "vendor"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresTokenStore_Delete_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("DELETE FROM apigateway_oauth_tokens").
		WithArgs("vendor").
		WillReturnError(errors.New("nope"))

	store := NewPostgresTokenStore(db, nil)
	err = store.Delete(context.Background(), "vendor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tokenstore delete")
}

// flakyEncryptor lets a test inject per-call behavior. Used to drive
// "first encrypt succeeds, second fails" — the only path that
// exercises the refresh_token-specific error wrapper.
type flakyEncryptor struct {
	encrypt func(string) (string, error)
}

func (f *flakyEncryptor) Encrypt(s string) (string, error) { return f.encrypt(s) }
func (*flakyEncryptor) Decrypt(s string) (string, error)   { return s, nil }

// Compile-time assertion: flakyEncryptor satisfies FieldEncryptor.
var _ FieldEncryptor = (*flakyEncryptor)(nil)
