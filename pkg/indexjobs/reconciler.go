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
	var total int
	for _, sink := range r.registry.Sinks() {
		ids, err := sink.FindGaps(ctx)
		if err != nil {
			slog.Warn("indexjobs: reconciler FindGaps failed",
				logKeySourceKind, sink.Kind(), logKeyError, err)
			continue
		}
		for _, id := range ids {
			created, err := r.store.Enqueue(ctx, Key{SourceKind: sink.Kind(), SourceID: id}, TriggerReconciler)
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
}
