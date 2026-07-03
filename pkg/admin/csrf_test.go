package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// csrfTestRegistry maps the "admin" role to the admin persona.
func csrfTestRegistry(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{
		Name:  "admin",
		Roles: []string{"admin"},
		Tools: persona.ToolRules{Allow: []string{"*"}},
	}))
	return reg
}

// adminCookieRequest builds a request carrying a valid admin session cookie.
func adminCookieRequest(t *testing.T, cfg browsersession.CookieConfig, method string) *http.Request {
	t.Helper()
	token, err := browsersession.SignSession(
		browsersession.SessionClaims{UserID: "admin-cookie", Roles: []string{"admin"}},
		&cfg,
	)
	require.NoError(t, err)
	r := httptest.NewRequestWithContext(context.Background(), method, "/api/v1/admin/personas", http.NoBody)
	r.AddCookie(&http.Cookie{Name: browsersession.DefaultCookieName, Value: token, HttpOnly: true, Secure: true})
	return r
}

func csrfTestAuthenticator(t *testing.T) (*PlatformAuthenticator, *browsersession.Authenticator, browsersession.CookieConfig) {
	t.Helper()
	cfg := browsersession.CookieConfig{Key: []byte("test-key-that-is-at-least-32-bytes-long!!"), TTL: time.Hour}
	ba := browsersession.NewAuthenticator(cfg)
	// The token authenticator resolves to an admin user, exercising the
	// API-key path's admin-persona check in the exemption test.
	mcp := &mockMCPAuthenticator{info: &middleware.UserInfo{UserID: "api-user", Roles: []string{"admin"}}}
	pa := NewPlatformAuthenticator(mcp, "admin", csrfTestRegistry(t), WithBrowserSessionAuth(ba))
	return pa, ba, cfg
}

// TestAdminAuthenticateCSRF verifies CSRF enforcement on the admin
// Authenticate choke point for cookie-authenticated requests.
func TestAdminAuthenticateCSRF(t *testing.T) {
	pa, ba, cfg := csrfTestAuthenticator(t)
	validToken := ba.IssueCSRFToken("admin-cookie")

	t.Run("GET passes without CSRF header", func(t *testing.T) {
		user, err := pa.Authenticate(adminCookieRequest(t, cfg, http.MethodGet))
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.True(t, user.FromCookie)
	})

	t.Run("POST without CSRF header is rejected", func(t *testing.T) {
		user, err := pa.Authenticate(adminCookieRequest(t, cfg, http.MethodPost))
		assert.Nil(t, user)
		assert.True(t, errors.Is(err, browsersession.ErrCSRFInvalid))
	})

	t.Run("DELETE with valid CSRF header passes", func(t *testing.T) {
		r := adminCookieRequest(t, cfg, http.MethodDelete)
		r.Header.Set(browsersession.CSRFHeaderName, validToken)
		user, err := pa.Authenticate(r)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.True(t, user.FromCookie)
	})
}

// TestRequirePersonaCSRF verifies the middleware maps a CSRF failure to 403
// (not 500) and lets a valid token through.
func TestRequirePersonaCSRF(t *testing.T) {
	pa, ba, cfg := csrfTestAuthenticator(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequirePersona(pa)(inner)

	t.Run("cookie POST without header is 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, adminCookieRequest(t, cfg, http.MethodPost))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("cookie POST with valid header is 200", func(t *testing.T) {
		r := adminCookieRequest(t, cfg, http.MethodPost)
		r.Header.Set(browsersession.CSRFHeaderName, ba.IssueCSRFToken("admin-cookie"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestAdminAPIKeyExemptFromCSRF verifies token auth is not CSRF-gated.
func TestAdminAPIKeyExemptFromCSRF(t *testing.T) {
	pa, _, _ := csrfTestAuthenticator(t)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/admin/personas", http.NoBody)
	r.Header.Set("X-API-Key", "admin-token")

	user, err := pa.Authenticate(r)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.False(t, user.FromCookie)
}

// TestAdminCookieCSRFFailsOverToToken verifies that a request carrying both a
// session cookie (missing its CSRF token) and a valid API key authenticates
// via the CSRF-exempt token rather than being rejected with 403.
func TestAdminCookieCSRFFailsOverToToken(t *testing.T) {
	pa, _, cfg := csrfTestAuthenticator(t)
	r := adminCookieRequest(t, cfg, http.MethodPost) // valid admin cookie, no CSRF header
	r.Header.Set("X-API-Key", "admin-token")

	user, err := pa.Authenticate(r)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "api-user", user.UserID, "should authenticate via CSRF-exempt token")
	assert.False(t, user.FromCookie)
}
