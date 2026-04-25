package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reverseEncryptor is a deterministic stand-in for the platform's
// FieldEncryptor: it reverses input bytes so that tests can verify
// values were transformed on the way in and again on the way out.
type reverseEncryptor struct {
	encErr error
	decErr error
}

func (r reverseEncryptor) Encrypt(s string) (string, error) {
	if r.encErr != nil {
		return "", r.encErr
	}
	return reverse(s), nil
}

func (r reverseEncryptor) Decrypt(s string) (string, error) {
	if r.decErr != nil {
		return "", r.decErr
	}
	return reverse(s), nil
}

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func TestNoopEncryptor_PassesThrough(t *testing.T) {
	var n noopEncryptor
	enc, err := n.Encrypt("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", enc)
	dec, err := n.Decrypt("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", dec)
}

func TestNullTime_ZeroBecomesNull(t *testing.T) {
	got := nullTime(time.Time{})
	assert.False(t, got.Valid)
	now := time.Now().UTC()
	got = nullTime(now)
	assert.True(t, got.Valid)
	assert.Equal(t, now, got.Time)
}

func TestMemoryTokenStore_Delete(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, PersistedToken{ConnectionName: "c1", AccessToken: "a"}))

	got, err := store.Get(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, "a", got.AccessToken)

	require.NoError(t, store.Delete(ctx, "c1"))

	_, err = store.Get(ctx, "c1")
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestPostgresTokenStore_NewWithNilEncryptor_UsesNoop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s := NewPostgresTokenStore(db, nil)
	assert.NotNil(t, s.enc)
	_, ok := s.enc.(noopEncryptor)
	assert.True(t, ok, "nil encryptor should default to noopEncryptor")
}

func TestPostgresTokenStore_Get_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expiresAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	authedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{
		"connection_name", "access_token", "refresh_token", "expires_at",
		"scope", "authenticated_by", "authenticated_at", "updated_at",
	}).AddRow("vendor", reverse("acc"), reverse("ref"), expiresAt,
		"api", "alice@example.com", authedAt, updatedAt)

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
	assert.Contains(t, err.Error(), "scan oauth token")
}

func TestPostgresTokenStore_Get_DecryptError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	rows := sqlmock.NewRows([]string{
		"connection_name", "access_token", "refresh_token", "expires_at",
		"scope", "authenticated_by", "authenticated_at", "updated_at",
	}).AddRow("vendor", "ciphertext", nil, nil, "", "", nil, time.Now())

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("vendor").
		WillReturnRows(rows)

	store := NewPostgresTokenStore(db, reverseEncryptor{decErr: errors.New("bad key")})
	_, err = store.Get(context.Background(), "vendor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt access_token")
}

func TestPostgresTokenStore_Get_DecryptRefreshError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Refresh token decrypt fails: access succeeds (no error), refresh
	// fails. Use noop for access by leaving access encrypted empty? Use
	// a custom encryptor that errors only on the second call.
	rows := sqlmock.NewRows([]string{
		"connection_name", "access_token", "refresh_token", "expires_at",
		"scope", "authenticated_by", "authenticated_at", "updated_at",
	}).AddRow("vendor", nil, "ref-cipher", nil, "", "", nil, time.Now())

	mock.ExpectQuery("SELECT connection_name").
		WithArgs("vendor").
		WillReturnRows(rows)

	store := NewPostgresTokenStore(db, reverseEncryptor{decErr: errors.New("bad refresh key")})
	_, err = store.Get(context.Background(), "vendor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt refresh_token")
}

func TestPostgresTokenStore_Set_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO gateway_oauth_tokens").
		WithArgs(
			"vendor",
			reverse("access-x"),
			reverse("refresh-x"),
			sqlmock.AnyArg(),
			"api",
			"alice@example.com",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
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
	assert.Contains(t, err.Error(), "encrypt access_token")
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
	assert.Contains(t, err.Error(), "encrypt refresh_token")
}

func TestPostgresTokenStore_Set_ExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("INSERT INTO gateway_oauth_tokens").
		WillReturnError(errors.New("db down"))

	store := NewPostgresTokenStore(db, nil)
	err = store.Set(context.Background(), PersistedToken{ConnectionName: "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upsert oauth token")
}

func TestPostgresTokenStore_Delete_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("DELETE FROM gateway_oauth_tokens").
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

	mock.ExpectExec("DELETE FROM gateway_oauth_tokens").
		WithArgs("vendor").
		WillReturnError(errors.New("nope"))

	store := NewPostgresTokenStore(db, nil)
	err = store.Delete(context.Background(), "vendor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete oauth token")
}

// flakyEncryptor lets a test inject per-call behavior.
type flakyEncryptor struct {
	encrypt func(string) (string, error)
}

func (f *flakyEncryptor) Encrypt(s string) (string, error) { return f.encrypt(s) }
func (*flakyEncryptor) Decrypt(s string) (string, error)   { return s, nil }

// Compile-time assertion: flakyEncryptor satisfies TokenEncryptor.
var _ TokenEncryptor = (*flakyEncryptor)(nil)

// Compile-time assertion to silence unused-fmt import if no string formatting
// is needed in tests. (fmt is used via require.NoError messages internally.)
var _ = fmt.Sprintf
