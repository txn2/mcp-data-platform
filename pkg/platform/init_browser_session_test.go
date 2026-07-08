package platform

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeOIDCDiscovery serves the minimal OIDC discovery document browsersession's
// NewFlow fetches at construction, so initBrowserSession can run end to end
// without a real identity provider.
func fakeOIDCDiscovery(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var serverURL string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": serverURL + "/auth",
			"token_endpoint":         serverURL + "/token",
			"end_session_endpoint":   serverURL + "/logout",
			"userinfo_endpoint":      serverURL + "/userinfo",
		})
	})
	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func TestInitBrowserSession(t *testing.T) {
	// 32 raw bytes → satisfies browsersession's minimum cookie key length.
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	t.Run("enabled builds flow and authenticator", func(t *testing.T) {
		srv := fakeOIDCDiscovery(t)
		p := &Platform{config: &Config{
			Auth: AuthConfig{
				OIDC:           OIDCAuthConfig{Enabled: true, Issuer: srv.URL, ClientID: "c"},
				BrowserSession: BrowserSessionConfig{Enabled: true, CookieName: "sess", SigningKey: key},
			},
			Portal: PortalConfig{PublicBaseURL: "https://portal.example"},
		}}
		require.NoError(t, p.initBrowserSession())
		require.NotNil(t, p.BrowserSessionFlow(), "flow must be wired when enabled")
		require.NotNil(t, p.BrowserSessionAuth(), "cookie authenticator must be wired when enabled")
	})

	t.Run("disabled is a no-op with nil accessors", func(t *testing.T) {
		p := &Platform{config: &Config{}}
		require.NoError(t, p.initBrowserSession())
		require.Nil(t, p.BrowserSessionFlow())
		require.Nil(t, p.BrowserSessionAuth())
	})

	t.Run("flow construction error is wrapped", func(t *testing.T) {
		// Enabled with an empty issuer: NewFlow rejects it and the error must
		// surface through initBrowserSession.
		p := &Platform{config: &Config{
			Auth: AuthConfig{
				OIDC:           OIDCAuthConfig{Enabled: true, ClientID: "c"},
				BrowserSession: BrowserSessionConfig{Enabled: true, SigningKey: key},
			},
		}}
		err := p.initBrowserSession()
		require.Error(t, err)
		require.Contains(t, err.Error(), "browser session")
	})
}
