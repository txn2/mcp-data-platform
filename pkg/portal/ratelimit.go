package portal

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// RateLimitConfig configures the public portal viewer's rate limiter. It is
// an alias for the shared ratelimit.Config so there is one config shape and
// one token-bucket implementation across the platform.
type RateLimitConfig = ratelimit.Config

// RateLimiter provides per-IP token-bucket rate limiting for the public
// portal viewer, delegating the bucket accounting to the shared
// pkg/ratelimit implementation while keeping the portal's existing
// leftmost-X-Forwarded-For client attribution (see clientIP). The portal's
// public endpoints serve cached asset blobs behind the same trust boundary
// as the rest of the portal, so its abuse-protection threat model differs
// from the OAuth endpoints, which use trusted-proxy-aware attribution.
type RateLimiter struct {
	lim *ratelimit.Limiter
}

// NewRateLimiter creates a rate limiter from config.
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	return &RateLimiter{lim: ratelimit.New(cfg)}
}

// Allow checks whether a request from the given IP should be allowed.
func (rl *RateLimiter) Allow(ip string) bool { return rl.lim.Allow(ip) }

// Close stops the background cleanup goroutine.
func (rl *RateLimiter) Close() { rl.lim.Close() }

// Middleware wraps an http.Handler with rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.lim.Allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the client IP, respecting X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	// Strip port from RemoteAddr.
	if host, _, ok := strings.Cut(r.RemoteAddr, ":"); ok {
		return host
	}
	return r.RemoteAddr
}
