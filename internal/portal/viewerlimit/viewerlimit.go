// Package viewerlimit rate-limits the portal's public share viewer. It wraps
// the shared ratelimit.HTTPLimiter with the viewer's defaults so the public
// surface gets the same spoof-resistant, trusted-proxy-aware client
// attribution and global-saturation guard as the OAuth endpoints (#904).
package viewerlimit

import (
	"context"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// Config configures the public portal viewer's rate limiter. It is an alias
// for the shared ratelimit.Config so there is one config shape and one
// token-bucket implementation across the platform. Client-IP attribution is
// supplied separately as a ratelimit.Resolver (see New) rather than baked
// into this config, mirroring how the OAuth endpoints compose the two.
type Config = ratelimit.Config

// Public portal viewer per-IP rate-limit defaults, applied when the config
// leaves a field non-positive (the common no-`portal.rate_limit`-block case:
// applyPortalDefaults does not seed these). They must be applied BEFORE
// building the HTTPLimiter: ratelimit.NewHTTPLimiter sizes the global backstop
// as rate*globalBackstopFactor, so passing a zero rate would default the
// backstop from zero to the per-IP default (60/10) instead of the intended
// 10x (600/100), silently collapsing the aggregate cap. Mirrors the OAuth
// path's orDefault step (pkg/oauth/ratelimit.go).
const (
	defaultPortalRPM   = 60
	defaultPortalBurst = 10
)

// RateLimiter provides two-layer rate limiting for the public portal viewer:
// a per-client-IP bucket for fairness and a global backstop that bounds total
// throughput regardless of client attribution. It delegates both to the
// shared ratelimit.HTTPLimiter. The public viewer serves cached asset blobs
// to unauthenticated callers, so an attacker who can mint unlimited per-IP
// buckets by rotating a spoofed X-Forwarded-For would otherwise defeat the
// per-IP limit outright; the resolver closes that hole and the global bucket
// bounds the overflow.
type RateLimiter struct {
	lim *ratelimit.HTTPLimiter
}

// New creates a rate limiter from config and a client-IP resolver. A nil
// resolver yields the safe trust-none default: the direct peer address is
// used and X-Forwarded-For is ignored (matching ratelimit.NewResolver(nil),
// which never errors on nil input). Callers that trust a proxy topology build
// the resolver from a trusted-proxy CIDR list and pass it in.
func New(cfg Config, resolver *ratelimit.Resolver) *RateLimiter {
	if resolver == nil {
		resolver, _ = ratelimit.NewResolver(nil)
	}
	// Apply per-IP defaults here (not inside the limiter) so the global backstop
	// is sized off the resolved rate rather than zero. See the const block above.
	cfg = WithDefaults(cfg)
	return &RateLimiter{
		lim: ratelimit.NewHTTPLimiter(cfg.RequestsPerMinute, cfg.BurstSize, resolver),
	}
}

// WithDefaults is cfg with the viewer's per-IP default put in each field the
// deployment left unset: the budget New builds a limiter from. A caller that
// derives its own budget from the viewer's starts from this rather than from
// the configured fields, so a deployment with no portal.rate_limit block gets
// the same derivation as one that sets the defaults by hand (#1791).
func WithDefaults(cfg Config) Config {
	if cfg.RequestsPerMinute <= 0 {
		cfg.RequestsPerMinute = defaultPortalRPM
	}
	if cfg.BurstSize <= 0 {
		cfg.BurstSize = defaultPortalBurst
	}
	return cfg
}

// Allow reports whether the request should be admitted, consuming a token from
// both the per-IP and global buckets on admission. Client-IP attribution is
// delegated to the resolver, so a spoofed X-Forwarded-For no longer mints a
// fresh per-IP bucket.
func (rl *RateLimiter) Allow(r *http.Request) bool { return rl.lim.Allow(r) }

// Close stops the background cleanup goroutines.
func (rl *RateLimiter) Close() { rl.lim.Close() }

// inProcessKey marks a request the platform makes of its own routes.
type inProcessKey struct{}

// InProcess marks ctx as carrying a request the platform makes of its own
// routes, which the limiter admits without counting (#1791).
//
// The limiter exists to bound what clients can make the platform do, and a
// request the platform issues to itself is not a client's: the tile worker
// draws a document's declared references by calling the reference route
// in-process, one render at a time, and its pace is its own. Counted, every
// such request presents the one loopback address, so every reference of every
// tile the worker draws comes out of a single bucket, and a document drawn
// after that bucket runs dry is recorded as not drawable. A context value
// cannot arrive over the network, so only code in this process can set it.
func InProcess(ctx context.Context) context.Context {
	return context.WithValue(ctx, inProcessKey{}, true)
}

func isInProcess(ctx context.Context) bool {
	v, _ := ctx.Value(inProcessKey{}).(bool)
	return v
}

// Middleware wraps an http.Handler with rate limiting. A request marked
// InProcess is passed through without being counted.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isInProcess(r.Context()) && !rl.Allow(r) {
			w.Header().Set("Retry-After", strconv.Itoa(rl.lim.RetryAfter()))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
