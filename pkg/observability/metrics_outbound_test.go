package observability

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestOutboundInstruments_Exposed records one of every outbound observation
// and reads the scrape, so the names the documentation carries are the ones
// exported (#1895).
func TestOutboundInstruments_Exposed(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()

	m.RecordHTTPClientRequest(ctx, "api", "crm", StatusClass2xx, 40*time.Millisecond)
	m.RecordHTTPClientRequest(ctx, "oidc", "", StatusClassOther, 2*time.Second)
	m.RecordEgressBlocked(ctx, "private")
	m.RecordUpstreamRetry(ctx, "api", false)
	m.RecordUpstreamRetry(ctx, "script", true)
	m.RecordEmbeddingCall(ctx, "nomic-embed-text", StatusOK, 90*time.Millisecond)
	m.RecordEmbeddingCall(ctx, "nomic-embed-text", StatusUpstreamErr, time.Second)
	m.RecordEmbeddingFallback(ctx, "nomic-embed-text")
	m.RecordGatewayUpstreamCall(ctx, "mcp-test", GatewayOutcomeOK, 30*time.Millisecond)
	m.RecordGatewayUpstreamCall(ctx, "mcp-test", GatewayOutcomeTransportError, 5*time.Second)
	m.RecordGatewaySessionRedial(ctx, "mcp-test", RedialOK)

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`http_client_requests_total{connection="crm",kind="api",status_class="2xx"} 1`,
		`http_client_requests_total{connection="",kind="oidc",status_class="other"} 1`,
		`http_client_request_duration_seconds_count{connection="crm",kind="api",status_class="2xx"} 1`,
		`egress_blocked_total{reason="private"} 1`,
		`upstream_retries_total{kind="api"} 1`,
		`upstream_retries_exhausted_total{kind="script"} 1`,
		`embedding_calls_total{model="nomic-embed-text",status="ok"} 1`,
		`embedding_calls_total{model="nomic-embed-text",status="upstream_err"} 1`,
		`embedding_call_duration_seconds_count{model="nomic-embed-text"} 2`,
		`embedding_fallbacks_total{model="nomic-embed-text"} 1`,
		`gateway_upstream_calls_total{connection="mcp-test",outcome="ok"} 1`,
		`gateway_upstream_calls_total{connection="mcp-test",outcome="transport_error"} 1`,
		`gateway_upstream_call_duration_seconds_count{connection="mcp-test"} 2`,
		`gateway_session_redials_total{connection="mcp-test",result="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	if strings.Contains(body, `upstream_retries_total{kind="script"}`) {
		t.Error("an exhausted retry is not also a retry")
	}
}

func TestOutboundInstruments_NilSafe(_ *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.RecordHTTPClientRequest(ctx, "api", "crm", StatusClass2xx, time.Second)
	m.RecordEgressBlocked(ctx, "private")
	m.RecordUpstreamRetry(ctx, "api", true)
	m.RecordEmbeddingCall(ctx, "m", StatusOK, time.Second)
	m.RecordEmbeddingFallback(ctx, "m")
	m.RecordGatewayUpstreamCall(ctx, "c", GatewayOutcomeOK, time.Second)
	m.RecordGatewaySessionRedial(ctx, "c", RedialFailed)
}
