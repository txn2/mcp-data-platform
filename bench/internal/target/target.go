// Package target describes the platform under test — its endpoint and auth —
// and builds the authenticated HTTP client the harness reuses for MCP and
// admin REST traffic. Adapted from test/load/internal/target.
package target

import (
	"net/http"
	"time"
)

// Target is the platform under test.
type Target struct {
	// BaseURL is the MCP + REST base, e.g. http://localhost:8098. The MCP
	// streamable endpoint is BaseURL itself (the SDK client posts there).
	BaseURL string
	// Credential is the admin API key, sent as a Bearer token.
	Credential string
}

// authRoundTripper injects the Authorization header on every request.
type authRoundTripper struct {
	header string
	base   http.RoundTripper
}

func (a authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.header != "" {
		r.Header.Set("Authorization", a.header)
	}
	return a.base.RoundTrip(r)
}

// HTTPClient builds an authenticated *http.Client. Benchmark sessions run one
// at a time, so the transport is a plain default rather than the load
// harness's high-concurrency pool.
func (t Target) HTTPClient(timeout time.Duration) *http.Client {
	var header string
	if t.Credential != "" {
		header = "Bearer " + t.Credential
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: authRoundTripper{header: header, base: http.DefaultTransport},
	}
}
