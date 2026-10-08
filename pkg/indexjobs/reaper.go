package indexjobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
)

// Reaper periodically releases expired leases so jobs whose holding
// workers crashed return to the queue. One Reaper per pod is fine
// (the UPDATE is idempotent and the cost is one query per interval)
// but multiple are also safe: each row's status=running predicate is
// checked atomically.
type Reaper struct {
	store    Store
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
}

// NewReaper constructs a Reaper. interval=0 selects ReaperInterval.
func NewReaper(store Store, interval time.Duration) *Reaper {
	if interval <= 0 {
		interval = ReaperInterval
	}
	return &Reaper{store: store, interval: interval, stopCh: make(chan struct{})}
}

// Start begins the periodic sweep. Safe to call multiple times.
func (r *Reaper) Start(_ context.Context) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	r.wg.Add(1)
	go r.run() // #nosec G118 -- background goroutine; ctx is created per-iteration inside the loop
}

// Stop signals shutdown and waits for the goroutine.
func (r *Reaper) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

func (r *Reaper) run() {
	defer r.wg.Done()
	// Run once on start so a pod that just took over immediately
	// sweeps any leases the outgoing pod's worker left in flight.
	bgloop.Run(context.Background(), bgloop.Loop{
		Name: bgloop.NameIndexJobReaper, Every: r.interval, Immediate: true, Stop: r.stopCh,
		Body: r.sweepOnce,
	})
}

func (r *Reaper) sweepOnce(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, r.interval/2)
	defer cancel()
	n, err := r.store.ReleaseExpiredLeases(ctx)
	if err != nil {
		slog.WarnContext(ctx, "indexjobs: reaper sweep failed", logKeyError, err)
		return fmt.Errorf("indexjobs: reaper: %w", err)
	}
	if n > 0 {
		slog.InfoContext(ctx, "indexjobs: reaper released expired leases", "count", n)
	}
	return nil
}
