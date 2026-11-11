package observability

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// TestHTTPInstruments_Exposed records one of every inbound observation and
// reads the scrape, so the names the documentation carries are the ones
// exported (#1889).
func TestHTTPInstruments_Exposed(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()

	m.RecordHTTPServerRequest(ctx, "GET /api/v1/resources", http.MethodGet, StatusClass2xx, 120*time.Millisecond)
	m.RecordHTTPServerRequest(ctx, "/", "BREW", StatusClass4xx, time.Millisecond)
	m.RecordHTTPRateLimited(ctx, "oauth_token")
	m.RecordMCPRequest(ctx, "tools/list", StatusOK, 3*time.Millisecond)
	m.RecordMCPRequest(ctx, "resources/read", StatusClientErr, 2*time.Millisecond)
	m.RecordEndpointRequest(ctx, "order-lookup", StatusClass5xx, 40*time.Millisecond)
	m.RecordSiteRequest(ctx, "site", "docs", "prod", StatusClass2xx, 5*time.Millisecond)
	m.RecordSiteRequest(ctx, "pages", "shop", "", StatusClass4xx, time.Millisecond)
	m.RecordSiteCacheRead(ctx, "memory")

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`http_server_request_duration_seconds_count{method="GET",route="GET /api/v1/resources",status_class="2xx"} 1`,
		`http_server_request_duration_seconds_count{method="unknown",route="/",status_class="4xx"} 1`,
		`http_rate_limited_total{limiter="oauth_token"} 1`,
		`mcp_requests_total{method="tools/list",status="ok"} 1`,
		`mcp_requests_total{method="resources/read",status="client_err"} 1`,
		`mcp_request_duration_seconds_count{method="tools/list",status="ok"} 1`,
		`endpoint_requests_total{endpoint="order-lookup",status_class="5xx"} 1`,
		`endpoint_request_duration_seconds_count{endpoint="order-lookup"} 1`,
		`site_requests_total{environment="prod",kind="site",site="docs",status_class="2xx"} 1`,
		`site_requests_total{environment="",kind="pages",site="shop",status_class="4xx"} 1`,
		`site_request_duration_seconds_count{environment="prod",kind="site",site="docs"} 1`,
		`site_cache_reads_total{outcome="memory"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

func TestHTTPInstruments_NilSafe(_ *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.RecordHTTPServerRequest(ctx, "/", http.MethodGet, StatusClass2xx, time.Second)
	m.RecordHTTPRateLimited(ctx, "pdf_export")
	m.RecordMCPRequest(ctx, "ping", StatusOK, time.Millisecond)
	m.RecordEndpointRequest(ctx, "x", StatusClass2xx, time.Millisecond)
	m.RecordSiteRequest(ctx, "site", "x", "y", StatusClass2xx, time.Millisecond)
	m.RecordSiteCacheRead(ctx, "disk")
}

func TestHTTPMethodLabel(t *testing.T) {
	for _, m := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
	} {
		if got := HTTPMethodLabel(m); got != m {
			t.Errorf("HTTPMethodLabel(%q) = %q", m, got)
		}
	}
	for _, m := range []string{"", "get", "PROPFIND", "X" + strings.Repeat("Y", 500)} {
		if got := HTTPMethodLabel(m); got != MetricLabelUnknown {
			t.Errorf("HTTPMethodLabel(%q) = %q, want unknown", m, got)
		}
	}
}

// failingMeter refuses to register the instrument named fail.
type failingMeter struct {
	noop.Meter
	fail string
}

func (f failingMeter) Int64Counter(name string, opts ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if name == f.fail {
		return nil, errors.New("refused")
	}
	return f.Meter.Int64Counter(name, opts...) //nolint:wrapcheck // a test fake passing the noop meter through
}

func (f failingMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if name == f.fail {
		return nil, errors.New("refused")
	}
	return f.Meter.Float64Histogram(name, opts...) //nolint:wrapcheck // a test fake passing the noop meter through
}

// A site instrument the meter refuses fails the registration, naming it.
func TestRegisterSiteInstruments_ARefusalNamesTheInstrument(t *testing.T) {
	for _, name := range []string{instSiteRequests, instSiteDuration, instSiteCacheReads} {
		m := &Metrics{}
		err := m.registerSiteInstruments(failingMeter{fail: name})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("refusing %s: %v", name, err)
		}
	}
	if err := (&Metrics{}).registerSiteInstruments(failingMeter{}); err != nil {
		t.Errorf("a meter that refuses nothing: %v", err)
	}
}
