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

	"github.com/txn2/mcp-data-platform/internal/httpserver/corshttp"
	"github.com/txn2/mcp-data-platform/internal/httpserver/webhookwire"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// TestWithoutCORS_HooksAnswerTheirOwnOptions proves an OPTIONS to /hooks/
// reaches the receiver, which answers the CloudEvents handshake or refuses it,
// while every other path keeps the CORS preflight answer (#1870).
func TestWithoutCORS_HooksAnswerTheirOwnOptions(t *testing.T) {
	receiver := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	h := corshttp.WithoutHooks(receiver, corshttp.Middleware(http.NotFoundHandler()))

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
	(*webhookwire.Webhooks)(nil).MountAdmin(http.NewServeMux(), nil, buildAdminAuth, adminEmail)
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

// TestSecretAdminAPIMountsOnlyWithADatabase: a secret is a database row, so
// with no platform or no database the routes are not mounted.
func TestSecretAdminAPIMountsOnlyWithADatabase(t *testing.T) {
	mux := http.NewServeMux()
	mountDatabaseAdminAPIs(mux, nil)
	p := newTestPlatform(t, &platform.Config{Server: platform.ServerConfig{Name: "test"}, Admin: platform.AdminConfig{Enabled: new(true), Persona: "admin"}})
	defer func() { _ = p.Close() }()
	mountDatabaseAdminAPIs(mux, p)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/secrets", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestResourceIndexAdminAPIMountsOnlyWithADatabase: the counts and the
// reclassification read and write resources rows (#2111), so with no
// platform or no database the routes are not mounted.
func TestResourceIndexAdminAPIMountsOnlyWithADatabase(t *testing.T) {
	mux := http.NewServeMux()
	mountDatabaseAdminAPIs(mux, nil)
	p := newTestPlatform(t, &platform.Config{Server: platform.ServerConfig{Name: "test"}, Admin: platform.AdminConfig{Enabled: new(true), Persona: "admin"}})
	defer func() { _ = p.Close() }()
	mountDatabaseAdminAPIs(mux, p)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/resources/index", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWebhooksMountAdmin(t *testing.T) {
	sources := func(mux *http.ServeMux) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/admin/webhooks/sources", http.NoBody))
		return w.Code
	}
	on := newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test", Transport: "http"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Admin:    platform.AdminConfig{Enabled: new(true), Persona: "admin"},
		Personas: platform.PersonasConfig{Definitions: map[string]platform.PersonaDef{
			"admin": {Roles: []string{"admin"}, Tools: platform.ToolRulesDef{Allow: []string{"*"}}},
		}},
	})
	t.Cleanup(func() { _ = on.Close() })
	off := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Admin:  platform.AdminConfig{Enabled: new(false)},
	})
	t.Cleanup(func() { _ = off.Close() })

	mux := http.NewServeMux()
	(*webhookwire.Webhooks)(nil).MountAdmin(mux, on, buildAdminAuth, adminEmail)
	assert.Equal(t, http.StatusNotFound, sources(mux), "no webhooks, no routes")

	mux = http.NewServeMux()
	(&webhookwire.Webhooks{}).MountAdmin(mux, nil, buildAdminAuth, adminEmail)
	assert.Equal(t, http.StatusNotFound, sources(mux), "no platform, no routes")

	mux = http.NewServeMux()
	(&webhookwire.Webhooks{}).MountAdmin(mux, off, buildAdminAuth, adminEmail)
	assert.Equal(t, http.StatusNotFound, sources(mux), "the admin API off, no routes")

	mux = http.NewServeMux()
	(&webhookwire.Webhooks{}).MountAdmin(mux, on, buildAdminAuth, adminEmail)
	assert.Equal(t, http.StatusUnauthorized, sources(mux), "mounted behind the admin API's authentication")
}
