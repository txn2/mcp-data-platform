package observability

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Webhook telemetry instruments (#2150): the series the webhooks admin page
// charts, so an external Prometheus sees what the page sees. Exposed names:
//
//   - webhook_responses_total{source, http_status}   the status each request was answered with; 0 is a sender that left first
//   - webhook_written_events_total{source}           events written to object storage (drained from the spool, or from the buffer)
//   - webhook_written_bytes_total{source}            bytes of the segments written
//   - webhook_ack_stage_seconds{source, stage}       each acknowledged delivery's buffer, mark and put wait
//   - webhook_compaction_duration_seconds{source}    one window's compaction
//   - webhook_compaction_lag_seconds{source}         window end to compaction finished
//   - webhook_window_held_bytes{source}              a window's events as the compactor holds them in memory
//   - webhook_source_bytes{source, kind}             raw segments not yet released and compacted files kept
const (
	instWebhookResponses   = "webhook_responses"
	instWebhookWrittenEvts = "webhook_written_events"
	instWebhookWrittenB    = "webhook_written_bytes"
	instWebhookAckStage    = "webhook_ack_stage"
	instWebhookCompactDur  = "webhook_compaction_duration"
	instWebhookCompactLag  = "webhook_compaction_lag"
	instWebhookWindowHeld  = "webhook_window_held_bytes"
	instWebhookSourceBytes = "webhook_source_bytes"

	attrStage         = "stage"
	attrWebhookStatus = "http_status"

	// webhookStorageScrapeLimit bounds the per-source storage read a scrape
	// makes.
	webhookStorageScrapeLimit = 5 * time.Second
)

// WebhookStorageSample is one source's objects.
type WebhookStorageSample struct {
	Source         string
	RawBytes       int64
	CompactedBytes int64
}

// WebhookStorageSampler reads every source's objects on a scrape.
type WebhookStorageSampler func(ctx context.Context) ([]WebhookStorageSample, error)

// webhookTelemetryInstruments are the #2150 series.
type webhookTelemetryInstruments struct {
	responses     metric.Int64Counter
	writtenEvents metric.Int64Counter
	writtenBytes  metric.Int64Counter
	ackStage      metric.Float64Histogram
	compactDur    metric.Float64Histogram
	compactLag    metric.Float64Histogram
	windowHeld    metric.Int64Histogram
	sourceBytes   metric.Int64ObservableGauge

	mu      sync.RWMutex
	sampler WebhookStorageSampler
}

// registerWebhookTelemetry registers the #2150 series.
func (m *Metrics) registerWebhookTelemetry(meter metric.Meter) error {
	wt := &m.webhookTelemetry
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
	seconds := func(name, desc string) metric.Float64Histogram {
		if err != nil {
			return nil
		}
		var h metric.Float64Histogram
		h, err = meter.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unitSeconds))
		err = wrapReg(name, err)
		return h
	}
	wt.responses = counter(instWebhookResponses,
		"Total requests to /hooks/{source} by the HTTP status they were answered with, labeled by source and http_status; 0 is a sender that went away before its answer. An answer the ingress gave without reaching the platform (a 502 during a pod swap) is not counted here.")
	wt.writtenEvents = counter(instWebhookWrittenEvts,
		"Total webhook events this replica wrote to object storage, drained from its spool or written from its buffer, labeled by source. Against webhook_events_total it says whether a burst is being absorbed.")
	wt.writtenBytes = counter(instWebhookWrittenB,
		"Total bytes of raw segments this replica wrote to object storage, labeled by source.")
	wt.ackStage = seconds(instWebhookAckStage,
		"Seconds each acknowledged delivery spent in one stage of its write, labeled by source and stage (buffer, mark, put; with a spool, put is the spool's sync).")
	wt.compactDur = seconds(instWebhookCompactDur,
		"Seconds one compaction of a window took, labeled by source.")
	wt.compactLag = seconds(instWebhookCompactLag,
		"Seconds from a window's end to its compaction finishing, labeled by source. Includes the compactor's grace.")
	if err != nil {
		return err
	}
	if wt.windowHeld, err = meter.Int64Histogram(instWebhookWindowHeld,
		metric.WithDescription("Bytes a window's events took in memory while it was compacted, labeled by source. The compactor holds a whole window at once; set it against the container's memory limit."),
		metric.WithUnit("By")); err != nil {
		return wrapReg(instWebhookWindowHeld, err)
	}
	if wt.sourceBytes, err = meter.Int64ObservableGauge(instWebhookSourceBytes,
		metric.WithDescription("Bytes a source holds in object storage, labeled by source and kind (raw: segments not yet released; compacted: Parquet files of windows not expired). Every replica reports the same value: read with max."),
		metric.WithUnit("By")); err != nil {
		return wrapReg(instWebhookSourceBytes, err)
	}
	if _, err := meter.RegisterCallback(m.observeWebhookStorage, wt.sourceBytes); err != nil {
		return wrapReg(instWebhookSourceBytes, err)
	}
	return nil
}

// RegisterWebhookStorage installs the sampler webhook_source_bytes reads.
// Nil-safe; a later call replaces the earlier sampler.
func (m *Metrics) RegisterWebhookStorage(s WebhookStorageSampler) {
	if m == nil {
		return
	}
	m.webhookTelemetry.mu.Lock()
	defer m.webhookTelemetry.mu.Unlock()
	m.webhookTelemetry.sampler = s
}

// observeWebhookStorage is the scrape callback for webhook_source_bytes. A
// failed read is logged and the scrape carries no such series.
func (m *Metrics) observeWebhookStorage(ctx context.Context, o metric.Observer) error {
	wt := &m.webhookTelemetry
	wt.mu.RLock()
	sampler := wt.sampler
	wt.mu.RUnlock()
	if sampler == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, webhookStorageScrapeLimit)
	defer cancel()
	samples, err := sampler(ctx)
	if err != nil {
		slog.WarnContext(ctx, "observability: webhook storage sample failed", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	for _, s := range samples {
		src := attribute.String(attrWebhookSource, s.Source)
		o.ObserveInt64(wt.sourceBytes, s.RawBytes, metric.WithAttributes(src, attribute.String(attrKind, "raw")))
		o.ObserveInt64(wt.sourceBytes, s.CompactedBytes, metric.WithAttributes(src, attribute.String(attrKind, "compacted")))
	}
	return nil
}

// WebhookResponse counts one request by the status it was answered with.
// Nil-safe.
func (m *Metrics) WebhookResponse(ctx context.Context, source string, status int) {
	if m == nil || m.webhookTelemetry.responses == nil {
		return
	}
	m.webhookTelemetry.responses.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrWebhookSource, source), attribute.String(attrWebhookStatus, strconv.Itoa(status))))
}

// WebhookWritten counts one segment's events and bytes written to object
// storage. Nil-safe.
func (m *Metrics) WebhookWritten(ctx context.Context, source string, events, bytes int) {
	if m == nil || m.webhookTelemetry.writtenEvents == nil {
		return
	}
	m.webhookTelemetry.writtenEvents.Add(ctx, int64(events), sourceAttr(source))
	m.webhookTelemetry.writtenBytes.Add(ctx, int64(bytes), sourceAttr(source))
}

// WebhookAckStages records an acknowledged delivery's wait in each stage.
// Nil-safe.
func (m *Metrics) WebhookAckStages(ctx context.Context, source string, buffer, mark, put time.Duration) {
	if m == nil || m.webhookTelemetry.ackStage == nil {
		return
	}
	for stage, d := range map[string]time.Duration{"buffer": buffer, "mark": mark, "put": put} {
		m.webhookTelemetry.ackStage.Record(ctx, d.Seconds(), metric.WithAttributes(
			attribute.String(attrWebhookSource, source), attribute.String(attrStage, stage)))
	}
}

// WebhookCompactionRun records one compaction's duration, its lag behind the
// window's end, and the bytes the window took in memory. Nil-safe.
func (m *Metrics) WebhookCompactionRun(ctx context.Context, source string, duration, lag time.Duration, heldBytes int64) {
	if m == nil || m.webhookTelemetry.compactDur == nil {
		return
	}
	m.webhookTelemetry.compactDur.Record(ctx, duration.Seconds(), sourceAttr(source))
	m.webhookTelemetry.compactLag.Record(ctx, lag.Seconds(), sourceAttr(source))
	m.webhookTelemetry.windowHeld.Record(ctx, heldBytes, sourceAttr(source))
}
