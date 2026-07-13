package harness

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/test/load/internal/profile"
	"github.com/txn2/mcp-data-platform/test/load/internal/scrape"
)

// snapshotSet is a concurrency-safe, insertion-ordered collection of scrapes.
// "before" is added first, "during" samples by the sampler goroutine, and
// "after" last (after the sampler has joined), so insertion order is the time
// order the report relies on.
type snapshotSet struct {
	mu    sync.Mutex
	snaps []scrape.Snapshot
}

func (s *snapshotSet) add(snap scrape.Snapshot, ok bool) {
	if !ok {
		return
	}
	s.mu.Lock()
	s.snaps = append(s.snaps, snap)
	s.mu.Unlock()
}

func (s *snapshotSet) ordered() []scrape.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]scrape.Snapshot, len(s.snaps))
	copy(out, s.snaps)
	return out
}

// scrapeOrLog takes one snapshot, logging (not failing) on error. Returns ok
// false when the scraper is absent or the scrape failed, so a run without a
// reachable metrics endpoint still completes with latency data.
func scrapeOrLog(ctx context.Context, sc *scrape.Scraper, label string, log *slog.Logger) (scrape.Snapshot, bool) {
	if sc == nil {
		return scrape.Snapshot{}, false
	}
	snap, err := sc.Snapshot(ctx, label)
	if err != nil {
		log.Warn("metrics scrape failed", "label", label, "error", err)
		return scrape.Snapshot{}, false
	}
	return snap, true
}

// sampler periodically scrapes metrics during the measured window.
type sampler struct {
	scraper  *scrape.Scraper
	interval time.Duration
	set      *snapshotSet
	log      *slog.Logger

	started bool
	stopCh  chan struct{}
	done    chan struct{}
}

func newSampler(sc *scrape.Scraper, interval time.Duration, set *snapshotSet, log *slog.Logger) *sampler {
	return &sampler{scraper: sc, interval: interval, set: set, log: log}
}

// start launches the sampling goroutine. It is a no-op when there is nothing to
// sample (no scraper or non-positive interval), leaving stop safe to call.
func (s *sampler) start(ctx context.Context) {
	if s.scraper == nil || s.interval <= 0 {
		return
	}
	s.started = true
	s.stopCh = make(chan struct{})
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case <-t.C:
				s.set.add(scrapeOrLog(ctx, s.scraper, "during", s.log))
			}
		}
	}()
}

// stop signals the sampler and waits for it to finish. Safe if start was a
// no-op.
func (s *sampler) stop() {
	if !s.started {
		return
	}
	close(s.stopCh)
	<-s.done
}

// cpuCapture runs a bounded CPU profile over the start of the measured window,
// concurrently with the run, so the profile reflects steady-state load.
type cpuCapture struct {
	prof   *profile.Capturer
	prefix string
	dur    time.Duration
	log    *slog.Logger

	started bool
	path    string
	done    chan struct{}
}

func (c *cpuCapture) start(ctx context.Context) {
	if c.prof == nil || !c.prof.Enabled() {
		return
	}
	c.started = true
	c.done = make(chan struct{})
	secs := min(c.dur, cpuProfileMaxSeconds*time.Second)
	go func() {
		defer close(c.done)
		path, err := c.prof.CaptureCPU(ctx, c.prefix, secs)
		if err != nil {
			c.log.Warn("cpu profile capture failed", "error", err)
			return
		}
		c.path = path
	}()
}

// wait blocks for the CPU capture to finish and returns its path ("" if none).
func (c *cpuCapture) wait() string {
	if !c.started {
		return ""
	}
	<-c.done
	return c.path
}

// collectProfiles joins the CPU capture and then takes the post-run heap and
// goroutine profiles, returning the non-empty file paths.
func collectProfiles(ctx context.Context, cpu *cpuCapture, prof *profile.Capturer, prefix string, log *slog.Logger) []string {
	var paths []string
	if p := cpu.wait(); p != "" {
		paths = append(paths, p)
	}
	if prof == nil || !prof.Enabled() {
		return paths
	}
	if p, err := prof.CaptureHeap(ctx, prefix); err != nil {
		log.Warn("heap profile capture failed", "error", err)
	} else if p != "" {
		paths = append(paths, p)
	}
	if p, err := prof.CaptureGoroutine(ctx, prefix); err != nil {
		log.Warn("goroutine profile capture failed", "error", err)
	} else if p != "" {
		paths = append(paths, p)
	}
	return paths
}
