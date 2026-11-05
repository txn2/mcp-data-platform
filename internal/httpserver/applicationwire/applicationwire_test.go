package applicationwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appwire"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

func TestMount_WithoutAPlatform(t *testing.T) {
	assert.NotPanics(t, func() { Mount(http.NewServeMux(), nil, nil, nil, nil) })
}

func TestActor(t *testing.T) {
	actor := Actor(func(roles []string) bool { return len(roles) > 0 && roles[0] == "admin" })
	r := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
	assert.Nil(t, actor(r), "signed out")
	r = r.WithContext(portal.ContextWithUser(r.Context(), &portal.User{Email: "a@b", Roles: []string{"admin"}}))
	got := actor(r)
	require.NotNil(t, got)
	assert.True(t, got.Admin)
	assert.Equal(t, "a@b", got.Email)
	assert.Equal(t, []string{"admin"}, got.Roles, "an import made through the portal runs under the caller's own roles")
}

func TestRegister_MountsTheRoutes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	register(mux, func(h http.Handler) http.Handler { return h }, appwire.Deps{}, nil, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/applications", http.NoBody))
	assert.Equal(t, http.StatusNotFound, w.Code, "no database, no routes")

	register(mux, func(h http.Handler) http.Handler { return h }, appwire.Deps{DB: db}, func([]string) bool { return false }, nil)
	mock.ExpectQuery("FROM applications ORDER BY name").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	r := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), &portal.User{Email: "a@b"}),
		"GET", "/api/v1/portal/applications", http.NoBody)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	assert.NotEqual(t, http.StatusNotFound, w.Code, "the listing route is mounted")
}
