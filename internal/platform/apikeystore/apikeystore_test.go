package apikeystore

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
	"user_email", "attributes",
}

func newTestAPIKeyStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("creating sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgres(db), mock
}

func TestNewPostgresAPIKeyStore(t *testing.T) {
	store := NewPostgres(nil)
	require.NotNil(t, store)
	assert.Nil(t, store.db)
}

func TestPostgresAPIKeyStoreList(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	rows := sqlmock.NewRows(apikeyColumns).
		AddRow("admin-key", "$2a$10$hash1", "admin@example.com", "Admin key",
			[]byte(`["admin"]`), exp, "creator@example.com", now, "", []byte(`{"tenant":"acme"}`)).
		AddRow("readonly-key", "$2a$10$hash2", "readonly@example.com", "Read-only key",
			[]byte(`["viewer"]`), nil, "creator@example.com", now, "", []byte(`{}`))

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
	assert.Equal(t, map[string]string{"tenant": "acme"}, defs[0].Attributes)

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
		AddRow("bad", "hash", "email", "desc", "not-json", nil, "admin", time.Now(), "", []byte(`{}`))

	mock.ExpectQuery("SELECT .+ FROM api_keys").WillReturnRows(rows)

	_, err := store.List(context.Background())
	assert.Error(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreCreate(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	now := time.Now()
	exp := now.Add(24 * time.Hour)

	def := Definition{
		Name:        "test-key",
		KeyHash:     "$2a$10$somehash",
		Email:       "test@example.com",
		Description: "Test key",
		Roles:       []string{"admin", "viewer"},
		ExpiresAt:   &exp,
		CreatedBy:   "admin@example.com",
		CreatedAt:   now,
	}

	// The statement must not replace a stored key: DO NOTHING, never DO UPDATE.
	mock.ExpectExec(`INSERT INTO api_keys .* ON CONFLICT \(name\) DO NOTHING`).
		WithArgs(
			def.Name, def.KeyHash, def.Email, def.Description,
			[]byte(`["admin","viewer"]`),
			def.ExpiresAt, def.CreatedBy, def.UserEmail, []byte(`{}`),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Create(context.Background(), def)
	require.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreCreate_NameTaken(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("INSERT INTO api_keys").
		WillReturnResult(driver.RowsAffected(0))

	err := store.Create(context.Background(), Definition{Name: "taken"})
	assert.ErrorIs(t, err, ErrExists)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreCreate_ExecError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("INSERT INTO api_keys").
		WillReturnError(errors.New("exec error"))

	err := store.Create(context.Background(), Definition{Name: "test"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrExists)
	assert.Contains(t, err.Error(), "inserting api key")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(apikeyFmtUnmetExpect, err)
	}
}

func TestPostgresAPIKeyStoreCreate_RowsAffectedError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)

	mock.ExpectExec("INSERT INTO api_keys").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))

	err := store.Create(context.Background(), Definition{Name: "test"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrExists)
	assert.Contains(t, err.Error(), "checking insert result")
}

func TestPostgresAPIKeyStoreHoldsKey(t *testing.T) {
	for _, held := range []bool{true, false} {
		store, mock := newTestAPIKeyStore(t)
		mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM api_keys WHERE name = \$1 AND key_hash = \$2\)`).
			WithArgs("ci", "$2a$10$hash").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(held))

		got, err := store.HoldsKey(context.Background(), "ci", "$2a$10$hash")
		require.NoError(t, err)
		assert.Equal(t, held, got)
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(apikeyFmtUnmetExpect, err)
		}
	}
}

func TestPostgresAPIKeyStoreHoldsKey_QueryError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("connection reset"))

	held, err := store.HoldsKey(context.Background(), "ci", "$2a$10$hash")
	require.Error(t, err)
	assert.False(t, held)
	assert.Contains(t, err.Error(), "checking api key")
}

func TestPostgresAPIKeyStoreHashedKeys(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	exp := time.Now().Add(time.Hour).UTC()
	mock.ExpectQuery("SELECT name, key_hash").
		WillReturnRows(sqlmock.NewRows(apikeyColumns).
			AddRow("ci", "$2a$10$hash", "ci@example.com", "pipeline", []byte(`["analyst"]`), exp, "admin@example.com", time.Now(), "analyst@example.com", []byte(`{}`)))

	keys, err := store.HashedKeys(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "ci", keys[0].Name)
	// The account a key is issued against reaches the authenticator with it,
	// which is what makes the key resolve to that person (#1759).
	assert.Equal(t, "analyst@example.com", keys[0].UserEmail)
	assert.Equal(t, "$2a$10$hash", keys[0].KeyHash)
	assert.Empty(t, keys[0].Key, "a stored key carries no plaintext")
	assert.Equal(t, "ci@example.com", keys[0].Email)
	assert.Equal(t, "pipeline", keys[0].Description)
	assert.Equal(t, []string{"analyst"}, keys[0].Roles)
	require.NotNil(t, keys[0].ExpiresAt)
	assert.True(t, exp.Equal(*keys[0].ExpiresAt))
}

func TestPostgresAPIKeyStoreHashedKeys_ListError(t *testing.T) {
	store, mock := newTestAPIKeyStore(t)
	mock.ExpectQuery("SELECT name, key_hash").WillReturnError(errors.New("connection reset"))

	keys, err := store.HashedKeys(context.Background())
	require.Error(t, err)
	assert.Nil(t, keys)
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
	assert.ErrorIs(t, err, ErrNotFound)

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
	store := &NoopStore{}
	ctx := context.Background()

	t.Run("List returns nil nil", func(t *testing.T) {
		defs, err := store.List(ctx)
		assert.NoError(t, err)
		assert.Nil(t, defs)
	})

	t.Run("Create returns nil", func(t *testing.T) {
		err := store.Create(ctx, Definition{Name: "test"})
		assert.NoError(t, err)
	})

	t.Run("HashedKeys returns nil nil", func(t *testing.T) {
		keys, err := store.HashedKeys(ctx)
		assert.NoError(t, err)
		assert.Nil(t, keys)
	})

	t.Run("HoldsKey holds nothing", func(t *testing.T) {
		held, err := store.HoldsKey(ctx, "anything", "$2a$10$hash")
		assert.NoError(t, err)
		assert.False(t, held)
	})

	t.Run("Delete returns ErrNotFound", func(t *testing.T) {
		err := store.Delete(ctx, "anything")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}
