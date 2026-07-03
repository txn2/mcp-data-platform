package portal

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
	mw "github.com/txn2/mcp-data-platform/pkg/middleware"
)

// cookieRequest builds a request carrying a valid session cookie for subject.
func cookieRequest(t *testing.T, cfg browsersession.CookieConfig, method, subject string) *http.Request {
	t.Helper()
	token, err := browsersession.SignSession(
		browsersession.SessionClaims{UserID: subject, Roles: []string{"analyst"}},
		&cfg,
	)
	require.NoError(t, err)
	r := httptest.NewRequestWithContext(context.Background(), method, "/api/v1/portal/assets", http.NoBody)
	r.AddCookie(&http.Cookie{Name: browsersession.DefaultCookieName, Value: token, HttpOnly: true, Secure: true})
	return r
}

// TestPortalAuthenticateCSRF verifies the Authenticate choke point enforces
// CSRF only for cookie-authenticated, state-changing requests.
func TestPortalAuthenticateCSRF(t *testing.T) {
	cfg := browsersession.CookieConfig{Key: testSessionKey(), TTL: time.Hour}
	ba := browsersession.NewAuthenticator(cfg)
	pa := NewAuthenticator(&mockAuthenticator{}, WithBrowserAuth(ba))
	validToken := ba.IssueCSRFToken("cookie-user")

	t.Run("GET passes without CSRF header", func(t *testing.T) {
		user, err := pa.Authenticate(cookieRequest(t, cfg, http.MethodGet, "cookie-user"))
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.True(t, user.FromCookie)
	})

	t.Run("POST without CSRF header is rejected", func(t *testing.T) {
		user, err := pa.Authenticate(cookieRequest(t, cfg, http.MethodPost, "cookie-user"))
		assert.Nil(t, user)
		assert.True(t, errors.Is(err, browsersession.ErrCSRFInvalid))
	})

	t.Run("POST with valid CSRF header passes", func(t *testing.T) {
		r := cookieRequest(t, cfg, http.MethodPost, "cookie-user")
		r.Header.Set(browsersession.CSRFHeaderName, validToken)
		user, err := pa.Authenticate(r)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.True(t, user.FromCookie)
	})

	t.Run("POST with wrong CSRF header is rejected", func(t *testing.T) {
		r := cookieRequest(t, cfg, http.MethodPost, "cookie-user")
		r.Header.Set(browsersession.CSRFHeaderName, "bogus")
		user, err := pa.Authenticate(r)
		assert.Nil(t, user)
		assert.True(t, errors.Is(err, browsersession.ErrCSRFInvalid))
	})
}

// TestPortalAPIKeyExemptFromCSRF verifies token auth never triggers CSRF.
func TestPortalAPIKeyExemptFromCSRF(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{
		info: &mw.UserInfo{UserID: "api-user", Roles: []string{"analyst"}},
	})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/assets", http.NoBody)
	r.Header.Set("X-API-Key", "key")

	user, err := pa.Authenticate(r)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.False(t, user.FromCookie, "API-key user is not a cookie session")
}

// TestPortalIssueCSRFNoBrowserAuth verifies IssueCSRF is a no-op when cookie
// authentication is not configured.
func TestPortalIssueCSRFNoBrowserAuth(t *testing.T) {
	pa := NewAuthenticator(&mockAuthenticator{})
	assert.Empty(t, pa.IssueCSRF("anyone"))
}

// TestPortalCookieCSRFFailsOverToToken verifies that when a request carries
// both a session cookie (missing its CSRF token) and a valid API-key/Bearer
// token, it authenticates via the CSRF-exempt token rather than being
// rejected — a cross-site attacker cannot supply the token, so this is safe.
func TestPortalCookieCSRFFailsOverToToken(t *testing.T) {
	cfg := browsersession.CookieConfig{Key: testSessionKey(), TTL: time.Hour}
	ba := browsersession.NewAuthenticator(cfg)
	pa := NewAuthenticator(
		&mockAuthenticator{info: &mw.UserInfo{UserID: "api-user", Roles: []string{"analyst"}}},
		WithBrowserAuth(ba),
	)

	r := cookieRequest(t, cfg, http.MethodPost, "cookie-user") // cookie present, no CSRF header
	r.Header.Set("X-API-Key", "key")

	user, err := pa.Authenticate(r)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "api-user", user.UserID, "should authenticate via CSRF-exempt token")
	assert.False(t, user.FromCookie)
}

// TestRequirePortalAuthCSRFIntegration exercises the real middleware end to
// end: a cookie-authenticated state-changing request is blocked with 403
// unless it carries a valid X-CSRF-Token.
func TestRequirePortalAuthCSRFIntegration(t *testing.T) {
	cfg := browsersession.CookieConfig{Key: testSessionKey(), TTL: time.Hour}
	ba := browsersession.NewAuthenticator(cfg)
	pa := NewAuthenticator(&mockAuthenticator{}, WithBrowserAuth(ba))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequirePortalAuth(pa)(inner)

	t.Run("cookie GET is allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, cookieRequest(t, cfg, http.MethodGet, "cookie-user"))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("cookie POST without header is 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, cookieRequest(t, cfg, http.MethodPost, "cookie-user"))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("cookie POST with valid token is allowed", func(t *testing.T) {
		r := cookieRequest(t, cfg, http.MethodPost, "cookie-user")
		r.Header.Set(browsersession.CSRFHeaderName, ba.IssueCSRFToken("cookie-user"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestGetMeReturnsCSRFTokenForCookie verifies GET /me advertises the CSRF
// token to cookie sessions and omits it for token auth.
func TestGetMeReturnsCSRFTokenForCookie(t *testing.T) {
	cfg := browsersession.CookieConfig{Key: testSessionKey(), TTL: time.Hour}
	ba := browsersession.NewAuthenticator(cfg)
	pa := NewAuthenticator(&mockAuthenticator{}, WithBrowserAuth(ba))
	h := NewHandler(Deps{Authenticator: pa}, RequirePortalAuth(pa))

	t.Run("cookie session gets a token", func(t *testing.T) {
		r := cookieRequest(t, cfg, http.MethodGet, "cookie-user")
		r.URL.Path = "/api/v1/portal/me"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), ba.IssueCSRFToken("cookie-user"))
	})

	t.Run("token session gets no CSRF token", func(t *testing.T) {
		paToken := NewAuthenticator(
			&mockAuthenticator{info: &mw.UserInfo{UserID: "api-user", Roles: []string{"analyst"}}},
			WithBrowserAuth(ba),
		)
		hToken := NewHandler(Deps{Authenticator: paToken}, RequirePortalAuth(paToken))
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/me", http.NoBody)
		r.Header.Set("X-API-Key", "key")
		w := httptest.NewRecorder()
		hToken.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		assert.NotContains(t, w.Body.String(), "csrf_token")
	})
}
