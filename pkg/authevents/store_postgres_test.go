package authevents

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newPostgresStoreForTest(t *testing.T) (*PostgresStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("creating sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), mock
}

func TestPostgresStoreInsertRejectsInvalid(t *testing.T) {
	s, _ := newPostgresStoreForTest(t)
	err := s.Insert(context.Background(), Event{Kind: "mcp", Name: "x", Type: "bogus", Actor: "a"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestPostgresStoreInsertSuccess(t *testing.T) {
	s, mock := newPostgresStoreForTest(t)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO connection_auth_events")).
		WithArgs("mcp", "x", string(TypeConnectStarted), "u@e.com", "idp.example.com", []byte(`{}`)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.Insert(context.Background(), Event{
		Kind: "mcp", Name: "x", Type: TypeConnectStarted, Actor: "u@e.com",
		IDPHost: "idp.example.com",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostgresStoreListSuccess(t *testing.T) {
	s, mock := newPostgresStoreForTest(t)
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{
		"id", "occurred_at", "connection_kind", "connection_name",
		"event_type", "actor", "idp_host", "detail",
	}).AddRow("uuid-1", now, "mcp", "x", string(TypeRefreshSucceeded),
		"system:background-refresh", "idp.example.com", []byte(`{"duration_ms":42}`))
	mock.ExpectQuery("SELECT id, occurred_at").
		WithArgs(10, "mcp", "x").
		WillReturnRows(rows)

	got, err := s.List(context.Background(), Filter{Kind: "mcp", Name: "x", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Type != TypeRefreshSucceeded || got[0].IDPHost != "idp.example.com" {
		t.Errorf("got %+v", got)
	}
}

func TestPostgresStoreListRequiresLimit(t *testing.T) {
	s, _ := newPostgresStoreForTest(t)
	if _, err := s.List(context.Background(), Filter{Kind: "mcp", Name: "x"}); err == nil {
		t.Error("List with Limit=0 should error")
	}
}

func TestPostgresStorePruneSuccess(t *testing.T) {
	s, mock := newPostgresStoreForTest(t)
	cutoff := time.Now().Add(-90 * 24 * time.Hour)
	mock.ExpectExec("DELETE FROM connection_auth_events").
		WithArgs(cutoff).
		WillReturnResult(sqlmock.NewResult(0, 7))

	n, err := s.Prune(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 7 {
		t.Errorf("Prune returned %d, want 7", n)
	}
}
