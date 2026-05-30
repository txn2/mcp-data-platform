package indexjobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// Structured log keys, shared so log lines across the package stay
// greppable.
const (
	logKeyJobID      = "job_id"
	logKeySourceKind = "source_kind"
	logKeySourceID   = "source_id"
	logKeyError      = "error"
	logKeyWorkerID   = "worker_id"
)

// defaultPollEvery is the fallback wait between Claim sweeps when
// the LISTEN/NOTIFY adapter has not woken the worker. The data path
// tolerates this much latency on a missed notification.
const defaultPollEvery = 30 * time.Second

// processSafetyBound is the absolute ceiling on a single job's
// processing context. It is a backstop against a provider call that
// hangs without forward progress, NOT the normal deadline: the DB
// lease (renewed by the heartbeat at lease/3 cadence) is the
// authoritative liveness signal for a healthy run. An earlier
// api-catalog revision bounded this at LeaseDuration+30s, which
// silently defeated the heartbeat and re-triggered a doom loop; one
// hour is high enough never to clip a legitimate large embed pass.
const processSafetyBound = time.Hour

// heartbeatDivisor sets how often the heartbeat fires relative to
// the lease window. lease/3 keeps two renewal opportunities in
// flight before the reaper would consider the lease abandoned.
const heartbeatDivisor = 3

// WorkerConfig bundles the worker's dependencies. The worker is
// kind-agnostic: it resolves the Source and Sink for each claimed
// job from the Registry by the job's source_kind.
type WorkerConfig struct {
	Store    Store
	Registry *Registry

	// Embedder is the platform-wide embedding provider every kind's
	// embed pass uses. One provider serves the whole pool (#438:
	// per-kind model selection is out of scope).
	Embedder embedding.Provider

	WorkerID  string        // empty -> auto-generated
	PollEvery time.Duration // fallback poll interval; default 30s

	// Concurrency is the number of goroutines that share the queue.
	// Each independently calls Claim; the lease + SKIP LOCKED
	// machinery prevents two goroutines (same pod or across pods)
	// from picking the same job. Zero or negative falls back to 1.
	Concurrency int

	// LeaseDuration is the window each Claim stamps and the cadence
	// the heartbeat renews it. Should match the store's
	// WithLeaseDuration. Zero or negative falls back to
	// DefaultLeaseDuration.
	LeaseDuration time.Duration

	// BatchSize is the chunk size the embed pass uses per upstream
	// EmbedBatch call. Zero or negative falls back to
	// DefaultEmbedBatchSize.
	BatchSize int
}

// Worker drains the job queue. One Worker instance per pod is the
// typical deployment; multiple workers in the same pod are
// supported and race for jobs the same way workers across pods do.
type Worker struct {
	cfg      WorkerConfig
	wakeup   chan struct{} // buffer 1; LISTEN/NOTIFY adapter signals this
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
}

// NewWorker constructs a Worker from the supplied config, filling
// defaults for the optional fields. The returned Worker is idle
// until Start is called.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.WorkerID == "" {
		cfg.WorkerID = generateWorkerID()
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = defaultPollEvery
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = DefaultLeaseDuration
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultEmbedBatchSize
	}
	return &Worker{
		cfg:    cfg,
		wakeup: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
	}
}

// Notify is the LISTEN/NOTIFY adapter's hook. The listener calls
// this when a NOTIFY arrives so the worker drops out of its poll
// wait. Buffered size 1 so a flurry coalesces into a single wake.
func (w *Worker) Notify() {
	select {
	case w.wakeup <- struct{}{}:
	default:
	}
}

// Concurrency reports the number of goroutines this worker spawns
// on Start. Exposed so wiring tests can assert the configured value
// flowed through without exporting cfg.
func (w *Worker) Concurrency() int { return w.cfg.Concurrency }

// Start begins the worker loop. Safe to call multiple times; only
// the first call spawns goroutines. One run() goroutine per
// Concurrency unit; each shares the wakeup channel and stopCh and
// races for jobs through Claim's SKIP LOCKED predicate.
func (w *Worker) Start(_ context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	for i := 0; i < w.cfg.Concurrency; i++ {
		w.wg.Add(1)
		go w.run() // #nosec G118 -- background goroutine uses its own context per iteration
	}
}

// Stop signals shutdown and waits for the goroutines to exit. Safe
// to call multiple times.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.wg.Wait()
}

// run is the main worker loop. Sleeps on wakeup OR poll interval;
// on wake drains the queue until ErrNoJob.
func (w *Worker) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.cfg.PollEvery)
	defer ticker.Stop()
	for {
		w.drainQueue()
		select {
		case <-w.stopCh:
			return
		case <-w.wakeup:
		case <-ticker.C:
		}
	}
}

// drainQueue claims and processes jobs until the queue is empty or
// shutdown is signaled. Each iteration is bounded by
// processSafetyBound only as a backstop; the DB lease is the
// authoritative deadline for a normal run.
func (w *Worker) drainQueue() {
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), processSafetyBound)
		job, err := w.cfg.Store.Claim(ctx, w.cfg.WorkerID)
		if errors.Is(err, ErrNoJob) {
			cancel()
			return
		}
		if err != nil {
			slog.Warn("indexjobs: claim failed", logKeyWorkerID, w.cfg.WorkerID, logKeyError, err)
			cancel()
			return
		}
		// A successful claim implies the queue had at least one
		// runnable job; nudge a sibling goroutine in case there is
		// more. Buffered to 1 so this coalesces with a pending wake.
		w.Notify()
		w.process(ctx, job)
		cancel()
	}
}

// process runs the embedding pass for one job. The outcome flows
// through one of Complete / Retry / Fail. Provider errors are
// retryable up to MaxAttempts; an unregistered kind or a missing
// source row is terminal (retrying won't help).
func (w *Worker) process(ctx context.Context, job *Job) {
	slog.Info("indexjobs: starting job",
		logKeyJobID, job.ID, logKeySourceKind, job.SourceKind,
		logKeySourceID, job.SourceID, "trigger", string(job.Trigger),
		"attempts", job.Attempts)

	source, sink, ok := w.cfg.Registry.Lookup(job.SourceKind)
	if !ok {
		// A job for a kind no consumer registered (a leftover row
		// from a removed consumer). Terminal: nothing can run it.
		w.terminate(ctx, job, fmt.Sprintf("no consumer registered for source_kind %q", job.SourceKind))
		return
	}

	items, err := source.LoadItems(ctx, job.SourceID)
	if err != nil {
		// The source row is gone or unreadable. Not retryable: it
		// may have been deleted on purpose. Move to terminal failed.
		w.terminate(ctx, job, fmt.Sprintf("load items failed: %v", err))
		return
	}

	key := Key{SourceKind: job.SourceKind, SourceID: job.SourceID}

	// A manual_retry job is the operator's "model swapped
	// externally" escape hatch: skip ListExisting so every item is
	// re-embedded. The Sink's Upsert is an atomic replace, so stale
	// vectors are swapped out cleanly.
	var existing map[string]Vector
	if job.Trigger != TriggerManualRetry {
		existing, err = sink.ListExisting(ctx, key)
		if err != nil {
			// A read failure from our own DB is retryable.
			w.retryOrFail(ctx, job, fmt.Sprintf("list existing failed: %v", err))
			return
		}
	}

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go w.heartbeat(hbCtx, job)

	rows, err := embedItems(ctx, embedRequest{
		embedder:     w.cfg.Embedder,
		items:        items,
		existing:     existing,
		batchSize:    w.cfg.BatchSize,
		progress:     w.progressFn(ctx, job),
		persistBatch: w.persistBatchFn(ctx, key, sink),
	})
	if err != nil {
		w.retryOrFail(ctx, job, fmt.Sprintf("embed failed: %v", err))
		return
	}

	if err := sink.Upsert(ctx, key, rows); err != nil {
		w.retryOrFail(ctx, job, fmt.Sprintf("persist failed: %v", err))
		return
	}

	// Stamp the expected item count so the reconciler's gap
	// predicate sees a fully-indexed unit on its next sweep.
	// Best-effort: a failure logs but does not undo the embed; the
	// reconciler re-detects and retries on the next tick.
	if err := sink.StampExpected(ctx, key, len(rows)); err != nil {
		slog.Warn("indexjobs: stamp expected failed",
			logKeyJobID, job.ID, logKeySourceKind, job.SourceKind,
			logKeySourceID, job.SourceID, "rows", len(rows), logKeyError, err)
	}

	if err := w.cfg.Store.Complete(ctx, job.ID, w.cfg.WorkerID); err != nil {
		if errors.Is(err, ErrNotFound) {
			slog.Warn("indexjobs: complete after lease rotation",
				logKeyJobID, job.ID, logKeyWorkerID, w.cfg.WorkerID)
			return
		}
		slog.Error("indexjobs: complete failed", logKeyJobID, job.ID, logKeyError, err)
		return
	}

	source.OnSucceeded(job.SourceID)
	slog.Info("indexjobs: job complete",
		logKeyJobID, job.ID, logKeySourceKind, job.SourceKind,
		logKeySourceID, job.SourceID, "rows", len(rows))
}

// progressFn returns the best-effort chunk-boundary progress
// publisher for a job. A missed update just delays a UI tick by one
// chunk; UpdateProgress already no-ops on a rotated lease.
func (w *Worker) progressFn(ctx context.Context, job *Job) func(int) {
	return func(completed int) {
		if err := w.cfg.Store.UpdateProgress(ctx, job.ID, w.cfg.WorkerID, completed); err != nil {
			slog.Debug("indexjobs: update_progress failed",
				logKeyJobID, job.ID, "items_done", completed, logKeyError, err)
		}
	}
}

// persistBatchFn returns the per-chunk durable persist callback. It
// forwards each chunk to Sink.UpsertBatch so a job that fails on
// chunk N still has chunks 0..N-1 saved for the next attempt's
// dedup pass.
func (*Worker) persistBatchFn(ctx context.Context, key Key, sink Sink) func([]Vector) error {
	return func(batch []Vector) error {
		if err := sink.UpsertBatch(ctx, key, batch); err != nil {
			return fmt.Errorf("persist batch: %w", err)
		}
		return nil
	}
}

// heartbeat renews the job's lease at lease/heartbeatDivisor
// cadence while the embed pass runs. Stops on ctx.Done (the
// deferred cancel after the pass returns) or on the first
// ErrNotFound from RenewLease (the lease rotated to another
// worker). Other errors are logged but do not stop the heartbeat:
// a transient DB blip would otherwise let the reaper kill the
// context mid-batch, the exact failure the heartbeat prevents.
func (w *Worker) heartbeat(ctx context.Context, job *Job) {
	interval := w.cfg.LeaseDuration / heartbeatDivisor
	if interval <= 0 {
		interval = DefaultLeaseDuration / heartbeatDivisor
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := w.cfg.Store.RenewLease(ctx, job.ID, w.cfg.WorkerID, w.cfg.LeaseDuration)
			if errors.Is(err, ErrNotFound) {
				slog.Info("indexjobs: heartbeat stopping; lease rotated",
					logKeyJobID, job.ID, logKeyWorkerID, w.cfg.WorkerID)
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("indexjobs: lease renewal failed; will retry next tick",
					logKeyJobID, job.ID, logKeyWorkerID, w.cfg.WorkerID, logKeyError, err)
			}
		}
	}
}

// retryOrFail routes a job to Retry (with backoff) or Fail
// (terminal) based on the attempts counter, which Claim already
// incremented.
func (w *Worker) retryOrFail(ctx context.Context, job *Job, errMsg string) {
	slog.Warn("indexjobs: job error",
		logKeyJobID, job.ID, logKeySourceKind, job.SourceKind,
		logKeySourceID, job.SourceID, "attempts", job.Attempts, logKeyError, errMsg)
	if job.Attempts >= MaxAttempts {
		w.terminate(ctx, job, errMsg)
		return
	}
	if err := w.cfg.Store.Retry(ctx, job.ID, w.cfg.WorkerID, errMsg); err != nil {
		slog.Error("indexjobs: retry release failed", logKeyJobID, job.ID, logKeyError, err)
	}
}

// terminate marks a job permanently failed.
func (w *Worker) terminate(ctx context.Context, job *Job, errMsg string) {
	slog.Warn("indexjobs: job failed terminally",
		logKeyJobID, job.ID, logKeySourceKind, job.SourceKind,
		logKeySourceID, job.SourceID, "attempts", job.Attempts, logKeyError, errMsg)
	if err := w.cfg.Store.Fail(ctx, job.ID, w.cfg.WorkerID, errMsg); err != nil {
		slog.Error("indexjobs: fail-state write failed", logKeyJobID, job.ID, logKeyError, err)
	}
}

// generateWorkerID composes "host-randhex" for log/audit
// readability. Hostname identifies the pod; the random suffix keeps
// two workers on the same pod distinguishable.
func generateWorkerID() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", host, time.Now().UnixNano())
	}
	return host + "-" + hex.EncodeToString(buf)
}
