package upstreamauth

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A session_login connection parsed and validated through the shared policy:
// the token header is reserved like any credential header, the authenticator
// leaves the request to the client, and the client carries the session (#2015).
func sessionConfig(t *testing.T, extra map[string]any) Config {
	t.Helper()
	cfg := map[string]any{
		"auth_mode": AuthModeSessionLogin, "session_login_url": "/signin", "session_login_secret": "s",
		"session_login_body": `{"s":"{{secret}}"}`, "session_token_source": "body:token", "session_token_header": "X-Tableau-Auth",
	}
	maps.Copy(cfg, extra)
	c, err := Parse("api", "apigateway", "https://h.example.com", cfg)
	require.NoError(t, err)
	require.NoError(t, c.Validate())
	return c
}

func TestSessionLoginRefusalSpeaksInTheKindsVoice(t *testing.T) {
	c, err := Parse("api", "apigateway", "https://h.example.com", map[string]any{"auth_mode": AuthModeSessionLogin})
	require.NoError(t, err)
	err = c.Validate()
	require.Error(t, err)
	assert.Equal(t, `apigateway: session_login_url is required when auth_mode is "session_login"`, err.Error())
}

func TestSessionLoginClientCarriesTheSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/signin" {
			_, _ = w.Write([]byte(`{"token":"tok"}`))
			return
		}
		_, _ = w.Write([]byte(r.Header.Get("X-Tableau-Auth")))
	}))
	t.Cleanup(srv.Close)
	c := sessionConfig(t, map[string]any{"session_login_url": srv.URL + "/signin"})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/x", http.NoBody)
	require.NoError(t, err)
	resp, err := NewHTTPClient(c).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 3)
	_, _ = resp.Body.Read(buf)
	assert.Equal(t, "tok", string(buf))
	assert.False(t, IsSessionFailure(err))
}

func TestSessionLoginTokenHeaderIsReserved(t *testing.T) {
	c := sessionConfig(t, nil)
	assert.Equal(t, "X-Tableau-Auth", c.AuthHeader())
	assert.Error(t, c.ValidateCustomHeaders(map[string]string{"x-tableau-auth": "forged"}))
	c.StaticHeaders = map[string]string{"X-Tableau-Auth": "fixed"}
	assert.Error(t, c.ValidateStaticHeaders())

	auth, err := NewAuthenticator(c)
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://h.example.com/x", http.NoBody)
	require.NoError(t, auth.Apply(req))
	assert.Empty(t, req.Header, "the session is applied by the client, not the authenticator")
}

// A path secret belongs on a call's path, not on the sign-in, which goes to
// its own URL.
func TestSessionLoginSignInDoesNotCarryThePathSecret(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/signin" {
			_, _ = w.Write([]byte(`{"token":"tok"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := sessionConfig(t, map[string]any{"session_login_url": srv.URL + "/signin", "path_secret": "s3cret"})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/hook", http.NoBody)
	require.NoError(t, err)
	resp, err := NewHTTPClient(c).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, []string{"/signin", "/hook/s3cret"}, paths)
}
