package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

const (
	testDBError      = "db error"
	testHistoryLimit = 10
	testData         = "data"
	fmtUnmetExpect   = "unmet expectations: %v"
)

func newTestStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("creating sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db), mock
}

func TestPostgresStore_Mode(t *testing.T) {
	store, _ := newTestStore(t)
	if got := store.Mode(); got != "database" {
		t.Errorf("Mode() = %q, want %q", got, "database")
	}
}

func TestPostgresStore_Load_NoRows(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT config_yaml FROM config_versions").
		WillReturnError(sql.ErrNoRows)

	data, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if data != nil {
		t.Error("Load() expected nil data for first boot")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Load_WithConfig(t *testing.T) {
	store, mock := newTestStore(t)

	configYAML := "server:\n  name: test-server\n"
	rows := sqlmock.NewRows([]string{"config_yaml"}).AddRow(configYAML)
	mock.ExpectQuery("SELECT config_yaml FROM config_versions").
		WillReturnRows(rows)

	data, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if data == nil {
		t.Fatal("Load() returned nil data")
	}
	if string(data) != configYAML {
		t.Errorf("Load() = %q, want %q", string(data), configYAML)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Load_DBError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT config_yaml FROM config_versions").
		WillReturnError(errors.New(testDBError))

	_, err := store.Load(context.Background())
	if err == nil {
		t.Error("Load() expected error")
	}
}

func TestPostgresStore_Save_FirstVersion(t *testing.T) {
	store, mock := newTestStore(t)

	data := []byte("server:\n  name: test\n")

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(1))
	mock.ExpectExec("UPDATE config_versions SET is_active").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO config_versions").
		WithArgs(1, string(data), "admin", "initial").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := store.Save(context.Background(), data, configstore.SaveMeta{
		Author:  "admin",
		Comment: "initial",
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Save_SecondVersion(t *testing.T) {
	store, mock := newTestStore(t)

	data := []byte("server:\n  name: updated\n")

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(2))
	mock.ExpectExec("UPDATE config_versions SET is_active").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO config_versions").
		WithArgs(2, string(data), "user1", "update").
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	err := store.Save(context.Background(), data, configstore.SaveMeta{
		Author:  "user1",
		Comment: "update",
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Save_BeginError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectBegin().WillReturnError(errors.New(testDBError))

	err := store.Save(context.Background(), []byte(testData), configstore.SaveMeta{})
	if err == nil {
		t.Error("Save() expected error on begin failure")
	}
}

func TestPostgresStore_Save_VersionQueryError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE").
		WillReturnError(errors.New(testDBError))
	mock.ExpectRollback()

	err := store.Save(context.Background(), []byte(testData), configstore.SaveMeta{})
	if err == nil {
		t.Error("Save() expected error on version query failure")
	}
}

func TestPostgresStore_Save_DeactivateError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(1))
	mock.ExpectExec("UPDATE config_versions SET is_active").
		WillReturnError(errors.New(testDBError))
	mock.ExpectRollback()

	err := store.Save(context.Background(), []byte(testData), configstore.SaveMeta{})
	if err == nil {
		t.Error("Save() expected error on deactivate failure")
	}
}

func TestPostgresStore_Save_InsertError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(1))
	mock.ExpectExec("UPDATE config_versions SET is_active").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO config_versions").
		WithArgs(1, testData, "", "").
		WillReturnError(errors.New(testDBError))
	mock.ExpectRollback()

	err := store.Save(context.Background(), []byte(testData), configstore.SaveMeta{})
	if err == nil {
		t.Error("Save() expected error on insert failure")
	}
}

func TestPostgresStore_Save_CommitError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE").
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(1))
	mock.ExpectExec("UPDATE config_versions SET is_active").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO config_versions").
		WithArgs(1, testData, "", "").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New(testDBError))

	err := store.Save(context.Background(), []byte(testData), configstore.SaveMeta{})
	if err == nil {
		t.Error("Save() expected error on commit failure")
	}
}

func TestPostgresStore_History(t *testing.T) {
	store, mock := newTestStore(t)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "version", "author", "comment", "created_at"}).
		AddRow(2, 2, "user1", "second save", now).
		AddRow(1, 1, "admin", "initial", now.Add(-time.Hour))

	mock.ExpectQuery("SELECT id, version, author, comment, created_at FROM config_versions").
		WithArgs(testHistoryLimit).
		WillReturnRows(rows)

	revisions, err := store.History(context.Background(), testHistoryLimit)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("History() returned %d revisions, want 2", len(revisions))
	}
	if revisions[0].Version != 2 {
		t.Errorf("revisions[0].Version = %d, want 2", revisions[0].Version)
	}
	if revisions[0].Author != "user1" {
		t.Errorf("revisions[0].Author = %q, want %q", revisions[0].Author, "user1")
	}
	if revisions[1].Version != 1 {
		t.Errorf("revisions[1].Version = %d, want 1", revisions[1].Version)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_History_Empty(t *testing.T) {
	store, mock := newTestStore(t)

	rows := sqlmock.NewRows([]string{"id", "version", "author", "comment", "created_at"})
	mock.ExpectQuery("SELECT id, version, author, comment, created_at FROM config_versions").
		WithArgs(testHistoryLimit).
		WillReturnRows(rows)

	revisions, err := store.History(context.Background(), testHistoryLimit)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(revisions) != 0 {
		t.Errorf("History() returned %d revisions, want 0", len(revisions))
	}
}

func TestPostgresStore_History_DBError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT id, version, author, comment, created_at FROM config_versions").
		WithArgs(testHistoryLimit).
		WillReturnError(errors.New(testDBError))

	_, err := store.History(context.Background(), testHistoryLimit)
	if err == nil {
		t.Error("History() expected error")
	}
}
