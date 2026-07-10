package portal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The token-bucket internals (refill, eviction, cleanup loop) are tested in
// pkg/ratelimit, which owns the implementation. These tests cover the portal
// wrapper's public behavior: per-IP allowance, the HTTP middleware, and the
// portal's leftmost-X-Forwarded-For client attribution.

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{RequestsPerMinute: 60, BurstSize: 5})
	defer rl.Close()

	// Burst should allow 5 requests.
	for i := range 5 {
		assert.True(t, rl.Allow("1.2.3.4"), "request %d should be allowed", i)
	}
	// 6th should be denied.
	assert.False(t, rl.Allow("1.2.3.4"))
}

func TestRateLimiterDifferentIPs(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{RequestsPerMinute: 60, BurstSize: 2})
	defer rl.Close()

	assert.True(t, rl.Allow("1.1.1.1"))
	assert.True(t, rl.Allow("1.1.1.1"))
	assert.False(t, rl.Allow("1.1.1.1"))

	// Different IP should have its own bucket.
	assert.True(t, rl.Allow("2.2.2.2"))
}

func TestRateLimiterDefaults(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{})
	defer rl.Close()
	// Should not panic, defaults applied.
	assert.True(t, rl.Allow("test"))
}

func TestRateLimiterMiddleware(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{RequestsPerMinute: 60, BurstSize: 1})
	defer rl.Close()

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request allowed.
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/test", http.NoBody)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Second request denied.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestClientIPXForwardedFor(t *testing.T) {
	tests := []struct {
		name     string
		xff      string
		remote   string
		expected string
	}{
		{"xff single", "1.2.3.4", "5.6.7.8:1234", "1.2.3.4"},
		{"xff multiple", "1.2.3.4, 5.6.7.8", "9.10.11.12:1234", "1.2.3.4"},
		{"no xff", "", "5.6.7.8:1234", "5.6.7.8"},
		{"no port", "", "5.6.7.8", "5.6.7.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			assert.Equal(t, tt.expected, clientIP(r))
		})
	}
}

func TestRateLimiterClose(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{RequestsPerMinute: 60, BurstSize: 5})
	// Close should not panic and should be idempotent.
	rl.Close()
	rl.Close()

	// After close, Allow should still work (buckets are intact, just cleanup stopped).
	assert.True(t, rl.Allow("post-close"))
}
