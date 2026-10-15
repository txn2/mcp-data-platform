package observability

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestWebhookInstruments_Exposed records one of every webhook observation and
// reads the scrape, so the names the documentation and the source's page name
// are the ones exported (#1870).
func TestWebhookInstruments_Exposed(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()

	m.WebhookRequest(ctx, "esp", "accepted")
	m.WebhookRequest(ctx, "", "unknown_source")
	m.WebhookEvents(ctx, "esp", 500)
	m.WebhookSegmentWritten(ctx, "esp")
	m.WebhookAck(ctx, "esp", 800*time.Millisecond)
	m.WebhookBuffer(ctx, "esp", 42)
	m.WebhookCompaction(ctx, "esp", "compacted")
	m.WebhookDuplicatesDropped(ctx, "esp", 7)
	m.WebhookSpool(ctx, 4096, 12, 3, 90*time.Second)
	m.WebhookSpoolDrained(ctx, "esp", 12)
	m.WebhookSpoolDrainFailed(ctx, "esp")

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`webhook_requests_total{outcome="accepted",source="esp"} 1`,
		`webhook_requests_total{outcome="unknown_source",source=""} 1`,
		`webhook_events_total{source="esp"} 500`,
		`webhook_segments_written_total{source="esp"} 1`,
		`webhook_buffer_events{source="esp"} 42`,
		`webhook_compactions_total{result="compacted",source="esp"} 1`,
		`webhook_duplicates_dropped_total{source="esp"} 7`,
		`webhook_ack_seconds_count{source="esp"} 1`,
		`webhook_spool_bytes 4096`,
		`webhook_spool_events 12`,
		`webhook_spool_files 3`,
		`webhook_spool_oldest_age_seconds 90`,
		`webhook_spool_drained_events_total{source="esp"} 12`,
		`webhook_spool_drain_failures_total{source="esp"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

func TestWebhookInstruments_NilSafe(_ *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.WebhookRequest(ctx, "a", "b")
	m.WebhookEvents(ctx, "a", 1)
	m.WebhookSegmentWritten(ctx, "a")
	m.WebhookAck(ctx, "a", time.Second)
	m.WebhookBuffer(ctx, "a", 1)
	m.WebhookCompaction(ctx, "a", "b")
	m.WebhookDuplicatesDropped(ctx, "a", 1)
	m.WebhookSpool(ctx, 1, 1, 1, time.Second)
	m.WebhookSpoolDrained(ctx, "a", 1)
	m.WebhookSpoolDrainFailed(ctx, "a")
	empty := &Metrics{}
	empty.WebhookRequest(ctx, "a", "b")
	empty.WebhookEvents(ctx, "a", 1)
	empty.WebhookSegmentWritten(ctx, "a")
	empty.WebhookAck(ctx, "a", time.Second)
	empty.WebhookBuffer(ctx, "a", 1)
	empty.WebhookCompaction(ctx, "a", "b")
	empty.WebhookDuplicatesDropped(ctx, "a", 1)
	empty.WebhookSpool(ctx, 1, 1, 1, time.Second)
	empty.WebhookSpoolDrained(ctx, "a", 1)
	empty.WebhookSpoolDrainFailed(ctx, "a")
}
