package observability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestWebhookTelemetry_Exposed records one of every #2150 observation and
// reads the scrape, so the names the documentation gives are the ones
// exported.
func TestWebhookTelemetry_Exposed(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()
	m.WebhookResponse(ctx, "esp", 202)
	m.WebhookResponse(ctx, "esp", 0)
	m.WebhookWritten(ctx, "esp", 12, 4096)
	m.WebhookAckStages(ctx, "esp", time.Millisecond, 2*time.Millisecond, 30*time.Millisecond)
	m.WebhookCompactionRun(ctx, "esp", 900*time.Millisecond, 140*time.Second, 64000)
	m.RegisterWebhookStorage(func(context.Context) ([]WebhookStorageSample, error) {
		return []WebhookStorageSample{{Source: "esp", RawBytes: 9000, CompactedBytes: 5000}}, nil
	})

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`webhook_responses_total{http_status="202",source="esp"} 1`,
		`webhook_responses_total{http_status="0",source="esp"} 1`,
		`webhook_written_events_total{source="esp"} 12`,
		`webhook_written_bytes_total{source="esp"} 4096`,
		`webhook_ack_stage_seconds_count{source="esp",stage="put"} 1`,
		`webhook_compaction_duration_seconds_count{source="esp"} 1`,
		`webhook_compaction_lag_seconds_count{source="esp"} 1`,
		`webhook_window_held_bytes_count{source="esp"} 1`,
		`webhook_source_bytes{kind="raw",source="esp"} 9000`,
		`webhook_source_bytes{kind="compacted",source="esp"} 5000`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}

	m.RegisterWebhookStorage(func(context.Context) ([]WebhookStorageSample, error) { return nil, errors.New("down") })
	if body := scrapeMetrics(t, m.Handler()); strings.Contains(body, "webhook_source_bytes{") {
		t.Error("a failed storage read still reported series")
	}
}

func TestWebhookTelemetry_NilSafe(_ *testing.T) {
	ctx := context.Background()
	for _, m := range []*Metrics{nil, {}} {
		m.WebhookResponse(ctx, "a", 202)
		m.WebhookWritten(ctx, "a", 1, 1)
		m.WebhookAckStages(ctx, "a", 0, 0, 0)
		m.WebhookCompactionRun(ctx, "a", 0, 0, 0)
		m.RegisterWebhookStorage(nil)
	}
}
