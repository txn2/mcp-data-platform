package user

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// DefaultObserveTTL is the minimum interval between directory writes for the
// same email. Authentication runs on every tool call, so without throttling we
// would upsert the same row constantly.
const DefaultObserveTTL = 5 * time.Minute

// observeTimeout bounds the background directory write so a slow database can
// never leak goroutines.
const observeTimeout = 5 * time.Second

// maxSeenEntries triggers a prune of expired throttle entries once the map
// grows past it, so the throttle map stays bounded by the active set rather
// than every email that has ever authenticated.
const maxSeenEntries = 4096

// Directory wraps a Store with in-memory throttling and asynchronous,
// best-effort writes. Recording who has authenticated must never block or fail
// the authentication path, so Observe returns immediately and swallows errors.
type Directory struct {
	store Store
	ttl   time.Duration

	mu   sync.Mutex
	seen map[string]time.Time
}

// NewDirectory wraps store with the default throttle window.
func NewDirectory(store Store) *Directory {
	return &Directory{
		store: store,
		ttl:   DefaultObserveTTL,
		seen:  make(map[string]time.Time),
	}
}

// Observe records a person seen via authentication. It throttles repeat writes
// for the same email within the TTL and performs the store write on a
// background goroutine. Errors are logged, never returned: directory upkeep
// must not affect auth.
//
// The throttle is checked against a cheaply-normalized key BEFORE the full
// RFC 5322 parse, so the ~99% of calls that are throttled never pay for
// parsing. Names are sanitized (control characters stripped, length bounded)
// because they come from untrusted token claims.
func (d *Directory) Observe(email, firstName, lastName string) {
	if d == nil || d.store == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(email))
	if key == "" {
		return
	}
	if !d.shouldWrite(key) {
		return
	}
	// Full validation only once we have decided to write. An invalid address
	// stays throttled for the TTL, so a synthetic/garbage email is parsed at
	// most once per window rather than on every authentication.
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return
	}
	first := SanitizeName(firstName)
	last := SanitizeName(lastName)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), observeTimeout)
		defer cancel()
		if err := d.store.Observe(ctx, normalized, first, last); err != nil {
			// Best-effort: log and let the TTL drive the next attempt. We
			// deliberately keep the throttle entry so a database outage cannot
			// turn every subsequent authentication into an immediate retry
			// storm against the already-struggling database.
			slog.Warn("user directory observe failed", "email", normalized, "error", err)
		}
	}()
}

// shouldWrite returns true if key has not been observed within the TTL,
// recording the attempt time when it returns true. It prunes expired entries
// when the map grows large so it stays bounded by the active set.
func (d *Directory) shouldWrite(key string) bool {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if last, ok := d.seen[key]; ok && now.Sub(last) < d.ttl {
		return false
	}
	if len(d.seen) >= maxSeenEntries {
		d.pruneExpired(now)
	}
	d.seen[key] = now
	return true
}

// pruneExpired drops throttle entries older than the TTL (a past-TTL entry no
// longer throttles anything, so removing it is free). Callers must hold d.mu.
func (d *Directory) pruneExpired(now time.Time) {
	for k, t := range d.seen {
		if now.Sub(t) >= d.ttl {
			delete(d.seen, k)
		}
	}
}
