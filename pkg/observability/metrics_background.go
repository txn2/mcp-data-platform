package observability

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Background work instruments (#1897). Before these, a background loop that
// stopped -- a wedged worker, a scheduler on a replica whose worker was off, a
// LISTEN connection gone half-open -- produced no signal at all: the loops
// logged a Warn on failure and nothing on success, so silence read the same as
// health. Exposed names:
//
// Every loop, through internal/bgloop:
//
//   - background_loop_iterations_total{loop, result}         result: ok, error, skipped
//   - background_loop_duration_seconds{loop}
//   - background_loop_last_success_timestamp_seconds{loop}   unix seconds of the last ok
//   - retention_rows_purged_total{loop}                      rows a sweep deleted
//
// Queues, read on each scrape from the sampler the owning layer installs
// (RegisterBackgroundQueue); every replica reports the same database value,
// so they are read with max:
//
//   - background_queue_items{loop, state}                    state: pending, running, waiting
//   - background_queue_oldest_age_seconds{loop}              age of the oldest item due
//
// Script runs:
//
//   - script_run_failures_total{cause}                       the run's failure cause
//   - script_runs_shed_total                                 runs requeued by memory shedding
//   - script_worker_load_ratio{reason}                       reason: memory, cpu; last admission read
//
// Notifications:
//
//   - notification_delivery_attempts_total{kind, result}     result: delivered, retry, failed
//
// Connection OAuth refresher:
//
//   - connection_oauth_refresh_total{kind, result}           result: ok, failed, revoked
//   - connection_oauth_credentials{kind, state}              state: ok, expiring, revoked, missing
//
// Thumbnails:
//
//   - thumbnail_renderer_up                                  1 when the renderer answered the last check
//   - thumbnail_render_duration_seconds{kind}
//   - thumbnail_render_failures_total{kind, reason}         reason: document, renderer, storage, undrawable
//
// Audit writer:
//
//   - audit_writer_queue_depth                               events waiting in this replica's writer
//   - audit_write_duration_seconds{result}                   one store write
//
// LISTEN adapters:
//
//   - pg_listen_connected{loop}                              1 while the connection is up
//   - pg_listen_reconnects_total{loop}
//   - pg_listen_last_notification_age_seconds{loop}          since the last notification
//
// Cardinality: loop is the closed set of loop names internal/bgloop declares;
// cause, result, state and reason are closed sets; kind is the notification
// channel kinds, the connection kinds, or the thumbnail kinds.
const (
	instLoopIterations  = "background_loop_iterations"
	instLoopDuration    = "background_loop_duration"
	instLoopLastSuccess = "background_loop_last_success_timestamp"
	instRowsPurged      = "retention_rows_purged"
	instQueueItems      = "background_queue_items"
	instQueueOldestAge  = "background_queue_oldest_age"

	instScriptRunFailures = "script_run_failures"
	instScriptRunsShed    = "script_runs_shed"
	instScriptWorkerLoad  = "script_worker_load_ratio"

	instNotifyAttempts = "notification_delivery_attempts"

	instConnOAuthRefresh = "connection_oauth_refresh"
	instConnOAuthCreds   = "connection_oauth_credentials"

	instThumbRendererUp = "thumbnail_renderer_up"
	instThumbRenderDur  = "thumbnail_render_duration"
	instThumbRenderFail = "thumbnail_render_failures"

	instAuditQueueDepth = "audit_writer_queue_depth"
	instAuditWriteDur   = "audit_write_duration"

	instListenConnected  = "pg_listen_connected"
	instListenReconnects = "pg_listen_reconnects"
	instListenLastAge    = "pg_listen_last_notification_age"

	unitUnixSeconds        = "s"
	backgroundScrapeLimit  = 5 * time.Second
	backgroundSampleMaxAge = 10 * time.Second
)

// Label keys the background series add.
const (
	attrLoop  = "loop"
	attrCause = "cause"
)

// Loop iteration results, as RecordLoopIteration labels them.
const (
	// LoopResultOK is an iteration that did its work.
	LoopResultOK = "ok"
	// LoopResultError is an iteration that returned an error.
	LoopResultError = "error"
	// LoopResultSkipped is an iteration that found the work held elsewhere
	// (another replica's advisory lock) and did nothing.
	LoopResultSkipped = "skipped"
)

// Queue item states, as a BackgroundQueueSample reports them.
const (
	// QueueStatePending is an item due and not yet claimed.
	QueueStatePending = "pending"
	// QueueStateRunning is an item claimed and in progress.
	QueueStateRunning = "running"
	// QueueStateWaiting is an item not yet due (a retry backoff, an open
	// compaction window).
	QueueStateWaiting = "waiting"
)

// BackgroundQueueSample is one queue's state as its store holds it, read on a
// scrape. Kind subdivides a queue (a notification's kind, a thumbnail's
// family) and may be empty.
type BackgroundQueueSample struct {
	Kind      string
	Pending   int64
	Running   int64
	Waiting   int64
	OldestAge time.Duration
}

// BackgroundQueueSampler returns a queue's state. It is called on a scrape
// with a bounded context; the result is cached for backgroundSampleMaxAge.
type BackgroundQueueSampler func(ctx context.Context) ([]BackgroundQueueSample, error)

type queueEntry struct {
	sampler BackgroundQueueSampler
	// mu guards the cache, and is held while the sampler runs so two readers
	// of one queue query it once. It is the entry's own: a slow queue holds no
	// other queue, and nothing the LISTEN path takes.
	mu     sync.Mutex
	cached []BackgroundQueueSample
	at     time.Time
}

// backgroundInstruments are the background series and the state the scrape
// callbacks read.
type backgroundInstruments struct {
	iterations  metric.Int64Counter
	duration    metric.Float64Histogram
	lastSuccess metric.Float64Gauge
	purged      metric.Int64Counter

	queueItems metric.Int64ObservableGauge
	queueAge   metric.Float64ObservableGauge

	scriptFailures metric.Int64Counter
	scriptShed     metric.Int64Counter
	scriptLoad     metric.Float64Gauge

	notifyAttempts metric.Int64Counter

	connRefresh metric.Int64Counter
	connCreds   metric.Int64Gauge

	thumbUp   metric.Int64Gauge
	thumbDur  metric.Float64Histogram
	thumbFail metric.Int64Counter

	auditDepth    metric.Int64ObservableGauge
	auditWriteDur metric.Float64Histogram

	listenConnected  metric.Int64Gauge
	listenReconnects metric.Int64Counter
	listenAge        metric.Float64ObservableGauge

	mu           sync.Mutex
	queues       map[string]*queueEntry
	auditDepthFn func() int
	lastNotify   map[string]time.Time
}

// registerBackgroundInstruments registers the background series and their two
// scrape callbacks (queues and audit depth/LISTEN age).
func (m *Metrics) registerBackgroundInstruments(meter metric.Meter) error {
	bg := &m.bg
	bg.queues = map[string]*queueEntry{}
	bg.lastNotify = map[string]time.Time{}
	r := instrumentRegistrar{meter: meter}

	bg.iterations = r.counter(instLoopIterations,
		"Total background loop iterations, labeled by loop and result (ok, error, skipped; skipped is another replica holding the work's lock).")
	bg.duration = r.histogram(instLoopDuration,
		"Background loop iteration duration in seconds, labeled by loop.")
	bg.lastSuccess = r.floatGauge(instLoopLastSuccess, unitUnixSeconds,
		"Unix time of the loop's last successful iteration on this replica, labeled by loop. A value that stops advancing is a loop that stopped; read with max across replicas for a loop that runs on one.")
	bg.purged = r.counter(instRowsPurged,
		"Total rows deleted by retention sweeps, labeled by the sweep's loop name.")
	bg.queueItems = r.observableGauge(instQueueItems,
		"Items in a background queue, labeled by loop (the queue's consumer), kind and state (pending, running, waiting). Read with max.")
	bg.scriptFailures = r.counter(instScriptRunFailures,
		"Total managed-script runs that failed, labeled by cause (script, upstream, memory, worker_lost, platform, state_conflict).")
	bg.scriptShed = r.counter(instScriptRunsShed,
		"Total managed-script runs a replica stopped and requeued because its memory passed the shed threshold.")
	bg.scriptLoad = r.floatGauge(instScriptWorkerLoad, "",
		"The share (0-1) of its memory or CPU limit the run worker measured at its last admission read, labeled by reason (memory, cpu).")
	bg.notifyAttempts = r.counter(instNotifyAttempts,
		"Total notification delivery attempts, labeled by kind (the transport: email or a channel kind) and result (delivered, retry, failed).")
	bg.connRefresh = r.counter(instConnOAuthRefresh,
		"Total refresh attempts by the connection OAuth refresher, labeled by connection kind and result (ok, failed, revoked).")
	bg.connCreds = r.intGauge(instConnOAuthCreds,
		"Connections with an OAuth credential, as the refresher's last pass found them, labeled by kind and state (ok, expiring, revoked, missing).")
	bg.thumbUp = r.intGauge(instThumbRendererUp,
		"1 when the thumbnail renderer answered the worker's last check, 0 when it did not.")
	bg.thumbDur = r.histogram(instThumbRenderDur,
		"Time to draw one thumbnail tile in seconds, labeled by kind.")
	bg.thumbFail = r.counter(instThumbRenderFail,
		"Total thumbnail attempts that drew no tile, labeled by kind and reason (document, renderer, storage, undrawable).")
	bg.auditDepth = r.observableGauge(instAuditQueueDepth,
		"Audit events waiting in this replica's asynchronous writer.")
	bg.auditWriteDur = r.histogram(instAuditWriteDur,
		"Duration of one audit store write in seconds, labeled by result (ok, error, timeout).")
	bg.listenConnected = r.intGauge(instListenConnected,
		"1 while the LISTEN connection is up, labeled by loop.")
	bg.listenReconnects = r.counter(instListenReconnects,
		"Total LISTEN reconnections, labeled by loop. A notification sent while the connection was down is lost; the listener wakes its workers to re-read on reconnect.")
	if r.err != nil {
		return r.err
	}
	var err error
	if bg.queueAge, err = meter.Float64ObservableGauge(instQueueOldestAge,
		metric.WithDescription("Age in seconds of the oldest item due in a background queue, labeled by loop and kind; zero when none waits. Read with max."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instQueueOldestAge, err)
	}
	if bg.listenAge, err = meter.Float64ObservableGauge(instListenLastAge,
		metric.WithDescription("Seconds since the LISTEN connection last delivered a notification, labeled by loop."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instListenLastAge, err)
	}
	if _, err := meter.RegisterCallback(m.observeBackgroundQueues, bg.queueItems, bg.queueAge); err != nil {
		return wrapReg("background queue callback", err)
	}
	if _, err := meter.RegisterCallback(m.observeBackgroundState, bg.auditDepth, bg.listenAge); err != nil {
		return wrapReg("background state callback", err)
	}
	return nil
}

// instrumentRegistrar registers instruments until the first error, which it
// keeps, so a block of registrations reads as a list.
type instrumentRegistrar struct {
	meter metric.Meter
	err   error
}

func (r *instrumentRegistrar) counter(name, desc string) metric.Int64Counter {
	if r.err != nil {
		return nil
	}
	c, err := r.meter.Int64Counter(name, metric.WithDescription(desc))
	r.err = wrapReg(name, err)
	return c
}

func (r *instrumentRegistrar) histogram(name, desc string) metric.Float64Histogram {
	if r.err != nil {
		return nil
	}
	h, err := r.meter.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unitSeconds))
	r.err = wrapReg(name, err)
	return h
}

func (r *instrumentRegistrar) floatGauge(name, unit, desc string) metric.Float64Gauge {
	if r.err != nil {
		return nil
	}
	opts := []metric.Float64GaugeOption{metric.WithDescription(desc)}
	if unit != "" {
		opts = append(opts, metric.WithUnit(unit))
	}
	g, err := r.meter.Float64Gauge(name, opts...)
	r.err = wrapReg(name, err)
	return g
}

func (r *instrumentRegistrar) intGauge(name, desc string) metric.Int64Gauge {
	if r.err != nil {
		return nil
	}
	g, err := r.meter.Int64Gauge(name, metric.WithDescription(desc))
	r.err = wrapReg(name, err)
	return g
}

func (r *instrumentRegistrar) observableGauge(name, desc string) metric.Int64ObservableGauge {
	if r.err != nil {
		return nil
	}
	g, err := r.meter.Int64ObservableGauge(name, metric.WithDescription(desc))
	r.err = wrapReg(name, err)
	return g
}

// RecordLoopIteration records one background loop iteration: its result and
// duration, and, when it succeeded, the time it did. Nil-safe.
func (m *Metrics) RecordLoopIteration(ctx context.Context, loop, result string, d time.Duration) {
	if m == nil {
		return
	}
	l := attribute.String(attrLoop, loop)
	m.bg.iterations.Add(ctx, 1, metric.WithAttributes(l, attribute.String(attrResult, result)))
	m.bg.duration.Record(ctx, d.Seconds(), metric.WithAttributes(l))
	if result == LoopResultOK {
		m.bg.lastSuccess.Record(ctx, float64(time.Now().UnixNano())/float64(time.Second), metric.WithAttributes(l))
	}
}

// RecordRowsPurged records the rows one retention sweep deleted. Nil-safe and
// a no-op for zero.
func (m *Metrics) RecordRowsPurged(ctx context.Context, loop string, n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.bg.purged.Add(ctx, n, metric.WithAttributes(attribute.String(attrLoop, loop)))
}

// RegisterBackgroundQueue installs the sampler a queue's gauges are read from,
// under the loop name of the queue's consumer. Nil-safe; a later call for the
// same loop replaces the earlier sampler.
func (m *Metrics) RegisterBackgroundQueue(loop string, s BackgroundQueueSampler) {
	if m == nil || s == nil {
		return
	}
	m.bg.mu.Lock()
	defer m.bg.mu.Unlock()
	m.bg.queues[loop] = &queueEntry{sampler: s}
}

// observeBackgroundQueues reads every registered queue, each through a cache
// so a second reader (an OTLP push beside the Prometheus scrape) does not
// query the database twice. The queues are sampled in parallel and outside
// bg.mu, which the LISTEN adapters take on every notification: a slow database
// delays this scrape, never a notification. A failing sampler is logged and
// its queue skipped on this scrape.
func (m *Metrics) observeBackgroundQueues(ctx context.Context, o metric.Observer) error {
	m.bg.mu.Lock()
	loops := make([]string, 0, len(m.bg.queues))
	entries := make([]*queueEntry, 0, len(m.bg.queues))
	for loop, e := range m.bg.queues {
		loops = append(loops, loop)
		entries = append(entries, e)
	}
	m.bg.mu.Unlock()

	results := make([][]BackgroundQueueSample, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Go(func() { results[i] = sampleQueue(ctx, loops[i], e) })
	}
	wg.Wait()

	for i, samples := range results {
		for _, s := range samples {
			// A queue with one series carries no kind label at all.
			set := []attribute.KeyValue{attribute.String(attrLoop, loops[i])}
			if s.Kind != "" {
				set = append(set, attribute.String(attrKind, s.Kind))
			}
			for state, n := range map[string]int64{
				QueueStatePending: s.Pending, QueueStateRunning: s.Running, QueueStateWaiting: s.Waiting,
			} {
				o.ObserveInt64(m.bg.queueItems, n, metric.WithAttributes(append(set, attribute.String(attrState, state))...))
			}
			o.ObserveFloat64(m.bg.queueAge, max(s.OldestAge, 0).Seconds(), metric.WithAttributes(set...))
		}
	}
	return nil
}

// sampleQueue returns a queue's cached sample, or reads a fresh one when the
// cache is older than backgroundSampleMaxAge; nil when the read fails.
func sampleQueue(ctx context.Context, loop string, e *queueEntry) []BackgroundQueueSample {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cached != nil && time.Since(e.at) < backgroundSampleMaxAge {
		return e.cached
	}
	sctx, cancel := context.WithTimeout(ctx, backgroundScrapeLimit)
	defer cancel()
	samples, err := e.sampler(sctx)
	if err != nil {
		slog.WarnContext(ctx, "observability: background queue sample failed", "loop", loop, "error", RedactError(err))
		return nil
	}
	if samples == nil {
		samples = []BackgroundQueueSample{}
	}
	e.cached, e.at = samples, time.Now()
	return samples
}

// observeBackgroundState reports the in-process state: the audit writer's
// depth and each LISTEN connection's time since its last notification.
func (m *Metrics) observeBackgroundState(_ context.Context, o metric.Observer) error {
	m.bg.mu.Lock()
	defer m.bg.mu.Unlock()
	if m.bg.auditDepthFn != nil {
		o.ObserveInt64(m.bg.auditDepth, int64(m.bg.auditDepthFn()))
	}
	now := time.Now()
	for loop, at := range m.bg.lastNotify {
		o.ObserveFloat64(m.bg.listenAge, now.Sub(at).Seconds(),
			metric.WithAttributes(attribute.String(attrLoop, loop)))
	}
	return nil
}

// RegisterAuditQueueDepth installs the function the audit writer's depth is
// read from on a scrape. Nil-safe.
func (m *Metrics) RegisterAuditQueueDepth(fn func() int) {
	if m == nil {
		return
	}
	m.bg.mu.Lock()
	defer m.bg.mu.Unlock()
	m.bg.auditDepthFn = fn
}

// RecordAuditWrite records one audit store write. Nil-safe.
func (m *Metrics) RecordAuditWrite(ctx context.Context, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.bg.auditWriteDur.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(attrResult, result)))
}

// RecordScriptRunFailure counts a failed run by its cause. Nil-safe.
func (m *Metrics) RecordScriptRunFailure(ctx context.Context, cause string) {
	if m == nil {
		return
	}
	m.bg.scriptFailures.Add(ctx, 1, metric.WithAttributes(attribute.String(attrCause, cause)))
}

// RecordScriptRunShed counts a run stopped and requeued by memory shedding.
// Nil-safe.
func (m *Metrics) RecordScriptRunShed(ctx context.Context) {
	if m == nil {
		return
	}
	m.bg.scriptShed.Add(ctx, 1)
}

// RecordScriptWorkerLoad records the share of a resource limit the run
// worker's admission read. reason is memory or cpu. Nil-safe.
func (m *Metrics) RecordScriptWorkerLoad(ctx context.Context, reason string, ratio float64) {
	if m == nil {
		return
	}
	m.bg.scriptLoad.Record(ctx, ratio, metric.WithAttributes(attribute.String(attrReason, reason)))
}

// RecordNotificationAttempt records one delivery attempt by transport kind and
// result (delivered, retry, failed). Nil-safe.
func (m *Metrics) RecordNotificationAttempt(ctx context.Context, kind, result string) {
	if m == nil {
		return
	}
	m.bg.notifyAttempts.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrResult, result)))
}

// RecordConnectionOAuthRefresh records one refresher attempt. Nil-safe.
func (m *Metrics) RecordConnectionOAuthRefresh(ctx context.Context, kind, result string) {
	if m == nil {
		return
	}
	m.bg.connRefresh.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrResult, result)))
}

// RecordConnectionOAuthCredentials records how many connections of a kind are
// in a credential state. Nil-safe.
func (m *Metrics) RecordConnectionOAuthCredentials(ctx context.Context, kind, state string, n int) {
	if m == nil {
		return
	}
	m.bg.connCreds.Record(ctx, int64(n), metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrState, state)))
}

// RecordThumbnailRenderer records whether the renderer answered. Nil-safe.
func (m *Metrics) RecordThumbnailRenderer(ctx context.Context, up bool) {
	if m == nil {
		return
	}
	var v int64
	if up {
		v = 1
	}
	m.bg.thumbUp.Record(ctx, v)
}

// RecordThumbnailRender records one attempt to draw a document's tiles and how
// long it took. Nil-safe.
func (m *Metrics) RecordThumbnailRender(ctx context.Context, kind string, d time.Duration) {
	if m == nil {
		return
	}
	m.bg.thumbDur.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(attrKind, kind)))
}

// RecordThumbnailFailure counts a document whose tile was not drawn, by why:
// document (the page threw or did not finish), renderer (the renderer was
// unavailable), storage (the file could not be read or the tile written), or
// undrawable (its attempts ran out). Nil-safe.
func (m *Metrics) RecordThumbnailFailure(ctx context.Context, kind, reason string) {
	if m == nil {
		return
	}
	m.bg.thumbFail.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrReason, reason)))
}

// RecordListenConnected records a LISTEN connection coming up or going down.
// A connection that comes back up after being down counts a reconnect.
// Nil-safe.
func (m *Metrics) RecordListenConnected(ctx context.Context, loop string, up, reconnect bool) {
	if m == nil {
		return
	}
	l := metric.WithAttributes(attribute.String(attrLoop, loop))
	var v int64
	if up {
		v = 1
	}
	m.bg.listenConnected.Record(ctx, v, l)
	if reconnect {
		m.bg.listenReconnects.Add(ctx, 1, l)
	}
	if up {
		m.startListenClock(loop)
	}
}

// startListenClock starts a LISTEN connection's time-since-notification clock
// when it first connects. A reconnect does not restart it: a connection that
// drops and comes back without delivering anything keeps aging, which is the
// signal pg_listen_last_notification_age_seconds exists to give.
func (m *Metrics) startListenClock(loop string) {
	m.bg.mu.Lock()
	defer m.bg.mu.Unlock()
	if _, ok := m.bg.lastNotify[loop]; !ok {
		m.bg.lastNotify[loop] = time.Now()
	}
}

// RecordListenNotification marks a LISTEN connection delivering a
// notification, which resets its clock. Nil-safe.
func (m *Metrics) RecordListenNotification(loop string) {
	if m == nil {
		return
	}
	m.bg.mu.Lock()
	defer m.bg.mu.Unlock()
	m.bg.lastNotify[loop] = time.Now()
}
