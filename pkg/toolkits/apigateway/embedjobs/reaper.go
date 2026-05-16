package embedjobs

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Reaper periodically releases expired leases so jobs whose
// holding workers crashed return to the queue. One Reaper per
// pod is fine (the UPDATE is idempotent and the cost is one
// query per ReaperInterval) but multiple are also safe — the
// update has no race because each row's status=running
// predicate is checked atomically.
type Reaper struct {
	store    Store
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
}

// NewReaper constructs a Reaper. interval=0 selects the package
// default (ReaperInterval).
func NewReaper(store Store, interval time.Duration) *Reaper {
	if interval <= 0 {
		interval = ReaperInterval
	}
	return &Reaper{
		store:    store,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the periodic sweep. Safe to call multiple times.
func (r *Reaper) Start(_ context.Context) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	r.wg.Add(1)
	go r.run() //#nosec G118
}

// Stop signals shutdown and waits for the goroutine.
func (r *Reaper) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.wg.Wait()
}

func (r *Reaper) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	// Run once on start so a pod that just took over (rolling
	// restart, scale-up) immediately sweeps any leases the
	// outgoing pod's worker left in flight.
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

func (r *Reaper) sweepOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), r.interval/2)
	defer cancel()
	n, err := r.store.ReleaseExpiredLeases(ctx)
	if err != nil {
		slog.Warn("embedjobs: reaper sweep failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("embedjobs: reaper released expired leases", "count", n)
	}
}
