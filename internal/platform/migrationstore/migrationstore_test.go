package migrationstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

var migrationColumns = []string{"connection", "schema_name", "version", "name", "up_sql", "down_sql", "transactional", "checksum", "author", "created_at", "updated_at"}

func TestList(t *testing.T) {
	s, mock := newMock(t)
	now := time.Now()
	mock.ExpectQuery("SELECT connection, schema_name, version").WithArgs("orders", "public").
		WillReturnRows(sqlmock.NewRows(migrationColumns).AddRow("orders", "public", 1, "orders", "CREATE TABLE o ()", "", true, "c", "a@example.com", now, now))
	got, err := s.List(context.Background(), "orders", "public")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "CREATE TABLE o ()", got[0].Up)
	assert.True(t, got[0].Transactional)

	mock.ExpectQuery("SELECT connection").WillReturnError(errors.New("down"))
	_, err = s.List(context.Background(), "orders", "public")
	require.ErrorContains(t, err, "listing migrations")

	mock.ExpectQuery("SELECT connection").WillReturnRows(sqlmock.NewRows([]string{"connection"}).AddRow("x"))
	_, err = s.List(context.Background(), "orders", "public")
	require.ErrorContains(t, err, "reading a migration")

	mock.ExpectQuery("SELECT connection").WillReturnRows(sqlmock.NewRows(migrationColumns))
	empty, err := s.List(context.Background(), "orders", "public")
	require.NoError(t, err)
	assert.NotNil(t, empty, "an empty list is [], never null")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPut(t *testing.T) {
	s, mock := newMock(t)
	m := postgres.Migration{Connection: "orders", Schema: "public", Version: 2, Name: "items", Up: "u", Checksum: "c", Transactional: true}
	mock.ExpectQuery("INSERT INTO appdb_migrations").WithArgs("orders", "public", 2, "items", "u", "", true, "c", "").
		WillReturnRows(sqlmock.NewRows([]string{"inserted"}).AddRow(true))
	replaced, err := s.Put(context.Background(), m)
	require.NoError(t, err)
	assert.False(t, replaced)
	mock.ExpectQuery("INSERT INTO appdb_migrations").WillReturnRows(sqlmock.NewRows([]string{"inserted"}).AddRow(false))
	replaced, err = s.Put(context.Background(), m)
	require.NoError(t, err)
	assert.True(t, replaced)
	mock.ExpectQuery("INSERT INTO appdb_migrations").WillReturnError(errors.New("down"))
	_, err = s.Put(context.Background(), m)
	require.ErrorContains(t, err, "saving migration 2")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRecordAndEvents(t *testing.T) {
	s, mock := newMock(t)
	e := postgres.MigrationEvent{Connection: "orders", Schema: "public", Version: 1, Event: postgres.EventApplied, Actor: "a", DurationMS: 4}
	mock.ExpectExec("INSERT INTO appdb_migration_events").WithArgs("orders", "public", 1, "applied", "a", "", int64(4), "").
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, s.Record(context.Background(), e))
	mock.ExpectExec("INSERT INTO appdb_migration_events").WillReturnError(errors.New("down"))
	require.ErrorContains(t, s.Record(context.Background(), e), "recording")

	cols := []string{"connection", "schema_name", "version", "event", "actor", "checksum", "duration_ms", "detail", "at"}
	mock.ExpectQuery("FROM appdb_migration_events").WithArgs("orders", "public", 5).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("orders", "public", 1, "applied", "a", "c", 4, "", time.Now()))
	events, err := s.Events(context.Background(), "orders", "public", 5)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, int64(4), events[0].DurationMS)
	mock.ExpectQuery("FROM appdb_migration_events").WillReturnError(errors.New("down"))
	_, err = s.Events(context.Background(), "orders", "public", 5)
	require.Error(t, err)
	mock.ExpectQuery("FROM appdb_migration_events").WillReturnRows(sqlmock.NewRows([]string{"connection"}).AddRow("x"))
	_, err = s.Events(context.Background(), "orders", "public", 5)
	require.ErrorContains(t, err, "reading a migration event")
	require.NoError(t, mock.ExpectationsWereMet())
}
