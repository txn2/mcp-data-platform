package platform

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	connTestDBError    = "db error"
	connFmtUnmetExpect = "unmet expectations: %v"
)

func newTestConnStore(t *testing.T) (*PostgresConnectionStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("creating sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresConnectionStore(db, nil), mock
}

// --- List ---

func TestPostgresConnectionStore_List(t *testing.T) {
	t.Run("success with results", func(t *testing.T) {
		store, mock := newTestConnStore(t)
		now := time.Now()

		config1, err := json.Marshal(map[string]any{"host": "trino.local", "port": float64(8080)})
		require.NoError(t, err)
		config2, err := json.Marshal(map[string]any{"url": "https://datahub.local"})
		require.NoError(t, err)

		rows := sqlmock.NewRows([]string{"kind", "name", "config", "description", "created_by", "updated_at"}).
			AddRow("datahub", "primary", config2, "Primary DataHub", "admin@test.com", now).
			AddRow("trino", "prod", config1, "Production Trino", "admin@test.com", now)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances ORDER BY kind, name").
			WillReturnRows(rows)

		instances, err := store.List(context.Background())
		require.NoError(t, err)
		require.Len(t, instances, 2)

		assert.Equal(t, "datahub", instances[0].Kind)
		assert.Equal(t, "primary", instances[0].Name)
		assert.Equal(t, "Primary DataHub", instances[0].Description)
		assert.Equal(t, "admin@test.com", instances[0].CreatedBy)
		assert.Equal(t, "https://datahub.local", instances[0].Config["url"])

		assert.Equal(t, "trino", instances[1].Kind)
		assert.Equal(t, "prod", instances[1].Name)
		assert.Equal(t, "trino.local", instances[1].Config["host"])

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("empty result", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		rows := sqlmock.NewRows([]string{"kind", "name", "config", "description", "created_by", "updated_at"})
		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances ORDER BY kind, name").
			WillReturnRows(rows)

		instances, err := store.List(context.Background())
		require.NoError(t, err)
		assert.Empty(t, instances)

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances ORDER BY kind, name").
			WillReturnError(errors.New(connTestDBError))

		_, err := store.List(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "querying connection instances")
	})

	t.Run("scan error on bad config JSON", func(t *testing.T) {
		store, mock := newTestConnStore(t)
		now := time.Now()

		rows := sqlmock.NewRows([]string{"kind", "name", "config", "description", "created_by", "updated_at"}).
			AddRow("trino", "bad", []byte("not-json"), "Bad config", "admin", now)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances ORDER BY kind, name").
			WillReturnRows(rows)

		_, err := store.List(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling connection config")
	})
}

// --- Get ---

func TestPostgresConnectionStore_Get(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store, mock := newTestConnStore(t)
		now := time.Now()

		configBytes, err := json.Marshal(map[string]any{"host": "trino.local"})
		require.NoError(t, err)

		row := sqlmock.NewRows([]string{"kind", "name", "config", "description", "created_by", "updated_at"}).
			AddRow("trino", "prod", configBytes, "Production Trino", "admin@test.com", now)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances WHERE kind").
			WithArgs("trino", "prod").
			WillReturnRows(row)

		inst, err := store.Get(context.Background(), "trino", "prod")
		require.NoError(t, err)
		assert.Equal(t, "trino", inst.Kind)
		assert.Equal(t, "prod", inst.Name)
		assert.Equal(t, "Production Trino", inst.Description)
		assert.Equal(t, "admin@test.com", inst.CreatedBy)
		assert.Equal(t, "trino.local", inst.Config["host"])

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("not found via empty result set", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances WHERE kind").
			WithArgs("trino", "missing").
			WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "config", "description", "created_by", "updated_at"}))

		_, err := store.Get(context.Background(), "trino", "missing")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrConnectionNotFound))

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("db error", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances WHERE kind").
			WithArgs("trino", "prod").
			WillReturnError(errors.New(connTestDBError))

		_, err := store.Get(context.Background(), "trino", "prod")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "querying connection instance")
	})

	t.Run("bad config JSON", func(t *testing.T) {
		store, mock := newTestConnStore(t)
		now := time.Now()

		row := sqlmock.NewRows([]string{"kind", "name", "config", "description", "created_by", "updated_at"}).
			AddRow("trino", "bad", []byte("not-json"), "Bad", "admin", now)

		mock.ExpectQuery("SELECT kind, name, config, description, created_by, updated_at FROM connection_instances WHERE kind").
			WithArgs("trino", "bad").
			WillReturnRows(row)

		_, err := store.Get(context.Background(), "trino", "bad")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling connection config")
	})
}

// --- Set ---

func TestPostgresConnectionStore_Set(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store, mock := newTestConnStore(t)
		now := time.Now()

		inst := ConnectionInstance{
			Kind:        "trino",
			Name:        "prod",
			Config:      map[string]any{"host": "trino.local"},
			Description: "Production Trino",
			CreatedBy:   "admin@test.com",
			UpdatedAt:   now,
		}

		mock.ExpectExec("INSERT INTO connection_instances").
			WithArgs("trino", "prod", sqlmock.AnyArg(), "Production Trino", "admin@test.com", now).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := store.Set(context.Background(), inst)
		require.NoError(t, err)

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("db error", func(t *testing.T) {
		store, mock := newTestConnStore(t)
		now := time.Now()

		inst := ConnectionInstance{
			Kind:      "trino",
			Name:      "prod",
			Config:    map[string]any{"host": "trino.local"},
			CreatedBy: "admin",
			UpdatedAt: now,
		}

		mock.ExpectExec("INSERT INTO connection_instances").
			WithArgs("trino", "prod", sqlmock.AnyArg(), "", "admin", now).
			WillReturnError(errors.New(connTestDBError))

		err := store.Set(context.Background(), inst)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "upserting connection instance")
	})
}

// --- Delete ---

func TestPostgresConnectionStore_Delete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		mock.ExpectExec("DELETE FROM connection_instances WHERE kind").
			WithArgs("trino", "prod").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := store.Delete(context.Background(), "trino", "prod")
		require.NoError(t, err)

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		mock.ExpectExec("DELETE FROM connection_instances WHERE kind").
			WithArgs("trino", "missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := store.Delete(context.Background(), "trino", "missing")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrConnectionNotFound))

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf(connFmtUnmetExpect, err)
		}
	})

	t.Run("db error", func(t *testing.T) {
		store, mock := newTestConnStore(t)

		mock.ExpectExec("DELETE FROM connection_instances WHERE kind").
			WithArgs("trino", "prod").
			WillReturnError(errors.New(connTestDBError))

		err := store.Delete(context.Background(), "trino", "prod")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deleting connection instance")
	})
}

// --- NoopConnectionStore ---

func TestNoopConnectionStore(t *testing.T) {
	store := &NoopConnectionStore{}

	t.Run("List returns nil", func(t *testing.T) {
		instances, err := store.List(context.Background())
		assert.NoError(t, err)
		assert.Nil(t, instances)
	})

	t.Run("Get returns ErrConnectionNotFound", func(t *testing.T) {
		inst, err := store.Get(context.Background(), "trino", "prod")
		assert.Nil(t, inst)
		assert.True(t, errors.Is(err, ErrConnectionNotFound))
	})

	t.Run("Set is a no-op", func(t *testing.T) {
		err := store.Set(context.Background(), ConnectionInstance{Kind: "trino", Name: "prod"})
		assert.NoError(t, err)
	})

	t.Run("Delete returns ErrConnectionNotFound", func(t *testing.T) {
		err := store.Delete(context.Background(), "trino", "prod")
		assert.True(t, errors.Is(err, ErrConnectionNotFound))
	})
}
