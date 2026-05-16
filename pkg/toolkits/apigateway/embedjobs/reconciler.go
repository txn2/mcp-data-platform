package embedjobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Reconciler is the gap-detection backstop. The producer path
// (spec writes enqueue jobs) is the primary trigger for embedding
// work; the reconciler covers the cases the producer misses:
//
//   - A spec was written before the embedding provider was
//     configured, so no producer job ran.
//   - A producer job ran, failed terminally, and the operator
//     never noticed.
//   - A backup/restore brought specs back without their vectors.
//   - The api_catalog_operation_embeddings table was manually
//     pruned for debugging.
//
// The query is a single SQL statement (ReconcileGaps on Store)
// that compares operation_count to embedding row counts and
// inserts a job row for every mismatch where no open job already
// exists. The unique partial index on (catalog_id, spec_name)
// WHERE status IN ('pending','running') makes the insert
// idempotent — multiple pods running the reconciler in lock-step
// produce the same set of jobs, not duplicates.
type Reconciler struct {
	store    Store
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
}

// NewReconciler constructs a Reconciler. interval=0 selects the
// package default (ReconcilerInterval).
func NewReconciler(store Store, interval time.Duration) *Reconciler {
	if interval <= 0 {
		interval = ReconcilerInterval
	}
	return &Reconciler{
		store:    store,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the periodic reconciliation loop. The first sweep
// runs immediately so a pod that just booted converges any gaps
// before its workers go idle. Subsequent sweeps follow the
// configured interval.
func (r *Reconciler) Start(_ context.Context) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	r.wg.Add(1)
	go r.run() // #nosec G118 -- background goroutine; ctx is created per-iteration inside the loop
}

// Stop signals shutdown and waits for the goroutine.
func (r *Reconciler) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
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

func (r *Reconciler) reconcileOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), r.interval/2)
	defer cancel()
	n, err := r.store.ReconcileGaps(ctx)
	if err != nil {
		slog.Warn("embedjobs: reconciler sweep failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("embedjobs: reconciler enqueued gap jobs", "count", n)
	}
}
