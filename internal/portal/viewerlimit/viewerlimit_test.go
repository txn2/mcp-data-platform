package viewerlimit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// The token-bucket internals (refill, eviction, cleanup loop) and the
// two-layer HTTPLimiter admission logic are tested in pkg/ratelimit, which owns
// the implementation. These tests cover the portal wrapper's public behavior:
// per-IP allowance, the HTTP middleware (including the Retry-After header), and
// the spoof-resistant, trusted-proxy-aware client attribution the wrapper now
// delegates to ratelimit.Resolver (#904).

// newReq builds a GET request with the given RemoteAddr and optional
// X-Forwarded-For header.
func newReq(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestRateLimiterAllow(t *testing.T) {
	rl := New(Config{RequestsPerMinute: 60, BurstSize: 5}, nil)
	defer rl.Close()

	req := newReq("1.2.3.4:1111", "")
	// Burst should allow 5 requests.
	for i := range 5 {
		assert.True(t, rl.Allow(req), "request %d should be allowed", i)
	}
	// 6th should be denied.
	assert.False(t, rl.Allow(req))
}

func TestRateLimiterDifferentIPs(t *testing.T) {
	rl := New(Config{RequestsPerMinute: 60, BurstSize: 2}, nil)
	defer rl.Close()

	req1 := newReq("1.1.1.1:1111", "")
	assert.True(t, rl.Allow(req1))
	assert.True(t, rl.Allow(req1))
	assert.False(t, rl.Allow(req1))

	// A different peer IP should have its own bucket.
	req2 := newReq("2.2.2.2:2222", "")
	assert.True(t, rl.Allow(req2))
}

func TestRateLimiterDefaults(t *testing.T) {
	rl := New(Config{}, nil)
	defer rl.Close()
	// Should not panic; defaults applied by the shared limiter.
	assert.True(t, rl.Allow(newReq("9.9.9.9:9999", "")))
}

// TestRateLimiterGlobalBackstopSizing guards the default-config sizing bug: with
// an empty portal.rate_limit block (RPM/Burst 0), the per-IP defaults must be
// resolved BEFORE the HTTPLimiter is built so the global backstop is 10x the
// per-IP default (burst 100), not collapsed to the per-IP default (burst 10).
// Firing >10 requests from distinct IPs (so per-IP never limits) must all pass;
// before the fix the 11th tripped the undersized global bucket.
func TestRateLimiterGlobalBackstopSizing(t *testing.T) {
	rl := New(Config{}, nil)
	defer rl.Close()

	for i := range 50 {
		req := newReq(fmt.Sprintf("203.0.113.%d:1000", i), "")
		assert.True(t, rl.Allow(req),
			"request %d from a distinct IP must pass the global backstop (burst 100, not 10)", i)
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	rl := New(Config{RequestsPerMinute: 60, BurstSize: 1}, nil)
	defer rl.Close()

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request allowed.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newReq("10.0.0.1:1234", ""))
	assert.Equal(t, http.StatusOK, w.Code)

	// Second request denied, with a Retry-After header advising a backoff.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, newReq("10.0.0.1:1234", ""))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

// TestRateLimiterIgnoresSpoofedXFF is the core #904 regression: with no trusted
// proxies configured (nil resolver), a rotating X-Forwarded-For no longer mints
// a fresh per-IP bucket. All requests from one peer share one bucket regardless
// of the spoofed header, so the per-IP limit holds.
func TestRateLimiterIgnoresSpoofedXFF(t *testing.T) {
	rl := New(Config{RequestsPerMinute: 60, BurstSize: 2}, nil)
	defer rl.Close()

	// Same peer, a different forged leftmost XFF on every request. Pre-#904 the
	// leftmost XFF was trusted, so each of these would have opened a new bucket
	// and never tripped the limit.
	assert.True(t, rl.Allow(newReq("5.5.5.5:1000", "1.1.1.1")))
	assert.True(t, rl.Allow(newReq("5.5.5.5:1001", "2.2.2.2")))
	assert.False(t, rl.Allow(newReq("5.5.5.5:1002", "3.3.3.3")),
		"forged XFF must not mint a fresh per-IP bucket")
}

// TestRateLimiterTrustedProxyAttribution verifies that when the direct peer is
// a configured trusted proxy, attribution uses the rightmost untrusted XFF hop
// (the real client), so distinct clients behind the proxy get distinct buckets.
func TestRateLimiterTrustedProxyAttribution(t *testing.T) {
	resolver, err := ratelimit.NewResolver([]string{"10.0.0.0/8"})
	require.NoError(t, err)

	rl := New(Config{RequestsPerMinute: 60, BurstSize: 1}, resolver)
	defer rl.Close()

	// Two real clients behind the same trusted proxy (10.1.2.3). Each is
	// attributed to its own XFF hop and gets its own single-token bucket.
	assert.True(t, rl.Allow(newReq("10.1.2.3:5000", "203.0.113.7")))
	assert.False(t, rl.Allow(newReq("10.1.2.3:5001", "203.0.113.7")),
		"second request from the same real client is over its per-IP limit")
	assert.True(t, rl.Allow(newReq("10.1.2.3:5002", "198.51.100.9")),
		"a different real client behind the proxy has its own bucket")
}

func TestRateLimiterNilResolverDefault(t *testing.T) {
	// A nil resolver must not panic and must behave as trust-none: two requests
	// from the same peer with different XFF headers share one bucket.
	rl := New(Config{RequestsPerMinute: 60, BurstSize: 1}, nil)
	defer rl.Close()

	assert.True(t, rl.Allow(newReq("7.7.7.7:1", "1.1.1.1")))
	assert.False(t, rl.Allow(newReq("7.7.7.7:2", "9.9.9.9")))
}

func TestRateLimiterClose(t *testing.T) {
	rl := New(Config{RequestsPerMinute: 60, BurstSize: 5}, nil)
	// Close should not panic and should be idempotent.
	rl.Close()
	rl.Close()

	// After close, Allow should still work (buckets are intact, cleanup stopped).
	assert.True(t, rl.Allow(newReq("8.8.8.8:1", "")))
}
