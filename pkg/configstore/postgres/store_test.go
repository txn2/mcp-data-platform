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
	testDBError    = "db error"
	fmtUnmetExpect = "unmet expectations: %v"
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

// --- Get ---

func TestPostgresStore_Get_Success(t *testing.T) {
	store, mock := newTestStore(t)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"key", "value_text", "updated_by", "updated_at"}).
		AddRow("server.name", "test-server", "admin", now)

	mock.ExpectQuery("SELECT key, value_text, updated_by, updated_at FROM config_entries WHERE key").
		WithArgs("server.name").
		WillReturnRows(rows)

	entry, err := store.Get(context.Background(), "server.name")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if entry.Key != "server.name" {
		t.Errorf("Key = %q, want %q", entry.Key, "server.name")
	}
	if entry.Value != "test-server" {
		t.Errorf("Value = %q, want %q", entry.Value, "test-server")
	}
	if entry.UpdatedBy != "admin" {
		t.Errorf("UpdatedBy = %q, want %q", entry.UpdatedBy, "admin")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Get_NotFound(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT key, value_text, updated_by, updated_at FROM config_entries WHERE key").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	_, err := store.Get(context.Background(), "missing")
	if !errors.Is(err, configstore.ErrNotFound) {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Get_DBError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT key, value_text, updated_by, updated_at FROM config_entries WHERE key").
		WithArgs("key").
		WillReturnError(errors.New(testDBError))

	_, err := store.Get(context.Background(), "key")
	if err == nil {
		t.Error("Get() expected error")
	}
}

// --- Set ---

func TestPostgresStore_Set_Success(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("INSERT INTO config_entries").
		WithArgs("server.name", "new-val", "admin", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectExec("INSERT INTO config_changelog").
		WithArgs("server.name", "new-val", "admin", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.Set(context.Background(), "server.name", "new-val", "admin")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Set_UpsertError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("INSERT INTO config_entries").
		WithArgs("key", "val", "admin", sqlmock.AnyArg()).
		WillReturnError(errors.New(testDBError))

	err := store.Set(context.Background(), "key", "val", "admin")
	if err == nil {
		t.Error("Set() expected error on upsert failure")
	}
}

func TestPostgresStore_Set_ChangelogError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("INSERT INTO config_entries").
		WithArgs("key", "val", "admin", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectExec("INSERT INTO config_changelog").
		WithArgs("key", "val", "admin", sqlmock.AnyArg()).
		WillReturnError(errors.New(testDBError))

	err := store.Set(context.Background(), "key", "val", "admin")
	if err == nil {
		t.Error("Set() expected error on changelog failure")
	}
}

// --- Delete ---

func TestPostgresStore_Delete_Success(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("DELETE FROM config_entries WHERE key").
		WithArgs("server.name").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec("INSERT INTO config_changelog").
		WithArgs("server.name", "admin", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.Delete(context.Background(), "server.name", "admin")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Delete_NotFound(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("DELETE FROM config_entries WHERE key").
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Delete(context.Background(), "missing", "admin")
	if !errors.Is(err, configstore.ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Delete_DBError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("DELETE FROM config_entries WHERE key").
		WithArgs("key").
		WillReturnError(errors.New(testDBError))

	err := store.Delete(context.Background(), "key", "admin")
	if err == nil {
		t.Error("Delete() expected error")
	}
}

func TestPostgresStore_Delete_ChangelogError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectExec("DELETE FROM config_entries WHERE key").
		WithArgs("key").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec("INSERT INTO config_changelog").
		WithArgs("key", "admin", sqlmock.AnyArg()).
		WillReturnError(errors.New(testDBError))

	err := store.Delete(context.Background(), "key", "admin")
	if err == nil {
		t.Error("Delete() expected error on changelog failure")
	}
}

// --- List ---

func TestPostgresStore_List_Success(t *testing.T) {
	store, mock := newTestStore(t)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"key", "value_text", "updated_by", "updated_at"}).
		AddRow("a.key", "alpha", "admin", now).
		AddRow("b.key", "bravo", "user1", now)

	mock.ExpectQuery("SELECT key, value_text, updated_by, updated_at FROM config_entries ORDER BY key").
		WillReturnRows(rows)

	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List() returned %d entries, want 2", len(entries))
	}
	if entries[0].Key != "a.key" || entries[0].Value != "alpha" {
		t.Errorf("entries[0] = {%q, %q}, want {%q, %q}", entries[0].Key, entries[0].Value, "a.key", "alpha")
	}
	if entries[1].Key != "b.key" || entries[1].Value != "bravo" {
		t.Errorf("entries[1] = {%q, %q}, want {%q, %q}", entries[1].Key, entries[1].Value, "b.key", "bravo")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_List_Empty(t *testing.T) {
	store, mock := newTestStore(t)

	rows := sqlmock.NewRows([]string{"key", "value_text", "updated_by", "updated_at"})
	mock.ExpectQuery("SELECT key, value_text, updated_by, updated_at FROM config_entries ORDER BY key").
		WillReturnRows(rows)

	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List() returned %d entries, want 0", len(entries))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_List_DBError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT key, value_text, updated_by, updated_at FROM config_entries ORDER BY key").
		WillReturnError(errors.New(testDBError))

	_, err := store.List(context.Background())
	if err == nil {
		t.Error("List() expected error")
	}
}

// --- Changelog ---

func TestPostgresStore_Changelog_Success(t *testing.T) {
	store, mock := newTestStore(t)

	now := time.Now()
	val := "new-value"
	rows := sqlmock.NewRows([]string{"id", "key", "action", "value_text", "changed_by", "changed_at"}).
		AddRow(2, "server.name", "set", val, "admin", now).
		AddRow(1, "old.key", "delete", nil, "user1", now.Add(-time.Hour))

	mock.ExpectQuery("SELECT id, key, action, value_text, changed_by, changed_at FROM config_changelog").
		WithArgs(10).
		WillReturnRows(rows)

	entries, err := store.Changelog(context.Background(), 10)
	if err != nil {
		t.Fatalf("Changelog() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Changelog() returned %d entries, want 2", len(entries))
	}

	// First entry: "set" with a value.
	if entries[0].ID != 2 {
		t.Errorf("entries[0].ID = %d, want 2", entries[0].ID)
	}
	if entries[0].Action != "set" {
		t.Errorf("entries[0].Action = %q, want %q", entries[0].Action, "set")
	}
	if entries[0].Value == nil || *entries[0].Value != val {
		t.Errorf("entries[0].Value = %v, want %q", entries[0].Value, val)
	}

	// Second entry: "delete" with NULL value.
	if entries[1].Action != "delete" {
		t.Errorf("entries[1].Action = %q, want %q", entries[1].Action, "delete")
	}
	if entries[1].Value != nil {
		t.Errorf("entries[1].Value = %v, want nil", entries[1].Value)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(fmtUnmetExpect, err)
	}
}

func TestPostgresStore_Changelog_Empty(t *testing.T) {
	store, mock := newTestStore(t)

	rows := sqlmock.NewRows([]string{"id", "key", "action", "value_text", "changed_by", "changed_at"})
	mock.ExpectQuery("SELECT id, key, action, value_text, changed_by, changed_at FROM config_changelog").
		WithArgs(10).
		WillReturnRows(rows)

	entries, err := store.Changelog(context.Background(), 10)
	if err != nil {
		t.Fatalf("Changelog() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Changelog() returned %d entries, want 0", len(entries))
	}
}

func TestPostgresStore_Changelog_DBError(t *testing.T) {
	store, mock := newTestStore(t)

	mock.ExpectQuery("SELECT id, key, action, value_text, changed_by, changed_at FROM config_changelog").
		WithArgs(10).
		WillReturnError(errors.New(testDBError))

	_, err := store.Changelog(context.Background(), 10)
	if err == nil {
		t.Error("Changelog() expected error")
	}
}
