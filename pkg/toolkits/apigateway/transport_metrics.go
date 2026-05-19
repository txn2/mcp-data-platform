package apigateway

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// instrumentedTransport wraps an http.RoundTripper to record outbound
// metrics for the apigateway toolkit. It sits inside the per-connection
// http.Client so EVERY outbound call — api_invoke_endpoint AND
// api_export — is observed without touching the call sites.
//
// The connection name is bound at construction so the recorder does
// not need to read it from the request context (which the request
// doesn't carry). One transport instance per connection.
type instrumentedTransport struct {
	base       http.RoundTripper
	connection string
	metrics    *observability.Metrics
}

// newInstrumentedTransport wraps base with metrics recording. When
// metrics is nil (subsystem disabled), the function returns base
// unchanged so the toolkit pays zero overhead beyond a one-time
// construction-site nil check.
func newInstrumentedTransport(base http.RoundTripper, connection string, metrics *observability.Metrics) http.RoundTripper {
	if !metrics.Enabled() {
		return base
	}
	return &instrumentedTransport{
		base:       base,
		connection: connection,
		metrics:    metrics,
	}
}

// RoundTrip records the latency and outcome of a single outbound
// HTTP call. A nil response with a non-nil error indicates a
// transport-level failure (DNS, connection refused, TLS, timeout);
// that case is classified as upstream_err with http_status_class=other
// so it is countable without needing a synthetic status code.
//
// The underlying error is returned unchanged so the existing
// scrubTransportError path in invoke.go still strips credentials
// before the message reaches the model.
func (t *instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	duration := time.Since(start)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	t.metrics.RecordAPIGatewayOutbound(req.Context(), observability.APIGatewayAttrs{
		Connection:      t.connection,
		HTTPStatusClass: observability.HTTPStatusClass(status),
		StatusCategory:  observability.HTTPStatusCategory(status, err),
	}, duration)
	// Returned unchanged so the wrapping http.Client (and the
	// scrubTransportError path that strips credentials from
	// url.Error.URL) continues to see the original error type.
	// Wrapping here would convert *url.Error into a plain error
	// and break credential scrubbing.
	return resp, err //nolint:wrapcheck // see comment above
}
