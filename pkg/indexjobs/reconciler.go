package indexjobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Reconciler is the gap-detection backstop. The producer path
// (consumer write paths enqueue jobs) is the primary trigger for
// indexing work; the reconciler covers the cases the producer
// misses:
//
//   - A source row was written before the embedding provider was
//     configured, so no producer job ran.
//   - A producer job ran, failed terminally, and the operator never
//     noticed.
//   - A backup/restore brought source rows back without vectors.
//   - The kind's vector table was manually pruned for debugging.
//
// Unlike the api-catalog precursor (one SQL statement against one
// pair of tables), gap detection here is per kind: the indexed
// count lives in each kind's own vector table, so each Sink owns
// its FindGaps query. The reconciler walks every registered Sink,
// asks for the source ids that need (re)indexing, and enqueues a
// reconciler job for each. The partial unique index on index_jobs
// makes the enqueue idempotent across pods running the sweep in
// lock-step.
type Reconciler struct {
	store    Store
	registry *Registry
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
}

// NewReconciler constructs a Reconciler. interval=0 selects
// ReconcilerInterval.
func NewReconciler(store Store, registry *Registry, interval time.Duration) *Reconciler {
	if interval <= 0 {
		interval = ReconcilerInterval
	}
	return &Reconciler{
		store:    store,
		registry: registry,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the periodic reconciliation loop. The first sweep
// runs immediately so a freshly-booted pod converges any gaps
// before its workers go idle.
func (r *Reconciler) Start(_ context.Context) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	r.wg.Add(1)
	go r.run() // #nosec G118 -- background goroutine; ctx is created per-iteration inside the loop
}

// Stop signals shutdown and waits for the goroutine.
func (r *Reconciler) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

func (r *Reconciler) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.reconcileOnce()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.reconcileOnce()
		}
	}
}

// reconcileOnce sweeps every registered Sink for gaps and enqueues
// a reconciler job per gap. A Sink whose FindGaps errors is logged
// and skipped; the other kinds still converge, and the next tick
// retries the failed kind.
func (r *Reconciler) reconcileOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), r.interval/2)
	defer cancel()
	parked := r.parkedUnits(ctx)
	var total, deferred int
	for _, sink := range r.registry.Sinks() {
		ids, err := sink.FindGaps(ctx)
		if err != nil {
			slog.Warn("indexjobs: reconciler FindGaps failed",
				logKeySourceKind, sink.Kind(), logKeyError, err)
			continue
		}
		for _, id := range ids {
			key := Key{SourceKind: sink.Kind(), SourceID: id}
			if _, ok := parked[key]; ok {
				deferred++
				continue
			}
			created, err := r.store.Enqueue(ctx, key, TriggerReconciler)
			if err != nil {
				slog.Warn("indexjobs: reconciler enqueue failed",
					logKeySourceKind, sink.Kind(), logKeySourceID, id, logKeyError, err)
				continue
			}
			if created {
				total++
			}
		}
	}
	if total > 0 {
		slog.Info("indexjobs: reconciler enqueued gap jobs", "count", total)
	}
	if deferred > 0 {
		slog.Info("indexjobs: reconciler deferred parked units", "count", deferred)
	}
}

// parkedUnits returns the units this sweep must not re-queue, keyed for
// O(1) lookup. One query covers every kind, so the cost is one read per
// sweep rather than one per gap.
//
// Two deliberate degradations, both toward re-queueing rather than
// withholding work:
//
//   - A read failure returns an empty set, which re-queues every gap.
//     Closing gaps is the sweep's job; losing one tick of deferral is a
//     far smaller fault than skipping reconciliation.
//   - The scan is bounded by parkScanLimit. Its predicate already narrows
//     to units carrying ParkThreshold open failures, so the bound is a
//     backstop against a pathological corpus, and the query's key ordering
//     keeps the same units deferred across sweeps rather than rotating.
func (r *Reconciler) parkedUnits(ctx context.Context) map[Key]struct{} {
	candidates, err := r.store.ParkCandidates(ctx, ParkThreshold, parkScanLimit)
	if err != nil {
		slog.Warn("indexjobs: reconciler park scan failed; re-queueing every gap this sweep",
			logKeyError, err)
		return nil
	}
	now := time.Now()
	parked := make(map[Key]struct{}, len(candidates))
	for _, c := range candidates {
		if until, ok := c.ParkedUntil(); ok && until.After(now) {
			parked[c.Key] = struct{}{}
		}
	}
	return parked
}
