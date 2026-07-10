package ratelimit

import (
	"math"
	"net/http"
)

const (
	// globalBackstopFactor multiplies the per-IP rate/burst to size the global
	// backstop bucket. The per-IP layer provides fairness; the global layer is
	// the topology-proof guarantee that total throughput stays bounded even
	// when per-IP attribution is defeated (spoofed forwarding header) or
	// collapses (a proxy that does not forward the client IP).
	globalBackstopFactor = 10

	// globalKey is the fixed bucket key for the single global backstop bucket.
	globalKey = "global"
)

// HTTPLimiter rate-limits requests to a single HTTP endpoint with two layers:
// a per-client-IP bucket for fairness and a global bucket that bounds total
// throughput regardless of how requests attribute to IPs. Client-IP
// attribution (including trusted-proxy handling) is delegated to a Resolver,
// so the per-IP layer is not trivially defeated by a spoofed X-Forwarded-For.
type HTTPLimiter struct {
	perIP      *Limiter
	global     *Limiter
	resolver   *Resolver
	retryAfter int
}

// NewHTTPLimiter builds an HTTPLimiter for one endpoint from a per-IP
// rate/burst and a client-IP resolver. The global backstop bucket is sized at
// globalBackstopFactor times the per-IP rate and burst. Call Close to stop the
// background cleanup goroutines.
func NewHTTPLimiter(rpm, burst int, resolver *Resolver) *HTTPLimiter {
	perIP := New(Config{RequestsPerMinute: rpm, BurstSize: burst})
	global := newSingleBucket(Config{
		RequestsPerMinute: rpm * globalBackstopFactor,
		BurstSize:         burst * globalBackstopFactor,
	})
	// Retry-After: seconds for one per-IP token to refill, at least 1.
	retryAfter := 1
	if r := perIP.Rate(); r > 0 {
		if ra := int(math.Ceil(1.0 / r)); ra > retryAfter {
			retryAfter = ra
		}
	}
	return &HTTPLimiter{perIP: perIP, global: global, resolver: resolver, retryAfter: retryAfter}
}

// Allow reports whether the request may proceed. A request is admitted only
// when it is within BOTH the client's own per-IP budget and the global budget,
// and a token is consumed from each bucket only on admission:
//
//  1. Peek per-IP (no consume). A client over its own limit is rejected here
//     without ever touching the global bucket, so an over-limit client cannot
//     drain the shared backstop against everyone else.
//  2. Consume global. If the system is saturated the request is rejected here
//     with the per-IP bucket left untouched, so a legitimate low-volume client
//     is not additionally penalized by a global-saturation event.
//  3. Consume per-IP to record the admitted request.
func (h *HTTPLimiter) Allow(r *http.Request) bool {
	ip := h.resolver.ClientIP(r)
	if !h.perIP.available(ip) {
		return false
	}
	if !h.global.Allow(globalKey) {
		return false
	}
	return h.perIP.Allow(ip)
}

// RetryAfter returns the advisory Retry-After value in seconds: the time for
// one per-IP token to refill, at least 1.
func (h *HTTPLimiter) RetryAfter() int { return h.retryAfter }

// Close stops both buckets' background cleanup goroutines.
func (h *HTTPLimiter) Close() {
	h.perIP.Close()
	h.global.Close()
}
