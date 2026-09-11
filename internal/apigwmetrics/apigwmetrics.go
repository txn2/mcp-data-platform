// Package apigwmetrics records an HTTP connection kind's outbound
// observations. The api gateway records at the http.RoundTripper level, so
// every call it makes -- api_invoke_endpoint, api_export, the REST shim, a page
// walk -- is measured without touching a call site. The graphql kind records
// from its one send path instead, through Record, because its upstream reports
// failure inside a 200 and the verdict is only known once the body is read
// (#1678).
//
// It lives outside pkg/toolkits/apigateway because it holds no gateway types:
// it takes a connection name, an observability recorder, and the request's own
// context, and it is the toolkit's outbound instrumentation rather than part of
// its behavior. Extracted when the toolkit reached its package-size budget.
package apigwmetrics

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// transport wraps an http.RoundTripper to record outbound metrics. It sits
// inside the per-connection http.Client, and the connection name is bound at
// construction because the request does not carry it.
type transport struct {
	base       http.RoundTripper
	connection string
	metrics    *observability.Metrics
}

// New wraps base with metrics recording. When metrics is nil (subsystem
// disabled), it returns base unchanged so the toolkit pays zero overhead beyond
// a one-time construction-site nil check.
func New(base http.RoundTripper, connection string, metrics *observability.Metrics) http.RoundTripper {
	if !metrics.Enabled() {
		return base
	}
	return &transport{base: base, connection: connection, metrics: metrics}
}

// Instrument wraps client.Transport with the metrics-recording transport when
// metrics is enabled. No-op otherwise, so a test helper that builds a bare
// client needs no change.
//
// Idempotent: a client already wrapped for the same (connection, recorder) pair
// is left alone. That prevents double-wrapping -- and therefore
// double-recording -- when the toolkit's SetMetrics runs against connections
// that were already instrumented at construction time.
func Instrument(client *http.Client, connection string, metrics *observability.Metrics) {
	if client == nil {
		return
	}
	if existing, ok := client.Transport.(*transport); ok {
		if existing.connection == connection && existing.metrics == metrics {
			return
		}
	}
	client.Transport = New(client.Transport, connection, metrics)
}

// Wraps reports whether client's transport is this package's recorder for the
// given connection, and returns the transport it wrapped. The toolkit's tests
// assert on the wrapping itself -- that a connection is instrumented, and that
// a repeated SetMetrics did not nest a second recorder inside the first.
func Wraps(client *http.Client, connection string) (base http.RoundTripper, ok bool) {
	if client == nil {
		return nil, false
	}
	t, isRecorder := client.Transport.(*transport)
	if !isRecorder || t.connection != connection {
		return nil, false
	}
	return t.base, true
}

// RoundTrip records the latency and outcome of a single outbound HTTP call. A
// nil response with a non-nil error indicates a transport-level failure (DNS,
// connection refused, TLS, timeout); that case is classified as upstream_err
// with http_status_class=other so it is countable without needing a synthetic
// status code.
//
// The persona label comes off the request context, which descends from the tool
// call the gateway is serving -- both api_invoke_endpoint and api_export, and
// the REST shim, which re-enters through the in-memory MCP session and so
// carries the same stamp. A call the gateway makes outside a tool call, such as
// a catalog refresh, carries no persona and records as unknown (#1615).
//
// The underlying error is returned unchanged so the toolkit's
// scrubTransportError path still strips credentials before the message reaches
// the model; wrapping here would convert *url.Error into a plain error and
// break that scrubbing.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	duration := time.Since(start)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	Record(req.Context(), t.metrics, t.connection, Observation{
		Status:   status,
		Failed:   observability.HTTPStatusCategory(status, err) != observability.StatusOK,
		Duration: duration,
	})
	return resp, err //nolint:wrapcheck // see comment above
}

// Observation is one outbound call as the recorder sees it. Status is the
// status line (0 for a transport failure) and supplies the http_status_class
// label; Failed decides status_category, because the two are not the same
// fact for every kind: the transport above derives Failed from the status
// line alone, and the graphql kind passes what the body said, so a 200
// carrying an errors array is counted as upstream_err under the 2xx class
// (#1678).
type Observation struct {
	Status   int
	Failed   bool
	Duration time.Duration
}

// Record writes one outbound observation. The persona label comes off ctx,
// which descends from the tool call being served; a call made outside one
// records as unknown (#1615). A nil recorder records nothing.
func Record(ctx context.Context, metrics *observability.Metrics, connection string, o Observation) {
	if !metrics.Enabled() {
		return
	}
	category := observability.StatusOK
	if o.Failed {
		category = observability.StatusUpstreamErr
	}
	metrics.RecordAPIGatewayOutbound(ctx, observability.APIGatewayAttrs{
		Connection:      connection,
		HTTPStatusClass: observability.HTTPStatusClass(o.Status),
		StatusCategory:  category,
		Persona:         mcpcontext.GetPersona(ctx),
	}, o.Duration)
}
