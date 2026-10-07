package observability

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
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

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`http_server_request_duration_seconds_count{method="GET",route="GET /api/v1/resources",status_class="2xx"} 1`,
		`http_server_request_duration_seconds_count{method="unknown",route="/",status_class="4xx"} 1`,
		`http_rate_limited_total{limiter="oauth_token"} 1`,
		`mcp_requests_total{method="tools/list",status="ok"} 1`,
		`mcp_requests_total{method="resources/read",status="client_err"} 1`,
		`mcp_request_duration_seconds_count{method="tools/list",status="ok"} 1`,
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
