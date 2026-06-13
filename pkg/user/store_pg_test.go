package user

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(db), mock, func() { _ = db.Close() }
}

func TestPostgresStore_Observe(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("INSERT INTO users").
		WithArgs("a@b.io", "Marcus", "Johnson").
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := store.Observe(context.Background(), "a@b.io", "Marcus", "Johnson"); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_Observe_Error(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("INSERT INTO users").
		WillReturnError(errors.New("connection reset"))

	if err := store.Observe(context.Background(), "a@b.io", "", ""); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestPostgresStore_Insert(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("INSERT INTO users").
		WithArgs("a@b.io", "Marcus", "Johnson", SourceAdmin, false, "admin@b.io").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := store.Insert(context.Background(), User{
		Email: "a@b.io", FirstName: "Marcus", LastName: "Johnson",
		AddedBy: "admin@b.io",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_Insert_Duplicate(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("INSERT INTO users").
		WillReturnError(&pq.Error{Code: pgUniqueViolation})

	err := store.Insert(context.Background(), User{Email: "a@b.io"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestPostgresStore_Get(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"email", "first_name", "last_name", "source", "confirmed", "added_by",
		"last_seen_at", "created_at", "updated_at",
	}).AddRow("a@b.io", "Marcus", "Johnson", "auth", true, "", now, now, now)
	mock.ExpectQuery("SELECT .+ FROM users WHERE email = \\$1").
		WithArgs("a@b.io").
		WillReturnRows(rows)

	u, err := store.Get(context.Background(), "a@b.io")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if u.Email != "a@b.io" || u.FirstName != "Marcus" || !u.Confirmed {
		t.Errorf("unexpected user: %+v", u)
	}
	if u.LastSeenAt == nil {
		t.Error("expected non-nil LastSeenAt")
	}
}

func TestPostgresStore_Get_NotFound(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT .+ FROM users WHERE email = \\$1").
		WithArgs("missing@b.io").
		WillReturnError(sql.ErrNoRows)

	_, err := store.Get(context.Background(), "missing@b.io")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPostgresStore_List(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"email", "first_name", "last_name", "source", "confirmed", "added_by",
		"last_seen_at", "created_at", "updated_at",
	}).AddRow("a@b.io", "Marcus", "Johnson", "auth", true, "", nil, now, now)
	mock.ExpectQuery("SELECT .+ FROM users").
		WillReturnRows(rows)

	users, total, err := store.List(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(users) != 1 {
		t.Fatalf("expected 1 user, got total=%d len=%d", total, len(users))
	}
	if users[0].LastSeenAt != nil {
		t.Error("expected nil LastSeenAt for null column")
	}
}

func TestPostgresStore_List_ClampsLimit(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"email", "first_name", "last_name", "source", "confirmed", "added_by",
		"last_seen_at", "created_at", "updated_at",
	}).AddRow("a@b.io", "A", "B", "auth", true, "", nil, now, now)
	// An over-large requested limit must be clamped to MaxListLimit in the args.
	mock.ExpectQuery("SELECT .+ FROM users").
		WithArgs(MaxListLimit, 0).
		WillReturnRows(rows)

	if _, _, err := store.List(context.Background(), Filter{Limit: 10000}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_List_Empty(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	users, total, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 || users != nil {
		t.Errorf("expected empty result, got total=%d users=%v", total, users)
	}
}

func TestPostgresStore_List_WithQuery(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	// Count query carries the ILIKE arg.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM users WHERE").
		WithArgs("%mar%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	_, total, err := store.List(context.Background(), Filter{Query: "mar"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0, got %d", total)
	}
}

func TestPostgresStore_Update(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	first := "Marc"
	mock.ExpectExec("UPDATE users SET").
		WithArgs("Marc", "a@b.io").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Update(context.Background(), "a@b.io", Update{FirstName: &first}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStore_Update_NotFound(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	last := "Smith"
	mock.ExpectExec("UPDATE users SET").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Update(context.Background(), "missing@b.io", Update{LastName: &last})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPostgresStore_Delete(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("DELETE FROM users WHERE email = \\$1").
		WithArgs("a@b.io").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.Delete(context.Background(), "a@b.io"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPostgresStore_Delete_NotFound(t *testing.T) {
	store, mock, done := newMockStore(t)
	defer done()

	mock.ExpectExec("DELETE FROM users WHERE email = \\$1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.Delete(context.Background(), "missing@b.io")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestBuildUserWhere(t *testing.T) {
	where, args := buildUserWhere(Filter{})
	if where != "" || args != nil {
		t.Errorf("empty query should produce no clause, got %q %v", where, args)
	}

	where, args = buildUserWhere(Filter{Query: "ma_n%"})
	if where == "" {
		t.Fatal("expected a WHERE clause")
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
	// LIKE metacharacters must be escaped inside the pattern.
	got, _ := args[0].(string)
	if got != "%ma\\_n\\%%" {
		t.Errorf("pattern not escaped: %q", got)
	}
}
