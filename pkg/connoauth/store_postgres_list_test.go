package connoauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const listQuery = `
		SELECT connection_kind, connection_name,
		       expires_at, refresh_expires_at, scope,
		       authenticated_by, authenticated_at, updated_at,
		       (refresh_token IS NOT NULL) AS has_refresh
		  FROM connection_oauth_tokens`

func TestPostgresStoreListSuccess(t *testing.T) {
	t.Parallel()
	store, mock := newMockPostgresStore(t)
	now := time.Now().Truncate(time.Second).UTC()
	rows := sqlmock.NewRows([]string{
		"connection_kind", "connection_name",
		"expires_at", "refresh_expires_at", "scope",
		"authenticated_by", "authenticated_at", "updated_at",
		"has_refresh",
	}).
		AddRow("mcp", "alpha", now.Add(time.Hour), now.Add(24*time.Hour), "openid",
			"u@e.com", now, now, true).
		AddRow("api", "beta", now.Add(time.Hour), nil, "",
			"u@e.com", now, now, false)
	mock.ExpectQuery(listQuery).WillReturnRows(rows)

	got, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	// First row: has refresh → RefreshToken sentinel is set.
	if got[0].RefreshToken == "" {
		t.Errorf("got[0] should be flagged as having a refresh token")
	}
	// Second row: no refresh.
	if got[1].RefreshToken != "" {
		t.Errorf("got[1] should NOT be flagged as having a refresh token")
	}
	// Null refresh_expires_at maps to zero time.
	if !got[1].RefreshExpiresAt.IsZero() {
		t.Errorf("got[1].RefreshExpiresAt should be zero")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}

func TestPostgresStoreListQueryError(t *testing.T) {
	t.Parallel()
	store, mock := newMockPostgresStore(t)
	mock.ExpectQuery(listQuery).WillReturnError(errors.New("connection refused"))

	if _, err := store.List(context.Background()); err == nil {
		t.Errorf("expected error from query failure")
	}
}

func TestPostgresStoreListScanError(t *testing.T) {
	t.Parallel()
	store, mock := newMockPostgresStore(t)
	// Return a row with one fewer column than the scan expects, which
	// makes Rows.Scan return an error.
	rows := sqlmock.NewRows([]string{"connection_kind"}).AddRow("mcp")
	mock.ExpectQuery(listQuery).WillReturnRows(rows)

	if _, err := store.List(context.Background()); err == nil {
		t.Errorf("expected scan error")
	}
}
