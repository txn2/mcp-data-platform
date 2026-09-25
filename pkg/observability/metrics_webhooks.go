package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Inbound webhook instruments (#1870). Exposed names:
//
//   - webhook_requests_total{source, outcome}   accepted, unauthorized, too_large, rate_limited,
//     buffer_full, write_failed, unknown_source, invalid_body
//   - webhook_events_total{source}              events acknowledged
//   - webhook_segments_written_total{source}    raw segments written to object storage
//   - webhook_compactions_total{source, result} compacted, failed
//   - webhook_duplicates_dropped_total{source}  event ids removed at compaction
//   - webhook_buffer_events{source}             events held in memory on this replica
//   - webhook_ack_seconds{source}               request to acknowledgement
//
// Cardinality: source is the set of administrator-created sources. A request
// to a name that is not a source is counted under an empty source, so a
// caller cannot mint series by inventing names.
const (
	instWebhookRequests   = "webhook_requests"
	instWebhookEvents     = "webhook_events"
	instWebhookSegments   = "webhook_segments_written"
	instWebhookCompaction = "webhook_compactions"
	instWebhookDuplicates = "webhook_duplicates_dropped"
	instWebhookBuffer     = "webhook_buffer_events"
	instWebhookAck        = "webhook_ack"

	attrWebhookSource = "source"
)

// webhookInstruments are the webhook series.
type webhookInstruments struct {
	requests   metric.Int64Counter
	events     metric.Int64Counter
	segments   metric.Int64Counter
	compaction metric.Int64Counter
	duplicates metric.Int64Counter
	buffer     metric.Int64Gauge
	ack        metric.Float64Histogram
}

// registerWebhookInstruments registers the webhook series.
func (m *Metrics) registerWebhookInstruments(meter metric.Meter) error {
	wh := &m.webhook
	var err error
	counter := func(name, desc string) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var c metric.Int64Counter
		c, err = meter.Int64Counter(name, metric.WithDescription(desc))
		err = wrapReg(name, err)
		return c
	}
	wh.requests = counter(instWebhookRequests,
		"Total requests to /hooks/{source}, labeled by source and outcome. Every outcome but accepted stored nothing.")
	wh.events = counter(instWebhookEvents,
		"Total webhook events acknowledged, labeled by source. A request whose body splits into many events counts each.")
	wh.segments = counter(instWebhookSegments,
		"Total raw segments written to object storage, labeled by source.")
	wh.compaction = counter(instWebhookCompaction,
		"Total compactions of a source's window, labeled by source and result (compacted, failed).")
	wh.duplicates = counter(instWebhookDuplicates,
		"Total events removed at compaction because an earlier event carried the same event id, labeled by source. Senders deliver at least once, so a steady rate is expected.")
	if err != nil {
		return err
	}
	if wh.buffer, err = meter.Int64Gauge(instWebhookBuffer,
		metric.WithDescription("Events a source holds in memory on this replica: waiting for their segment, or in a write under way. Never above the source's buffer_limit.")); err != nil {
		return wrapReg(instWebhookBuffer, err)
	}
	if wh.ack, err = meter.Float64Histogram(instWebhookAck,
		metric.WithDescription("Seconds from receiving a webhook request to acknowledging it, labeled by source. The acknowledgement waits for the segment to be written."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instWebhookAck, err)
	}
	return nil
}

func sourceAttr(source string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String(attrWebhookSource, source))
}

// WebhookRequest counts one request with its outcome. Nil-safe.
func (m *Metrics) WebhookRequest(ctx context.Context, source, outcome string) {
	if m == nil || m.webhook.requests == nil {
		return
	}
	m.webhook.requests.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrWebhookSource, source), attribute.String(attrOutcome, outcome)))
}

// WebhookEvents counts acknowledged events. Nil-safe.
func (m *Metrics) WebhookEvents(ctx context.Context, source string, n int) {
	if m == nil || m.webhook.events == nil {
		return
	}
	m.webhook.events.Add(ctx, int64(n), sourceAttr(source))
}

// WebhookSegmentWritten counts one raw segment. Nil-safe.
func (m *Metrics) WebhookSegmentWritten(ctx context.Context, source string) {
	if m == nil || m.webhook.segments == nil {
		return
	}
	m.webhook.segments.Add(ctx, 1, sourceAttr(source))
}

// WebhookAck records how long a request took to acknowledge. Nil-safe.
func (m *Metrics) WebhookAck(ctx context.Context, source string, d time.Duration) {
	if m == nil || m.webhook.ack == nil {
		return
	}
	m.webhook.ack.Record(ctx, d.Seconds(), sourceAttr(source))
}

// WebhookBuffer reports the events a source holds in memory. Nil-safe.
func (m *Metrics) WebhookBuffer(ctx context.Context, source string, events int) {
	if m == nil || m.webhook.buffer == nil {
		return
	}
	m.webhook.buffer.Record(ctx, int64(events), sourceAttr(source))
}

// WebhookCompaction counts one compaction with its result. Nil-safe.
func (m *Metrics) WebhookCompaction(ctx context.Context, source, result string) {
	if m == nil || m.webhook.compaction == nil {
		return
	}
	m.webhook.compaction.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrWebhookSource, source), attribute.String(attrResult, result)))
}

// WebhookDuplicatesDropped counts events removed at compaction. Nil-safe.
func (m *Metrics) WebhookDuplicatesDropped(ctx context.Context, source string, n int64) {
	if m == nil || m.webhook.duplicates == nil {
		return
	}
	m.webhook.duplicates.Add(ctx, n, sourceAttr(source))
}
