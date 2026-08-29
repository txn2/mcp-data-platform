// Package ratelimit provides a per-key token-bucket rate limiter shared
// across the platform's HTTP surfaces (the public portal viewer and the
// OAuth authorization server) and the per-user tool-call limiter. Keeping one
// implementation avoids per-caller forks of the bucket math, cleanup
// goroutine, and eviction policy.
//
// The limiter is keyed on an arbitrary string. Callers choose the key: a
// client IP for per-client fairness, or a fixed sentinel for a global
// backstop that bounds total throughput regardless of client attribution.
// Client-IP extraction (including trusted-proxy handling) lives in Resolver
// (clientip.go); this file is only the bucket accounting.
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Config configures a token-bucket limiter.
type Config struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
	BurstSize         int `yaml:"burst_size"`
}

// Limiter provides per-key token-bucket rate limiting.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   int
	stop    context.CancelFunc
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

const (
	// defaultRPM is the default requests per minute.
	defaultRPM = 60
	// defaultBurst is the default burst size.
	defaultBurst = 10
	// secondsPerMinute converts RPM to per-second rate.
	secondsPerMinute = 60.0
	// cleanupInterval is how often the background goroutine runs.
	cleanupInterval = 10 * time.Minute
	// cleanupMaxAge is the max idle time before a bucket is evicted.
	cleanupMaxAge = 30 * time.Minute
)

// New creates a rate limiter from config, applying defaults for
// non-positive values, and starts the background eviction goroutine.
// Call Close to stop it.
func New(cfg Config) *Limiter {
	return newLimiter(cfg, true)
}

// newSingleBucket creates a limiter for a fixed, small set of keys (typically
// one) with no background eviction goroutine: a bucket that is checked
// continuously never goes idle long enough to evict, so the janitor would only
// ever wake to do nothing. Used for the global backstop bucket.
func newSingleBucket(cfg Config) *Limiter {
	return newLimiter(cfg, false)
}

// newLimiter builds a Limiter, starting the eviction goroutine only when
// runJanitor is set.
func newLimiter(cfg Config, runJanitor bool) *Limiter {
	rpm := cfg.RequestsPerMinute
	if rpm <= 0 {
		rpm = defaultRPM
	}
	burst := cfg.BurstSize
	if burst <= 0 {
		burst = defaultBurst
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &Limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(rpm) / secondsPerMinute,
		burst:   burst,
		stop:    cancel,
	}
	if runJanitor {
		go l.cleanupLoop(ctx)
	}
	return l
}

// Rate returns the refill rate in tokens per second. It backs Retry-After
// computation: the time for a single token to refill is 1/Rate seconds.
func (l *Limiter) Rate() float64 { return l.rate }

// Allow checks whether a request under the given key should be allowed,
// consuming one token when it is.
func (l *Limiter) Allow(key string) bool {
	ok, _ := l.take(key)
	return ok
}

// Wait admits a request under the given key as soon as a token is available,
// consuming it, or returns ctx's error if ctx ends first. It is the queueing
// counterpart to Allow, for a caller that is delayed to the moment it is within
// the rate rather than refused. One waiter on an empty bucket is admitted after
// at most 1/Rate seconds; when several wait on one key, each refilled token
// admits whichever wakes first and the others wait for the next.
func (l *Limiter) Wait(ctx context.Context, key string) error {
	for {
		ok, until := l.take(key)
		if ok {
			return nil
		}
		timer := time.NewTimer(until)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("waiting for a rate-limit token: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// take consumes one token under key when one is available. When none is, it
// reports how long the bucket needs to refill to a whole token.
func (l *Limiter) take(key string) (ok bool, until time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: float64(l.burst), lastSeen: now}
		l.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// available reports whether a token is currently available for key WITHOUT
// consuming one. It lets callers compose multiple limiters so a request
// rejected by a later limiter does not consume a token from an earlier one
// (see HTTPLimiter). It never mutates bucket state.
func (l *Limiter) available(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		// A key with no bucket yet starts full.
		return l.burst >= 1
	}
	tokens := b.tokens + time.Since(b.lastSeen).Seconds()*l.rate
	if tokens > float64(l.burst) {
		tokens = float64(l.burst)
	}
	return tokens >= 1
}

// Cleanup removes stale entries older than the given duration.
func (l *Limiter) Cleanup(maxAge time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// cleanupLoop periodically evicts stale rate-limit buckets.
func (l *Limiter) cleanupLoop(ctx context.Context) {
	l.runCleanupLoop(ctx, cleanupInterval, cleanupMaxAge)
}

// runCleanupLoop is the testable core of cleanupLoop.
func (l *Limiter) runCleanupLoop(ctx context.Context, interval, maxAge time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Cleanup(maxAge)
			slog.Debug("rate limiter cleanup completed")
		}
	}
}

// Close stops the background cleanup goroutine. It is idempotent.
func (l *Limiter) Close() {
	if l.stop != nil {
		l.stop()
	}
}
