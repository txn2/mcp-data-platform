//nolint:revive // max-public-structs: same cohesive-queue rationale as types.go.
package embedjobs

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
)

// Worker-side constants and structured-log keys.
const (
	// claimTimeoutGrace is the slack between a worker's claim
	// context deadline and the lease itself. The worker context
	// must outlive the lease so a long-running embed does not
	// race the deadline; 30s slack covers the post-embed
	// Complete/Retry/Fail call against the DB.
	claimTimeoutGrace = 30 * time.Second

	// defaultPollEvery is the fallback poll interval when
	// WorkerConfig.PollEvery is unset. LISTEN/NOTIFY is the
	// primary wake signal; this exists so a missed notification
	// does not stall a worker indefinitely.
	defaultPollEvery = 30 * time.Second

	logKeyJobID     = "job_id"
	logKeyCatalogID = "catalog_id"
	logKeySpecName  = "spec_name"
	logKeyError     = "error"
)

// SpecResolver looks up the content of a spec by (catalog_id,
// spec_name). The worker calls it to fetch the raw OpenAPI
// document that ComputeOperationEmbeddings parses.
//
// The interface is small so tests can substitute a fixed-content
// stub without depending on the catalog.Store contract.
type SpecResolver interface {
	GetSpecContent(ctx context.Context, catalogID, specName string) (content string, err error)
}

// EmbeddingComputer is the worker's bridge to the embedding
// provider. The implementation calls
// apigateway.ComputeOperationEmbeddings (or a test stub).
type EmbeddingComputer interface {
	Compute(ctx context.Context, content, specName string, existing map[string]ExistingEmbedding) ([]ComputedEmbedding, error)
}

// ExistingEmbedding is the subset of catalog.OperationEmbedding
// the computer needs for text-hash + model dedup. Kept as a
// package-local type so embedjobs does not import the catalog
// package (and thus does not pull pgvector through every test).
// The platform-side adapter translates between this struct and
// catalog.OperationEmbedding at the boundary.
type ExistingEmbedding struct {
	OperationID string
	TextHash    []byte
	Embedding   []float32
	Model       string
	Dim         int
}

// ComputedEmbedding is the result of one operation's embedding
// pass. Same field set as catalog.OperationEmbedding; declared
// here for the same reason as ExistingEmbedding.
type ComputedEmbedding struct {
	OperationID string
	TextHash    []byte
	Embedding   []float32
	Model       string
	Dim         int
}

// EmbeddingPersister writes the worker's output (and reads the
// existing set for dedup). Wraps catalog.Store's embedding
// methods on the platform side; tests substitute a map.
//
// StampOperationCount is called after every successful Upsert
// so the reconciler can use the spec row's operation_count
// column as the expected-work target. Without this, legacy
// specs written before migration 000045 retain
// operation_count=0 and the reconciler enqueues a job every
// 5 minutes forever (the embedding row count is non-zero but
// operation_count is zero, so the predicate sees a permanent
// gap). Stamping closes the loop: after one worker run the
// columns agree.
type EmbeddingPersister interface {
	ListExisting(ctx context.Context, catalogID, specName string) (map[string]ExistingEmbedding, error)
	Upsert(ctx context.Context, catalogID, specName string, rows []ComputedEmbedding) error
	StampOperationCount(ctx context.Context, catalogID, specName string, count int) error
}

// ConnectionReloader is the optional hook the worker uses after
// a successful embed to tell the api-gateway toolkit to reload
// connections that mount this catalog so their in-memory vector
// map picks up the new rows. nil is acceptable; the toolkit
// continues to serve from its old map until the next reload
// from another path.
type ConnectionReloader interface {
	ReloadConnectionsByCatalog(catalogID string)
}

// WorkerConfig bundles the worker's dependencies.
type WorkerConfig struct {
	Store     Store
	Resolver  SpecResolver
	Computer  EmbeddingComputer
	Persister EmbeddingPersister
	Reloader  ConnectionReloader // optional
	WorkerID  string             // empty -> auto-generated
	PollEvery time.Duration      // fallback poll interval; default 30s
}

// Worker drains the job queue. One Worker instance per pod is
// the typical deployment; multiple workers in the same pod are
// supported and race for jobs the same way workers across pods
// do.
type Worker struct {
	cfg      WorkerConfig
	wakeup   chan struct{} // buffer 1; LISTEN/NOTIFY adapter signals this
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	started  atomic.Bool
}

// NewWorker constructs a Worker from the supplied config. The
// returned Worker is idle until Start is called.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.WorkerID == "" {
		cfg.WorkerID = generateWorkerID()
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = defaultPollEvery
	}
	return &Worker{
		cfg:    cfg,
		wakeup: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
	}
}

// Notify is the LISTEN/NOTIFY adapter's hook into the worker.
// The Postgres listener calls this when a NOTIFY arrives so the
// worker drops out of its poll wait and checks the queue
// immediately. Buffered size 1 so a flurry of notifications
// coalesces into a single wake.
func (w *Worker) Notify() {
	select {
	case w.wakeup <- struct{}{}:
	default:
	}
}

// Start begins the worker loop. Safe to call multiple times;
// only the first call spawns the goroutine.
func (w *Worker) Start(_ context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.run() // #nosec G118 -- background goroutine intentionally uses its own context per iteration
}

// Stop signals shutdown and waits for the goroutine to exit.
// Safe to call multiple times.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.wg.Wait()
}

// run is the main worker loop. Sleeps on wakeup OR poll
// interval; on wake calls Claim until ErrNoJob; for each
// claimed job runs the embed pass and writes the outcome.
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

// drainQueue claims and processes jobs until the queue is empty
// or shutdown is signaled. Each iteration is bounded so a flood
// of jobs cannot starve the shutdown signal.
func (w *Worker) drainQueue() {
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), LeaseDuration+claimTimeoutGrace)
		job, err := w.cfg.Store.Claim(ctx, w.cfg.WorkerID)
		if errors.Is(err, ErrNoJob) {
			cancel()
			return
		}
		if err != nil {
			slog.Warn("embedjobs: claim failed",
				"worker_id", w.cfg.WorkerID, logKeyError, err)
			cancel()
			return
		}
		w.process(ctx, job)
		cancel()
	}
}

// process runs the embedding pass for one job. Outcome flows
// through one of Complete / Retry / Fail. Errors from the
// embedding provider are retryable up to MaxAttempts; everything
// else (parse failure, persistence failure) goes straight to
// failed because retrying won't help.
func (w *Worker) process(ctx context.Context, job *Job) {
	slog.Info("embedjobs: starting job",
		logKeyJobID, job.ID, logKeyCatalogID, job.CatalogID,
		logKeySpecName, job.SpecName, "kind", string(job.Kind),
		"attempts", job.Attempts)

	content, err := w.cfg.Resolver.GetSpecContent(ctx, job.CatalogID, job.SpecName)
	if err != nil {
		// Spec disappeared between enqueue and claim. Not a
		// retryable failure — the spec might have been deleted
		// on purpose. Move to terminal failed.
		w.terminate(ctx, job, fmt.Sprintf("spec resolve failed: %v", err))
		return
	}

	// A manual_retry job is the operator's "model swapped
	// externally" escape hatch: same spec content, same model
	// name, but the underlying model behavior has drifted (or
	// the operator just wants vectors recomputed for any reason).
	// Skip ListExisting in that case so the computer sees an
	// empty existing-map and re-embeds every operation. The
	// Persister.Upsert below is delete+insert in one tx, so the
	// stale vectors get replaced atomically.
	var existing map[string]ExistingEmbedding
	if job.Kind != KindManualRetry {
		var err error
		existing, err = w.cfg.Persister.ListExisting(ctx, job.CatalogID, job.SpecName)
		if err != nil {
			// A read failure from our own DB is retryable:
			// another pod might be holding a long lock or the
			// connection pool is exhausted.
			w.retryOrFail(ctx, job, fmt.Sprintf("list existing failed: %v", err))
			return
		}
	}

	rows, err := w.cfg.Computer.Compute(ctx, content, job.SpecName, existing)
	if err != nil {
		w.retryOrFail(ctx, job, fmt.Sprintf("compute failed: %v", err))
		return
	}

	if err := w.cfg.Persister.Upsert(ctx, job.CatalogID, job.SpecName, rows); err != nil {
		w.retryOrFail(ctx, job, fmt.Sprintf("persist failed: %v", err))
		return
	}

	// Stamp operation_count so the reconciler's gap predicate
	// (s.operation_count <> COALESCE(e.embedded, 0)) sees a
	// fully-indexed spec on its next sweep. Best-effort: a
	// failure here logs but does not undo the successful embed.
	// The reconciler will re-detect and retry on the next tick
	// if this update was lost.
	if err := w.cfg.Persister.StampOperationCount(ctx, job.CatalogID, job.SpecName, len(rows)); err != nil {
		slog.Warn("embedjobs: stamp operation_count failed",
			logKeyJobID, job.ID, logKeyCatalogID, job.CatalogID,
			logKeySpecName, job.SpecName, "rows", len(rows), logKeyError, err)
	}

	if err := w.cfg.Store.Complete(ctx, job.ID, w.cfg.WorkerID); err != nil {
		if errors.Is(err, ErrNotFound) {
			slog.Warn("embedjobs: complete after lease rotation",
				logKeyJobID, job.ID, "worker_id", w.cfg.WorkerID)
			return
		}
		slog.Error("embedjobs: complete failed",
			logKeyJobID, job.ID, logKeyError, err)
		return
	}

	if w.cfg.Reloader != nil {
		w.cfg.Reloader.ReloadConnectionsByCatalog(job.CatalogID)
	}

	slog.Info("embedjobs: job complete",
		logKeyJobID, job.ID, logKeyCatalogID, job.CatalogID,
		logKeySpecName, job.SpecName, "rows", len(rows))
}

// retryOrFail consults the attempts counter and routes the job
// to Retry (with exponential backoff) or Fail (terminal).
// Attempts was already incremented by Claim, so we compare
// against MaxAttempts directly.
func (w *Worker) retryOrFail(ctx context.Context, job *Job, errMsg string) {
	slog.Warn("embedjobs: job error",
		logKeyJobID, job.ID, logKeyCatalogID, job.CatalogID,
		logKeySpecName, job.SpecName, "attempts", job.Attempts,
		logKeyError, errMsg)
	if job.Attempts >= MaxAttempts {
		w.terminate(ctx, job, errMsg)
		return
	}
	if err := w.cfg.Store.Retry(ctx, job.ID, w.cfg.WorkerID, errMsg); err != nil {
		slog.Error("embedjobs: retry release failed",
			logKeyJobID, job.ID, logKeyError, err)
	}
}

// terminate marks a job permanently failed.
func (w *Worker) terminate(ctx context.Context, job *Job, errMsg string) {
	slog.Warn("embedjobs: job failed terminally",
		logKeyJobID, job.ID, logKeyCatalogID, job.CatalogID,
		logKeySpecName, job.SpecName, "attempts", job.Attempts,
		logKeyError, errMsg)
	if err := w.cfg.Store.Fail(ctx, job.ID, w.cfg.WorkerID, errMsg); err != nil {
		slog.Error("embedjobs: fail-state write failed",
			logKeyJobID, job.ID, logKeyError, err)
	}
}

// generateWorkerID composes "host-randhex" for log/audit
// readability. Hostname identifies which pod; the random suffix
// keeps two workers on the same pod distinguishable.
func generateWorkerID() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is exceptional; fall back to a
		// timestamp-based suffix so workers still get a unique
		// id even on a misconfigured host.
		return fmt.Sprintf("%s-%d", host, time.Now().UnixNano())
	}
	return host + "-" + hex.EncodeToString(buf)
}
