package proxy

import (
	"sync"

	"golang.org/x/time/rate"
)

// rateLimiter applies a per-key token-bucket limit. The key is the
// caller's resolved persona name, so a misbehaving UI for one persona
// cannot starve queries for another. Persona names are a small bounded
// set (one limiter per persona), so the map does not grow unbounded and
// needs no eviction.
type rateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	burst    int
}

func newRateLimiter(perSecond int) *rateLimiter {
	return &rateLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        rate.Limit(perSecond),
		burst:    perSecond,
	}
}

// allow reports whether a query for the given key may proceed now.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	lim, ok := rl.limiters[key]
	if !ok {
		lim = rate.NewLimiter(rl.r, rl.burst)
		rl.limiters[key] = lim
	}
	rl.mu.Unlock()
	return lim.Allow()
}
