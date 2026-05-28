// Package proxy implements an authenticated PromQL query proxy. The
// portal's observability views query Prometheus through this proxy so
// the browser never talks to Prometheus directly: it reuses the
// platform's auth and persona model, keeps Prometheus on the internal
// network, and audits every query. The proxy forwards the read-only
// /api/v1/query and /api/v1/query_range endpoints and returns the
// upstream response body unchanged.
package proxy

import (
	"fmt"
	"net/url"
	"time"
)

// defaultTimeout is the upstream request timeout when none is configured.
const defaultTimeout = 30 * time.Second

// defaultRatePerSecond is the per-persona query rate when none is
// configured. Matches the issue's documented default.
const defaultRatePerSecond = 10

// Config configures the PromQL proxy. An empty URL leaves the proxy
// unconfigured: every endpoint returns 503 so the portal renders a
// clean empty state instead of erroring.
type Config struct {
	URL                string
	Timeout            time.Duration
	BasicAuthUser      string
	BasicAuthPass      string
	RateLimitPerSecond int
}

// parseBase parses and validates the configured Prometheus URL. The
// caller guarantees a non-empty URL (the empty/unconfigured case is
// handled in New). A valid URL must be absolute http(s) with a host;
// anything else is a configuration error surfaced at construction time
// so a bad value fails fast rather than per request.
func (c Config) parseBase() (*url.URL, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, fmt.Errorf("observability proxy: invalid prometheus url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("observability proxy: prometheus url must be an absolute http(s) URL, got %q", c.URL)
	}
	return u, nil
}

func (c Config) timeout() time.Duration {
	if c.Timeout <= 0 {
		return defaultTimeout
	}
	return c.Timeout
}

func (c Config) ratePerSecond() int {
	if c.RateLimitPerSecond <= 0 {
		return defaultRatePerSecond
	}
	return c.RateLimitPerSecond
}
