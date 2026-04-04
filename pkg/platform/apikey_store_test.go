package platform

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	apikeyFmtUnmetExpect = "unmet expectations: %v"
)

var apikeyColumns = []string{
	"name", "key_hash", "email", "description", "roles", "expires_at", "created_by", "created_at",
}

func newTestAPIKeyStore(t *testing.T) (*PostgresAPIKeyStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("creating sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresAPIKeyStore(db), mock
}

func TestNewPostgresAPIKeyStore(t *testing.T) {
	store := NewPostgresAPIKeyStore(nil)
	require.NotNil(t, store)
	assert.Nil(t, store.db)
}

func TestPostgresAPIKeyStoreList(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	rows := sqlmock.NewRows(apikeyColumns).
		AddRow("admin-key", "$2a$10$hash1", "admin@example.com", "Admin key",
			[]byte(`["admin"]`), exp, "creator@example.com", now).
		AddRow("readonly-key", "$2a$10$hash2", "readonly@example.com", "Read-only key",
			[]byte(`["viewer"]`), nil, "creator@example.com", now)

	mock.ExpectQuery("SELECT name, key_hash, email, description, roles, expires_at, created_by, created_at").
		WillReturnRows(rows)

	defs, err := store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 2)

	assert.Equal(t, "admin-key", defs[0].Name)
	assert.Equal(t, "$2a$10$hash1", defs[0].KeyHash)
	assert.Equal(t, "admin@example.com", defs[0].Email)
	assert.Equal(t, "Admin key", defs[0].Description)
	assert.Equal(t, []string{"admin"}, defs[0].Roles)
	require.NotNil(t, defs[0].ExpiresAt)
	assert.Equal(t, "creator@example.com", defs[0].CreatedBy)
	assert.Equal(t, now, defs[0].CreatedAt)

	assert.Equal(t, "readonly-key", defs[1].Name)
	assert.Equal(t, "$2a$10$hash2", defs[1].KeyHash)
	assert.Equal(t, []string{"viewer"}, defs[1].Roles)
	assert.Nil(t, defs[1].ExpiresAt)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreList_QueryError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectQuery("SELECT name, key_hash, email, description, roles, expires_at, created_by, created_at").
		WillReturnError(errors.New("db error"))

	defs, err := store.List(context.Background())
	require.Error(t, err)
	assert.Nil(t, defs)
	assert.Contains(t, err.Error(), "listing api keys")
	assert.Contains(t, err.Error(), "db error")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreList_ScanError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	rows := sqlmock.NewRows(apikeyColumns).
		AddRow("bad", "hash", "email", "desc", "not-json", nil, "admin", time.Now())

	mock.ExpectQuery("SELECT .+ FROM api_keys").WillReturnRows(rows)

	_, err := store.List(context.Background())
	assert.Error(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreSet(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	def := APIKeyDefinition{
		Name:        "test-key",
		KeyHash:     "$2a$10$somehash",
		Email:       "test@example.com",
		Description: "Test key",
		Roles:       []string{"admin", "viewer"},
		ExpiresAt:   &exp,
		CreatedBy:   "admin@example.com",
		CreatedAt:   now,
	}

	mock.ExpectExec("INSERT INTO api_keys").
		WithArgs(
			def.Name, def.KeyHash, def.Email, def.Description,
			sqlmock.AnyArg(), // roles JSON
			def.ExpiresAt, def.CreatedBy,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Set(context.Background(), def)
	require.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreSet_ExecError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("INSERT INTO api_keys").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("exec error"))

	err := store.Set(context.Background(), APIKeyDefinition{Name: "test"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upserting api key")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreDelete(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("DELETE FROM api_keys WHERE name").
		WithArgs("test-key").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Delete(context.Background(), "test-key")
	require.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreDelete_NotFound(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("DELETE FROM api_keys WHERE name").
		WithArgs("nonexistent").
		WillReturnResult(driver.RowsAffected(0))

	err := store.Delete(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrAPIKeyNotFound)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreDelete_ExecError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("DELETE FROM api_keys WHERE name").
		WithArgs("test-key").
		WillReturnError(errors.New("exec error"))

	err := store.Delete(context.Background(), "test-key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting api key")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestNoopAPIKeyStore(t *testing.T) {
	store := &NoopAPIKeyStore{}
	ctx := context.Background()

	t.Run("List returns nil nil", func(t *testing.T) {
		defs, err := store.List(ctx)
		assert.NoError(t, err)
		assert.Nil(t, defs)
	})

	t.Run("Set returns nil", func(t *testing.T) {
		err := store.Set(ctx, APIKeyDefinition{Name: "test"})
		assert.NoError(t, err)
	})

	t.Run("Delete returns ErrAPIKeyNotFound", func(t *testing.T) {
		err := store.Delete(ctx, "anything")
		assert.ErrorIs(t, err, ErrAPIKeyNotFound)
	})
}
