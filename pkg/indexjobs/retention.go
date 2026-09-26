package indexjobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Retainer periodically purges finished job history so index_jobs stays
// bounded. The table gains one row per reconciler sweep per unit (every
// ReconcilerInterval, on every replica), so succeeded history grows
// without limit; the reaper only releases leases and never deletes. The
// Retainer is that missing sweep: it deletes succeeded and
// failed-and-resolved rows older than the retention window, while
// leaving open failures and in-flight rows untouched (see
// Store.PurgeTerminal).
//
// Like the Reaper, one Retainer per pod is fine and multiple are safe:
// the DELETE is idempotent (a row another replica already removed simply
// does not match), so no cross-replica coordination is needed.
type Retainer struct {
	store    Store
	days     int
	interval time.Duration
	// failedDays is how long an unresolved failure is kept (#1904); 0 keeps
	// it until someone resolves it.
	failedDays int
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	started    atomic.Bool
}

// retentionSweepTimeout caps a single purge. The DELETE rides the
// index_jobs_retention partial index and runs against a table the sweep
// itself keeps bounded, so it completes in well under a second in steady
// state; the cap bounds a pathological first sweep against a large
// pre-retention backlog without tying the timeout to the (long) sweep
// interval the way the reaper's interval/2 does.
const retentionSweepTimeout = 5 * time.Minute

// NewRetainer constructs a Retainer. days <= 0 selects
// DefaultRetentionDays; interval <= 0 selects RetentionInterval. The
// caller decides whether to start it at all (a deployment that wants
// unbounded history simply never wires one); once started it always
// applies a positive window.
func NewRetainer(store Store, days int, interval time.Duration, opts ...RetainerOption) *Retainer {
	if days <= 0 {
		days = DefaultRetentionDays
	}
	if interval <= 0 {
		interval = RetentionInterval
	}
	r := &Retainer{store: store, days: days, interval: interval, stopCh: make(chan struct{})}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RetainerOption configures a Retainer.
type RetainerOption func(*Retainer)

// WithFailedRetention also deletes failures nobody resolved once they are
// older than days (#1904). Without it an unresolved failure is kept until it
// is resolved, and a unit whose source was removed while its job was failing
// is kept for good. days <= 0 leaves them.
func WithFailedRetention(days int) RetainerOption {
	return func(r *Retainer) { r.failedDays = days }
}

// FailedPurger is the store half of WithFailedRetention.
type FailedPurger interface {
	PurgeUnresolvedFailed(ctx context.Context, retentionDays int) (int, error)
}

// Start begins the periodic sweep. Safe to call multiple times; only the
// first call starts the goroutine.
func (r *Retainer) Start(_ context.Context) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	r.wg.Add(1)
	go r.run() // #nosec G118 -- background goroutine; ctx is created per-iteration inside the loop
}

// Stop signals shutdown and waits for the goroutine.
func (r *Retainer) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

func (r *Retainer) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	// Sweep once on start so a pod that just booted purges whatever
	// backlog aged past the window while no replica was running.
	r.sweepOnce()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.sweepOnce()
		}
	}
}

func (r *Retainer) sweepOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), retentionSweepTimeout)
	defer cancel()
	n, err := r.store.PurgeTerminal(ctx, r.days)
	if err != nil {
		slog.Warn("indexjobs: retention sweep failed", logKeyError, err)
		return
	}
	if n > 0 {
		slog.Info("indexjobs: retention purged terminal jobs", "count", n, "retention_days", r.days)
	}
	fp, ok := r.store.(FailedPurger)
	if r.failedDays <= 0 || !ok {
		return
	}
	n, err = fp.PurgeUnresolvedFailed(ctx, r.failedDays)
	if err != nil {
		slog.Warn("indexjobs: unresolved-failure retention sweep failed", logKeyError, err)
		return
	}
	if n > 0 {
		slog.Info("indexjobs: retention purged unresolved failures", "count", n, "retention_days", r.failedDays)
	}
}
