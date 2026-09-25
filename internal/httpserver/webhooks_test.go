package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// TestWithoutCORS_HooksAnswerTheirOwnOptions proves an OPTIONS to /hooks/
// reaches the receiver, which answers the CloudEvents handshake or refuses it,
// while every other path keeps the CORS preflight answer (#1870).
func TestWithoutCORS_HooksAnswerTheirOwnOptions(t *testing.T) {
	receiver := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	h := withoutCORS(receiver, corsMiddleware(http.NotFoundHandler()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/hooks/esp", http.NoBody))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), "a webhook is never a browser request")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/v1/portal/assets", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestWebhookMountsWithoutPlatform(t *testing.T) {
	assert.Nil(t, buildWebhooks(nil, ":8080"))
	mountWebhookAdminAPI(http.NewServeMux(), nil, nil)
}

// TestWebhookSourceRefs names a webhook table's source in the Scratch Tables
// listing, and leaves out one that is gone so the listing reports it missing.
func TestWebhookSourceRefs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cols := []string{"name", "enabled", "auth", "config", "connection_name", "created_by", "created_at", "updated_at"}
	mock.ExpectQuery(`FROM webhook_sources WHERE name`).WithArgs("esp").WillReturnRows(
		sqlmock.NewRows(cols).AddRow("esp", true, []byte(`{}`), []byte(`{}`), "scratch", "", time.Now(), time.Now()))
	mock.ExpectQuery(`FROM webhook_sources WHERE name`).WithArgs("gone").WillReturnRows(sqlmock.NewRows(cols))

	refs := webhookSourceRefs(context.Background(), whsource.NewStore(db, nil), []string{"esp", "gone"})
	require.Contains(t, refs, "esp")
	assert.Equal(t, "esp", refs["esp"].Name)
	assert.Contains(t, refs["esp"].Description, "/hooks/esp")
	assert.False(t, refs["esp"].CanModify, "a source's table is removed with the source")
	assert.NotContains(t, refs, "gone")
}
