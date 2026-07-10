package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postForm issues a POST to the server's HTTP handler from the given peer
// address and returns the recorder.
func postForm(t *testing.T, server *Server, path, remoteAddr, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	return w
}

func TestTokenEndpointRateLimit(t *testing.T) {
	storage := &mockStorage{}
	// RPM is deliberately tiny (1 req/min => a token refills only once every
	// 60s) so the "third request is rejected" assertion cannot flake if the
	// handler is slow on a loaded CI runner: with a higher rate, enough
	// wall-clock could elapse between requests to refill a token. The burst is
	// what this test exercises, not the refill rate.
	server, err := NewServer(ServerConfig{
		Issuer:    "http://localhost:8080",
		RateLimit: RateLimitConfig{TokenRPM: 1, TokenBurst: 2},
	}, storage)
	require.NoError(t, err)
	defer func() { _ = server.Close() }()

	const ip1 = "1.2.3.4:5000"
	const body = "grant_type=authorization_code&client_id=x"

	// Burst of 2 from one IP: allowed (they fail the grant with 400, not 429).
	for i := range 2 {
		w := postForm(t, server, "/token", ip1, body)
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "request %d under the limit must not be 429", i)
	}

	// Third from the same IP: rate-limited.
	w := postForm(t, server, "/token", ip1, body)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"), "429 must carry Retry-After")
	assert.Contains(t, w.Body.String(), errSlowDown)

	// A different IP is unaffected.
	w = postForm(t, server, "/token", "9.9.9.9:5000", body)
	assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "distinct IP must have its own bucket")
}

func TestRegisterEndpointRateLimit(t *testing.T) {
	storage := &mockStorage{}
	server, err := NewServer(ServerConfig{
		Issuer:    "http://localhost:8080",
		DCR: DCRConfig{Enabled: true, AllowAllRedirectURIs: true},
		// RPM is deliberately tiny so no token can refill while the two burst
		// registrations run their (slow, per-call bcrypt) work: at RegisterRPM
		// 60 a loaded CI runner spent ~4s hashing and refilled enough tokens to
		// wrongly admit the over-limit request. See TestTokenEndpointRateLimit.
		RateLimit: RateLimitConfig{RegisterRPM: 1, RegisterBurst: 2},
	}, storage)
	require.NoError(t, err)
	defer func() { _ = server.Close() }()

	const ip1 = "1.2.3.4:5000"
	const body = `{"client_name":"x","redirect_uris":["https://example.com/cb"]}`

	for i := range 2 {
		w := postForm(t, server, "/register", ip1, body)
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "request %d under the limit must not be 429", i)
	}

	w := postForm(t, server, "/register", ip1, body)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))

	w = postForm(t, server, "/register", "9.9.9.9:5000", body)
	assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "distinct IP must have its own bucket")
}

func TestRateLimitDisabled(t *testing.T) {
	storage := &mockStorage{}
	server, err := NewServer(ServerConfig{
		Issuer:    "http://localhost:8080",
		RateLimit: RateLimitConfig{Enabled: new(false), TokenBurst: 1},
	}, storage)
	require.NoError(t, err)
	defer func() { _ = server.Close() }()

	// Far past any burst; with limiting off none is 429.
	for range 50 {
		w := postForm(t, server, "/token", "1.2.3.4:5000", "grant_type=authorization_code&client_id=x")
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code)
	}
}

func TestRateLimitTrustedProxyInvalidCIDR(t *testing.T) {
	storage := &mockStorage{}
	_, err := NewServer(ServerConfig{
		RateLimit: RateLimitConfig{TrustedProxies: []string{"nonsense"}},
	}, storage)
	require.Error(t, err, "malformed trusted-proxy CIDR must fail construction")
}

func TestRateLimitConfigIsEnabled(t *testing.T) {
	assert.True(t, RateLimitConfig{}.isEnabled(), "nil Enabled defaults to on")
	assert.True(t, RateLimitConfig{Enabled: new(true)}.isEnabled())
	assert.False(t, RateLimitConfig{Enabled: new(false)}.isEnabled())
}

func TestOrDefault(t *testing.T) {
	assert.Equal(t, 5, orDefault(5, 10))
	assert.Equal(t, 10, orDefault(0, 10))
	assert.Equal(t, 10, orDefault(-1, 10))
}
