// Package target describes the platform under test — its endpoints and auth —
// and builds the authenticated HTTP client the harness reuses for MCP,
// portal REST, OAuth, and metrics traffic.
package target

import (
	"net/http"
	"strings"
	"time"
)

// AuthMode selects how the harness authenticates.
type AuthMode string

const (
	// AuthAPIKey sends the credential as a Bearer token (the platform accepts
	// an admin API key this way).
	AuthAPIKey AuthMode = "apikey"
	// AuthOAuthToken sends a pre-issued OAuth access token as a Bearer token.
	AuthOAuthToken AuthMode = "oauth"
	// AuthNone sends no Authorization header (for anonymous / DCR paths).
	AuthNone AuthMode = "none"
)

// Target is the platform under test.
type Target struct {
	// BaseURL is the MCP + REST base, e.g. http://localhost:8099. The MCP
	// streamable endpoint is BaseURL itself (the SDK client posts there).
	BaseURL string
	// MetricsURL is the Prometheus endpoint, e.g. http://localhost:9091/metrics.
	MetricsURL string
	// PprofURL is the base of the debug pprof listener, e.g.
	// http://localhost:6060. Empty disables profile capture.
	PprofURL string
	// Auth mode and credential.
	Auth       AuthMode
	Credential string
}

// authRoundTripper injects the Authorization header on every request.
type authRoundTripper struct {
	header string // full header value, e.g. "Bearer abc"; empty = no header
	base   http.RoundTripper
}

func (a authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.header != "" {
		r.Header.Set("Authorization", a.header)
	}
	return a.base.RoundTrip(r)
}

// HTTPClient builds an *http.Client that authenticates per the target's mode.
// The transport is tuned for high concurrency so connection setup does not
// dominate the measured latency: a generous idle pool keyed per-host.
func (t Target) HTTPClient(timeout time.Duration) *http.Client {
	base := &http.Transport{
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 512,
		MaxConnsPerHost:     0, // unlimited; concurrency is bounded by the runner
		IdleConnTimeout:     90 * time.Second,
	}
	var header string
	switch t.Auth {
	case AuthAPIKey, AuthOAuthToken:
		if t.Credential != "" {
			header = "Bearer " + t.Credential
		}
	case AuthNone:
		header = ""
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: authRoundTripper{header: header, base: base},
	}
}

// AnonymousHTTPClient builds a client that sends no Authorization header,
// reusing the same tuned transport. Used for the DCR (/register) and public
// portal-viewer paths that must be exercised unauthenticated.
func (t Target) AnonymousHTTPClient(timeout time.Duration) *http.Client {
	return Target{Auth: AuthNone}.HTTPClient(timeout)
}

// URL joins the base URL and a path, tolerating a trailing slash on the base.
func (t Target) URL(path string) string {
	return strings.TrimRight(t.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
}
