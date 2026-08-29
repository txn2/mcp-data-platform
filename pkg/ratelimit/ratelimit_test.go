package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLimiterAllow(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 5})
	defer l.Close()

	// Burst should allow 5 requests.
	for i := range 5 {
		assert.True(t, l.Allow("1.2.3.4"), "request %d should be allowed", i)
	}
	// 6th should be denied.
	assert.False(t, l.Allow("1.2.3.4"))
}

func TestLimiterDifferentKeys(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 2})
	defer l.Close()

	assert.True(t, l.Allow("1.1.1.1"))
	assert.True(t, l.Allow("1.1.1.1"))
	assert.False(t, l.Allow("1.1.1.1"))

	// Different key should have its own bucket.
	assert.True(t, l.Allow("2.2.2.2"))
}

func TestLimiterDefaults(t *testing.T) {
	l := New(Config{})
	defer l.Close()
	// Should not panic, defaults applied.
	assert.True(t, l.Allow("test"))
	assert.InDelta(t, float64(defaultRPM)/secondsPerMinute, l.Rate(), 1e-9)
}

func TestLimiterCleanup(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 5})
	defer l.Close()

	l.Allow("old")
	l.mu.Lock()
	l.buckets["old"].lastSeen = time.Now().Add(-2 * time.Hour)
	l.mu.Unlock()

	l.Allow("new")
	l.Cleanup(1 * time.Hour)

	l.mu.Lock()
	_, hasOld := l.buckets["old"]
	_, hasNew := l.buckets["new"]
	l.mu.Unlock()

	assert.False(t, hasOld, "old entry should be cleaned up")
	assert.True(t, hasNew, "new entry should remain")
}

func TestLimiterTokenRefill(t *testing.T) {
	l := New(Config{RequestsPerMinute: 6000, BurstSize: 1})
	defer l.Close()

	// Exhaust the bucket.
	assert.True(t, l.Allow("refill"))
	assert.False(t, l.Allow("refill"))

	// Simulate time passing by adjusting lastSeen.
	l.mu.Lock()
	l.buckets["refill"].lastSeen = time.Now().Add(-1 * time.Second)
	l.mu.Unlock()

	// After enough time, tokens should refill.
	assert.True(t, l.Allow("refill"))
}

func TestLimiterClose(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 5})
	// Close should not panic and should be idempotent.
	l.Close()
	l.Close()

	// After close, Allow should still work (buckets are intact, just cleanup stopped).
	assert.True(t, l.Allow("post-close"))
}

func TestLimiterCleanupLoopTickerFires(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 5})
	defer l.Close()

	l.Allow("stale-key")
	l.mu.Lock()
	l.buckets["stale-key"].lastSeen = time.Now().Add(-1 * time.Hour)
	l.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	// Run cleanup loop with very short interval so the ticker fires.
	go l.runCleanupLoop(ctx, 10*time.Millisecond, 30*time.Minute)

	// Wait long enough for at least one tick.
	time.Sleep(50 * time.Millisecond)
	cancel()

	l.mu.Lock()
	_, hasStale := l.buckets["stale-key"]
	l.mu.Unlock()
	assert.False(t, hasStale, "stale entry should be cleaned up by loop")
}

// TestLimiterWaitAdmitsWithoutDelayWhileTokensRemain: Wait consumes a token
// exactly as Allow does when one is available, and returns at once.
func TestLimiterWaitAdmitsWithoutDelayWhileTokensRemain(t *testing.T) {
	l := New(Config{RequestsPerMinute: 60, BurstSize: 2})
	defer l.Close()

	start := time.Now()
	assert.NoError(t, l.Wait(context.Background(), "k"))
	assert.NoError(t, l.Wait(context.Background(), "k"))
	assert.Less(t, time.Since(start), 500*time.Millisecond)
	assert.False(t, l.Allow("k"), "Wait consumed the burst")
}

// TestLimiterWaitHoldsUntilARefill: an empty bucket admits the waiter once
// the sustained rate has refilled one token, 1/Rate seconds later, and the
// wait is bounded by that interval rather than open-ended.
func TestLimiterWaitHoldsUntilARefill(t *testing.T) {
	// 600 rpm = 10 tokens/second = one token every 100ms.
	l := New(Config{RequestsPerMinute: 600, BurstSize: 1})
	defer l.Close()
	assert.True(t, l.Allow("k"))

	start := time.Now()
	assert.NoError(t, l.Wait(context.Background(), "k"))
	waited := time.Since(start)
	assert.GreaterOrEqual(t, waited, 90*time.Millisecond, "admitted before the token refilled")
	assert.Less(t, waited, 2*time.Second, "waited far past one refill interval")
}

// TestLimiterWaitReturnsWhenTheContextEnds: a canceled context ends the wait
// with its error and leaves the bucket untouched, so no token is consumed for a
// call that will not run.
func TestLimiterWaitReturnsWhenTheContextEnds(t *testing.T) {
	// 6 rpm = one token every 10 seconds: the wait cannot end by refill.
	l := New(Config{RequestsPerMinute: 6, BurstSize: 1})
	defer l.Close()
	assert.True(t, l.Allow("k"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := l.Wait(ctx, "k")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}
