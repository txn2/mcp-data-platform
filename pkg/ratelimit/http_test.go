package ratelimit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPLimiterRetryAfter(t *testing.T) {
	resolver, err := NewResolver(nil)
	require.NoError(t, err)

	// 60 rpm => 1 token/sec => Retry-After 1s.
	h := NewHTTPLimiter(60, 10, resolver)
	defer h.Close()
	assert.Equal(t, 1, h.RetryAfter())

	// 10 rpm => 1 token/6s => Retry-After 6s.
	h2 := NewHTTPLimiter(10, 3, resolver)
	defer h2.Close()
	assert.Equal(t, 6, h2.RetryAfter())
}

func TestHTTPLimiterPerIP(t *testing.T) {
	resolver, err := NewResolver(nil)
	require.NoError(t, err)
	h := NewHTTPLimiter(60, 2, resolver)
	defer h.Close()

	// Burst of 2 from one peer, then denied.
	assert.True(t, h.Allow(newReq("1.2.3.4:5000", "")))
	assert.True(t, h.Allow(newReq("1.2.3.4:5000", "")))
	assert.False(t, h.Allow(newReq("1.2.3.4:5000", "")))

	// A distinct peer has its own bucket.
	assert.True(t, h.Allow(newReq("9.9.9.9:5000", "")))
}

// TestHTTPLimiterGlobalBackstop proves the global bucket bounds total
// throughput even when every request comes from a distinct (spoofable) IP:
// per-IP burst is 1 so each fresh IP clears per-IP, but the global bucket
// (burst = 1 * globalBackstopFactor) is exhausted after globalBackstopFactor
// distinct IPs.
func TestHTTPLimiterGlobalBackstop(t *testing.T) {
	resolver, err := NewResolver(nil)
	require.NoError(t, err)
	h := NewHTTPLimiter(60, 1, resolver)
	defer h.Close()

	for i := range globalBackstopFactor {
		assert.True(t, h.Allow(newReq(ipForIndex(i), "")), "distinct IP %d within global budget", i)
	}
	// One more distinct IP: per-IP would allow it, but the global backstop is
	// exhausted.
	assert.False(t, h.Allow(newReq(ipForIndex(globalBackstopFactor), "")),
		"global backstop must reject past the total budget")
}

// TestHTTPLimiterGlobalRejectDoesNotDrainPerIP proves the admission ordering:
// a request rejected by the global backstop must not consume (or create) the
// caller's per-IP bucket, so a legitimate low-volume client is not additionally
// penalized by a global-saturation event.
func TestHTTPLimiterGlobalRejectDoesNotDrainPerIP(t *testing.T) {
	resolver, err := NewResolver(nil)
	require.NoError(t, err)
	h := NewHTTPLimiter(60, 1, resolver) // per-IP burst 1; global burst 10
	defer h.Close()

	// Exhaust the global bucket with distinct IPs, each within per-IP burst 1.
	for i := range globalBackstopFactor {
		require.True(t, h.Allow(newReq(ipForIndex(i), "")))
	}

	// A fresh IP is now rejected by the global backstop.
	const fresh = "203.0.113.99:5000"
	require.False(t, h.Allow(newReq(fresh, "")))

	// Its per-IP bucket must be untouched: peek-first means no token was
	// consumed and no bucket created for the globally-rejected request.
	h.perIP.mu.Lock()
	_, exists := h.perIP.buckets[hostOnly(fresh)]
	h.perIP.mu.Unlock()
	assert.False(t, exists, "globally-rejected request must not consume a per-IP token")
}

func TestLimiterAvailableDoesNotConsume(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 1})
	defer l.Close()

	// Peeking repeatedly never consumes a token.
	assert.True(t, l.available("k"))
	assert.True(t, l.available("k"))
	// The single token is still there for a real Allow.
	assert.True(t, l.Allow("k"))
	assert.False(t, l.Allow("k"))
	// Once exhausted, available reflects it.
	assert.False(t, l.available("k"))
}

func ipForIndex(i int) string {
	return fmt.Sprintf("10.%d.%d.1:5000", i/256, i%256)
}
