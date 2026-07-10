package oauth

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
)

// RateLimitConfig configures rate limiting for the unauthenticated OAuth HTTP
// endpoints. /token performs a bcrypt compare per attempt and /register
// performs a bcrypt hash plus a DB insert per request, so both are CPU (and,
// for /register, storage) amplification levers. Limiting is on by default
// (Enabled nil == enabled); the per-endpoint knobs default to the *Default*
// constants below.
//
// Attribution is trusted-proxy-aware (TrustedProxies) rather than naive
// leftmost-X-Forwarded-For, and each endpoint additionally has a global
// backstop bucket (see ratelimit.HTTPLimiter) so total throughput — and
// therefore worst-case bcrypt work — is bounded even when per-IP attribution
// is defeated or collapses behind a proxy.
type RateLimitConfig struct {
	// Enabled turns limiting on/off; nil means enabled (platform default-on).
	Enabled *bool

	// TrustedProxies is the set of CIDRs whose X-Forwarded-For headers are
	// trusted for client attribution. Empty means trust none: the direct peer
	// address is used and forwarding headers are ignored.
	TrustedProxies []string

	// TokenRPM / TokenBurst limit /token per client IP.
	TokenRPM   int
	TokenBurst int

	// RegisterRPM / RegisterBurst limit /register per client IP.
	RegisterRPM   int
	RegisterBurst int
}

// OAuth endpoint rate-limit defaults.
const (
	defaultTokenRPM      = 60
	defaultTokenBurst    = 10
	defaultRegisterRPM   = 10
	defaultRegisterBurst = 3

	// errSlowDown is the RFC-style error code returned on a 429 (mirrors the
	// device-flow slow_down semantics: retry after backing off).
	errSlowDown = "slow_down"
)

// isEnabled reports whether OAuth rate limiting is on, defaulting to true.
func (c RateLimitConfig) isEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// configureRateLimiting builds the server's per-endpoint limiters from cfg. It
// is a no-op when limiting is disabled. A malformed trusted-proxy CIDR is a
// configuration error and is returned. The two endpoints share one resolver
// (same trusted-proxy set) but have independent buckets.
func (s *Server) configureRateLimiting(cfg RateLimitConfig) error {
	if !cfg.isEnabled() {
		return nil
	}
	resolver, err := ratelimit.NewResolver(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("configuring oauth rate limiter: %w", err)
	}
	s.rlEnabled = true
	s.tokenRL = ratelimit.NewHTTPLimiter(
		orDefault(cfg.TokenRPM, defaultTokenRPM),
		orDefault(cfg.TokenBurst, defaultTokenBurst),
		resolver,
	)
	s.registerRL = ratelimit.NewHTTPLimiter(
		orDefault(cfg.RegisterRPM, defaultRegisterRPM),
		orDefault(cfg.RegisterBurst, defaultRegisterBurst),
		resolver,
	)
	return nil
}

// allowRequest reports whether the request may proceed against the given
// endpoint limiter. It is a no-op (always allows) when limiting is disabled.
func (s *Server) allowRequest(el *ratelimit.HTTPLimiter, r *http.Request) bool {
	if !s.rlEnabled {
		return true
	}
	return el.Allow(r)
}

// writeRateLimited writes a 429 with a Retry-After header and an RFC-style
// JSON error body.
func (s *Server) writeRateLimited(w http.ResponseWriter, el *ratelimit.HTTPLimiter) {
	w.Header().Set("Retry-After", strconv.Itoa(el.RetryAfter()))
	s.writeError(w, http.StatusTooManyRequests, errSlowDown, "rate limit exceeded")
}

// orDefault returns v when positive, otherwise def.
func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
