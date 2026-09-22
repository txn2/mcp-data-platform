package observability

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Background embedding queue instruments (#1837). The job table records what
// happened to each unit, but a dashboard reading a page of it goes blank the
// moment a backlog is larger than the page: the newest rows are all pending,
// so no completion, no running pass and no retry is in view. These series
// count every job as it happens, across replicas and restarts. Exposed names:
//
//   - indexjob_enqueued_total{kind, trigger, result}      result: created, folded
//   - indexjob_jobs_total{kind, trigger, outcome}          one per claimed job
//   - indexjob_duration_seconds{kind, outcome}             claim to settling write
//   - indexjob_running{kind}                               on this replica
//   - indexjob_items_total{kind, result}                   result: embedded, reused
//   - indexjob_embed_calls_total{kind, status}             status: ok, timeout, error
//   - indexjob_embed_texts_total{kind}                     texts sent to the provider
//   - indexjob_embed_call_duration_seconds{kind}           one EmbedBatch call
//   - indexjob_leases_released_total                       reaper reclaims
//   - indexjob_units_deferred_total{kind}                  parked units a sweep skipped
//
// And, read from the database on each scrape (IndexQueueSampler):
//
//   - indexjob_queue_jobs{kind, state}                     state: pending, running, retrying
//   - indexjob_failed_units{kind}                          units with an open failure
//   - indexjob_oldest_runnable_wait_seconds{kind}          longest runnable wait
//   - indexjob_vectors_indexed{kind}
//   - indexjob_vectors_expected{kind}                      only where the kind knows it
//
// Every replica reports the database gauges with the same value, so they are
// read with max by (kind), never sum. The counters and indexjob_running are
// per replica and are summed.
//
// Cardinality: kind is the set of registered consumers (about a dozen);
// trigger, outcome, result, status and state are closed sets.
const (
	instIndexEnqueued       = "indexjob_enqueued"
	instIndexJobs           = "indexjob_jobs"
	instIndexDuration       = "indexjob_duration"
	instIndexRunning        = "indexjob_running"
	instIndexItems          = "indexjob_items"
	instIndexEmbedCalls     = "indexjob_embed_calls"
	instIndexEmbedTexts     = "indexjob_embed_texts"
	instIndexEmbedDuration  = "indexjob_embed_call_duration"
	instIndexLeasesReleased = "indexjob_leases_released"
	instIndexUnitsDeferred  = "indexjob_units_deferred"

	instIndexQueueJobs    = "indexjob_queue_jobs"
	instIndexFailedUnits  = "indexjob_failed_units"
	instIndexOldestWait   = "indexjob_oldest_runnable_wait"
	instIndexVecIndexed   = "indexjob_vectors_indexed"
	instIndexVecExpected  = "indexjob_vectors_expected"
	indexQueueScrapeLimit = 10 * time.Second
)

// Label keys and closed values the index-job series use.
const (
	attrKind    = "kind"
	attrOutcome = "outcome"
	attrResult  = "result"
	attrState   = "state"

	resultCreated  = "created"
	resultFolded   = "folded"
	resultEmbedded = "embedded"
	resultReused   = "reused"

	statePending  = "pending"
	stateRunning  = "running"
	stateRetrying = "retrying"
)

// indexJobDurationBuckets resolve an index pass, which runs from under a
// second (one tool, one prompt) to many minutes (an API spec with hundreds of
// operations on a CPU-only embedder). The default buckets stop at 60s and
// would put every large pass in +Inf.
var indexJobDurationBuckets = []float64{
	0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600, 1200, 1800, 3600,
}

// isIndexJobHistogram reports whether an instrument takes the index-job
// buckets instead of the defaults.
func isIndexJobHistogram(name string) bool {
	return name == instIndexDuration || name == instIndexEmbedDuration
}

// IndexQueueSample is one kind's state as the database holds it, read on a
// scrape by the sampler RegisterIndexQueue installs.
type IndexQueueSample struct {
	Kind        string
	Pending     int64
	Running     int64
	Retrying    int64
	FailedUnits int64
	// OldestRunnableWait is how long the longest-waiting runnable job has
	// waited for a worker; zero when none is waiting.
	OldestRunnableWait time.Duration
	// CoverageKnown is false when the kind reported no coverage; the vector
	// gauges are then not observed for it. ExpectedKnown is false for a kind
	// with no fixed denominator (the tool registry), which observes indexed
	// alone.
	CoverageKnown bool
	Indexed       int64
	Expected      int64
	ExpectedKnown bool
}

// IndexQueueSampler returns every registered kind's state. It is called on
// each scrape with a bounded context and is expected to cache.
type IndexQueueSampler func(ctx context.Context) ([]IndexQueueSample, error)

// indexJobInstruments are the queue's instruments and its one sampler.
type indexJobInstruments struct {
	enqueued       metric.Int64Counter
	jobs           metric.Int64Counter
	duration       metric.Float64Histogram
	running        metric.Int64UpDownCounter
	items          metric.Int64Counter
	embedCalls     metric.Int64Counter
	embedTexts     metric.Int64Counter
	embedDuration  metric.Float64Histogram
	leasesReleased metric.Int64Counter
	unitsDeferred  metric.Int64Counter

	queueJobs   metric.Int64ObservableGauge
	failedUnits metric.Int64ObservableGauge
	oldestWait  metric.Float64ObservableGauge
	vecIndexed  metric.Int64ObservableGauge
	vecExpected metric.Int64ObservableGauge

	mu      sync.RWMutex
	sampler IndexQueueSampler
}

// registerIndexJobInstruments registers the queue's counters, histograms and
// gauges, and the one scrape callback that reads the sampler.
func (m *Metrics) registerIndexJobInstruments(meter metric.Meter) error {
	ix := &m.index
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
	histogram := func(name, desc string) metric.Float64Histogram {
		if err != nil {
			return nil
		}
		var h metric.Float64Histogram
		h, err = meter.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unitSeconds))
		err = wrapReg(name, err)
		return h
	}
	gauge := func(name, desc string) metric.Int64ObservableGauge {
		if err != nil {
			return nil
		}
		var g metric.Int64ObservableGauge
		g, err = meter.Int64ObservableGauge(name, metric.WithDescription(desc))
		err = wrapReg(name, err)
		return g
	}

	ix.enqueued = counter(instIndexEnqueued,
		"Total index jobs enqueued, labeled by kind, trigger (write, reconciler, manual_retry), and result: created, or folded into a pending or running job for the same unit.")
	ix.jobs = counter(instIndexJobs,
		"Total index jobs claimed and settled, labeled by kind, trigger, and outcome (succeeded, retried, failed, source_gone, lease_lost, store_error).")
	ix.duration = histogram(instIndexDuration,
		"Index job duration in seconds, from claim to the write that settled it, labeled by kind and outcome.")
	ix.items = counter(instIndexItems,
		"Total items planned by index passes, labeled by kind and result: embedded (sent to the provider) or reused (text unchanged, persisted vector kept).")
	ix.embedCalls = counter(instIndexEmbedCalls,
		"Total EmbedBatch calls made by index passes, labeled by kind and status (ok, timeout, error). A timeout halves the batch and retries it.")
	ix.embedTexts = counter(instIndexEmbedTexts,
		"Total texts sent to the embedding provider by index passes, labeled by kind. Exceeds embedded items by the texts re-sent after a timeout.")
	ix.embedDuration = histogram(instIndexEmbedDuration,
		"Latency of one EmbedBatch call made by an index pass, in seconds, labeled by kind.")
	ix.leasesReleased = counter(instIndexLeasesReleased,
		"Total running index jobs whose lease expired and was released back to pending by the reaper. A rising value is a worker dying or stalling mid-pass.")
	ix.unitsDeferred = counter(instIndexUnitsDeferred,
		"Total gaps the reconciler left unqueued because their unit keeps failing and is parked, labeled by kind.")
	ix.queueJobs = gauge(instIndexQueueJobs,
		"Open index jobs in the database, labeled by kind and state (pending, running, retrying; retrying is the pending jobs waiting out a backoff). Every replica reports the same value: read with max.")
	ix.failedUnits = gauge(instIndexFailedUnits,
		"Units with an open (unresolved) index failure, labeled by kind. Read with max.")
	ix.vecIndexed = gauge(instIndexVecIndexed,
		"Vectors persisted for the kind, labeled by kind. Read with max.")
	ix.vecExpected = gauge(instIndexVecExpected,
		"Vectors the kind expects when fully indexed, labeled by kind; absent for a kind with no fixed denominator. Read with max.")
	if err != nil {
		return err
	}
	if ix.running, err = meter.Int64UpDownCounter(instIndexRunning,
		metric.WithDescription("Index jobs executing on this replica, labeled by kind.")); err != nil {
		return wrapReg(instIndexRunning, err)
	}
	if ix.oldestWait, err = meter.Float64ObservableGauge(instIndexOldestWait,
		metric.WithDescription("Seconds the longest-waiting runnable index job has waited for a worker, labeled by kind; zero when none waits. A job in retry backoff is not waiting. Read with max."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instIndexOldestWait, err)
	}
	if _, err := meter.RegisterCallback(m.observeIndexQueue,
		ix.queueJobs, ix.failedUnits, ix.oldestWait, ix.vecIndexed, ix.vecExpected); err != nil {
		return fmt.Errorf(instErrFmt, "indexjob callback", err)
	}
	return nil
}

// RegisterIndexQueue installs the sampler the database gauges are read from.
// Nil-safe; a later call replaces the earlier sampler.
func (m *Metrics) RegisterIndexQueue(s IndexQueueSampler) {
	if m == nil {
		return
	}
	m.index.mu.Lock()
	defer m.index.mu.Unlock()
	m.index.sampler = s
}

// observeIndexQueue is the scrape callback for the database gauges. A sampler
// error is logged and the scrape carries no index gauges, rather than failing
// the whole scrape.
func (m *Metrics) observeIndexQueue(ctx context.Context, o metric.Observer) error {
	m.index.mu.RLock()
	sampler := m.index.sampler
	m.index.mu.RUnlock()
	if sampler == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, indexQueueScrapeLimit)
	defer cancel()
	samples, err := sampler(ctx)
	if err != nil {
		slog.Warn("observability: index queue sample failed", "error", err)
		return nil
	}
	ix := &m.index
	for _, s := range samples {
		kind := attribute.String(attrKind, s.Kind)
		set := metric.WithAttributes(kind)
		for state, n := range map[string]int64{
			statePending: s.Pending, stateRunning: s.Running, stateRetrying: s.Retrying,
		} {
			o.ObserveInt64(ix.queueJobs, n, metric.WithAttributes(kind, attribute.String(attrState, state)))
		}
		o.ObserveInt64(ix.failedUnits, s.FailedUnits, set)
		o.ObserveFloat64(ix.oldestWait, s.OldestRunnableWait.Seconds(), set)
		if !s.CoverageKnown {
			continue
		}
		o.ObserveInt64(ix.vecIndexed, s.Indexed, set)
		if s.ExpectedKnown {
			o.ObserveInt64(ix.vecExpected, s.Expected, set)
		}
	}
	return nil
}

// IndexJobEnqueued records one Enqueue. Nil-safe.
func (m *Metrics) IndexJobEnqueued(ctx context.Context, kind, trigger string, created bool) {
	if m == nil {
		return
	}
	result := resultFolded
	if created {
		result = resultCreated
	}
	m.index.enqueued.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrKind, kind),
		attribute.String(attrTrigger, trigger),
		attribute.String(attrResult, result),
	))
}

// IndexJobStarted marks a job executing on this replica. Nil-safe.
func (m *Metrics) IndexJobStarted(ctx context.Context, kind string) {
	if m == nil {
		return
	}
	m.index.running.Add(ctx, 1, metric.WithAttributes(attribute.String(attrKind, kind)))
}

// IndexJobFinished records a job settling: the running gauge drops, the job is
// counted by outcome, and its duration is observed. Nil-safe.
func (m *Metrics) IndexJobFinished(ctx context.Context, kind, trigger, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	k := attribute.String(attrKind, kind)
	out := attribute.String(attrOutcome, outcome)
	m.index.running.Add(ctx, -1, metric.WithAttributes(k))
	m.index.jobs.Add(ctx, 1, metric.WithAttributes(k, attribute.String(attrTrigger, trigger), out))
	m.index.duration.Record(ctx, d.Seconds(), metric.WithAttributes(k, out))
}

// IndexJobItems records one pass's plan. Nil-safe.
func (m *Metrics) IndexJobItems(ctx context.Context, kind string, embedded, reused int) {
	if m == nil {
		return
	}
	k := attribute.String(attrKind, kind)
	m.index.items.Add(ctx, int64(embedded), metric.WithAttributes(k, attribute.String(attrResult, resultEmbedded)))
	m.index.items.Add(ctx, int64(reused), metric.WithAttributes(k, attribute.String(attrResult, resultReused)))
}

// IndexEmbedCall records one EmbedBatch call. Nil-safe.
func (m *Metrics) IndexEmbedCall(ctx context.Context, kind string, texts int, status string, d time.Duration) {
	if m == nil {
		return
	}
	k := attribute.String(attrKind, kind)
	m.index.embedCalls.Add(ctx, 1, metric.WithAttributes(k, attribute.String(attrStatus, status)))
	m.index.embedTexts.Add(ctx, int64(texts), metric.WithAttributes(k))
	m.index.embedDuration.Record(ctx, d.Seconds(), metric.WithAttributes(k))
}

// IndexLeasesReleased records one reaper sweep's reclaimed leases. Nil-safe.
func (m *Metrics) IndexLeasesReleased(ctx context.Context, released int) {
	if m == nil || released <= 0 {
		return
	}
	m.index.leasesReleased.Add(ctx, int64(released))
}

// IndexUnitsDeferred records the parked units one reconciler sweep skipped for
// a kind. Nil-safe.
func (m *Metrics) IndexUnitsDeferred(ctx context.Context, kind string, deferred int) {
	if m == nil || deferred <= 0 {
		return
	}
	m.index.unitsDeferred.Add(ctx, int64(deferred),
		metric.WithAttributes(attribute.String(attrKind, kind)))
}
