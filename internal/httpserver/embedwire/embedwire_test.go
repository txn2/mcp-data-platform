package embedwire

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/embedhosts"
)

func TestNewWithoutADatabaseServesTheConfiguredList(t *testing.T) {
	own := []string{"cdn.example.com"}
	r := New(nil, embedhosts.Config{Hosts: &own})
	assert.Equal(t, []string{"https://cdn.example.com"}, r.Hosts(context.Background()))

	mux := http.NewServeMux()
	Mount(mux, r, nil, func(h http.Handler) http.Handler { return h }, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/settings/embed-hosts", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "nothing to store an administrator's list in: no routes")
}

func TestSectionReadsAndWritesThePlatformSettingsRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := New(db, embedhosts.Config{})
	ctx := context.Background()

	mock.ExpectQuery(`SELECT value, updated_by, updated_at FROM platform_settings`).
		WithArgs(embedhosts.SettingsSection).WillReturnError(sql.ErrNoRows)
	assert.Equal(t, embedhosts.Default, r.Hosts(ctx), "a section never written serves the configured list")
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectExec(`INSERT INTO platform_settings`).
		WithArgs(embedhosts.SettingsSection, []byte(`{"hosts":["https://cdn.example.com"]}`), "admin").
		WillReturnResult(sqlmock.NewResult(0, 1))
	got, err := r.Write(ctx, []string{"cdn.example.com"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, []string{"https://cdn.example.com"}, got)
	require.NoError(t, mock.ExpectationsWereMet())

	s := section{embedhostsSection(db)}
	mock.ExpectQuery(`SELECT value`).WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
		AddRow([]byte(`{"hosts":["https://fast.wistia.net"]}`), "admin", time.Now()))
	v, written, err := s.Read(ctx)
	require.NoError(t, err)
	assert.True(t, written)
	assert.Equal(t, []string{"https://fast.wistia.net"}, v.Hosts)

	mock.ExpectQuery(`SELECT value`).WillReturnError(errors.New("down"))
	_, _, err = s.Read(ctx)
	require.Error(t, err)

	mux := http.NewServeMux()
	Mount(mux, r, db, func(h http.Handler) http.Handler { return h }, func(*http.Request) string { return "admin" })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/admin/settings/embed-hosts", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)
}
