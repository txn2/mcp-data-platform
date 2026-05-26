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
	// defaultPollEvery is the fallback poll interval when
	// WorkerConfig.PollEvery is unset. LISTEN/NOTIFY is the
	// primary wake signal; this exists so a missed notification
	// does not stall a worker indefinitely.
	defaultPollEvery = 30 * time.Second

	// processSafetyBound caps a single process() call as a
	// backstop against a Compute implementation that hangs
	// without making forward progress (a code bug, not a slow
	// upstream — per-HTTP-call timeouts inside the embedder
	// already bound the upstream-hang case). One hour is large
	// enough that any realistic CPU embedding pass completes
	// well inside it, while still bounding K8s pod shutdown.
	//
	// Under normal operation this never fires: the heartbeat
	// (lease/3 cadence) keeps the DB lease alive as long as
	// Compute is making progress, and the heartbeat itself
	// exits on lease rotation. The bound exists only to keep
	// a buggy Compute from pinning a worker goroutine forever
	// and stalling shutdown past the K8s termination grace.
	processSafetyBound = 1 * time.Hour

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

// ComputeRequest bundles the parameters EmbeddingComputer.Compute
// needs. The struct shape keeps the interface call-site
// self-documenting and accommodates the additional knobs that
// joined the parameter set for #479 (batch size, per-batch
// persistence callback) without growing positional arguments.
type ComputeRequest struct {
	// Content is the raw OpenAPI document text the worker fetched
	// from the SpecResolver.
	Content string

	// SpecName is the catalog key the worker is processing.
	SpecName string

	// Existing is the dedup map from Persister.ListExisting. A
	// match on (operation_id, text_hash, model, dim) lets the
	// computer reuse the existing vector instead of calling the
	// upstream embedder.
	Existing map[string]ExistingEmbedding

	// BatchSize caps the texts per upstream EmbedBatch call.
	// Zero falls back to the implementation's default.
	BatchSize int

	// Progress is called at chunk boundaries with the cumulative
	// count of operations whose vectors are ready (reused +
	// freshly embedded). The worker forwards this to
	// Store.UpdateProgress so the catalog status endpoint can
	// render incremental progress before the final commit.
	Progress func(completed int)

	// PersistBatch is called after every successful chunk with
	// just that chunk's rows. The worker's adapter forwards each
	// call to Persister.UpsertBatch so progress survives a
	// mid-job failure: the next attempt's ListExisting pass picks
	// the persisted rows up and skips the upstream call. nil
	// disables (the chunk's vectors are still in the final return
	// slice for the worker's atomic Upsert at job completion).
	PersistBatch func(rows []ComputedEmbedding) error
}

// EmbeddingComputer is the worker's bridge to the embedding
// provider. The implementation calls
// apigateway.ComputeOperationEmbeddings (or a test stub).
type EmbeddingComputer interface {
	Compute(ctx context.Context, req ComputeRequest) ([]ComputedEmbedding, error)
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

	// UpsertBatch writes a single chunk's worth of embedding rows
	// to durable storage without disturbing rows outside the
	// supplied set. The worker calls this once per chunk inside
	// EmbeddingComputer.Compute via the PersistBatch callback so
	// a job that fails mid-pass leaves its prior chunks visible
	// to the next attempt's ListExisting (and therefore to the
	// dedup map that skips the upstream re-embed). Unlike Upsert,
	// this method does NOT delete rows absent from the batch:
	// stale-row cleanup happens once at job completion via the
	// catalog adapter's final Upsert pass.
	UpsertBatch(ctx context.Context, catalogID, specName string, rows []ComputedEmbedding) error
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

	// Concurrency is the number of goroutines that share the queue.
	// Each goroutine independently calls Claim, so a flood of new
	// specs can be embedded in parallel up to this cap. The lease +
	// SKIP LOCKED machinery in Claim already prevents two goroutines
	// (in the same pod or across pods) from picking the same job.
	// Zero or negative falls back to 1, preserving the pre-#430
	// single-goroutine behavior.
	Concurrency int

	// LeaseDuration is the window each Claim stamps on a job and
	// the cadence the heartbeat goroutine uses to renew it. The
	// store's WithLeaseDuration option should be set to the same
	// value so Claim and RenewLease agree on the window.
	//
	// Zero or negative falls back to DefaultLeaseDuration. The
	// heartbeat fires at LeaseDuration / heartbeatDivisor so a
	// slow embed pass renews well before the reaper considers
	// the lease abandoned.
	LeaseDuration time.Duration

	// BatchSize is the chunk size the Computer's embed pass uses
	// per upstream EmbedBatch call. Smaller batches keep a single
	// stuck call from burning the whole budget; larger batches
	// amortize per-call overhead. Zero or negative falls back to
	// DefaultEmbedBatchSize. Plumbed through to the Computer via
	// the EmbeddingComputer interface.
	BatchSize int
}

// heartbeatDivisor sets how often the heartbeat fires relative
// to the lease window. lease/3 keeps two renewal opportunities
// in flight before the reaper would consider the lease abandoned
// (one renew at T+lease/3, second at T+2*lease/3, reaper kills
// at T+lease). Two-fault tolerance against a transient DB blip
// without spamming the table.
const heartbeatDivisor = 3

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

// Concurrency reports the number of goroutines this worker will
// spawn (or has spawned) on Start. Exposed so platform wiring tests
// can assert the configured value flowed from
// apigateway.embed_jobs.workers through WorkerConfig into the
// Worker without exporting the cfg field itself.
func (w *Worker) Concurrency() int { return w.cfg.Concurrency }

// Start begins the worker loop. Safe to call multiple times;
// only the first call spawns goroutines.
//
// One run() goroutine per WorkerConfig.Concurrency unit; each
// shares the wakeup channel + stopCh and races for jobs through
// Claim's SKIP LOCKED predicate. The lease guarantee at the DB
// level keeps two goroutines from racing on the same (catalog,
// spec) pair, so the only coordination needed inside the pod is
// the wakeup-channel buffering (one slot; a flurry of NOTIFYs
// coalesces into a single wake, which is fine because every
// goroutine drains the queue independently after waking).
func (w *Worker) Start(_ context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	for i := 0; i < w.cfg.Concurrency; i++ {
		w.wg.Add(1)
		go w.run() // #nosec G118 -- background goroutine intentionally uses its own context per iteration
	}
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
// or shutdown is signaled. Each iteration is bounded by
// processSafetyBound (1 hour) only as a backstop against a
// Compute that hangs without forward progress — the DB lease
// (renewed by the heartbeat at lease/3 cadence) is the
// authoritative deadline for a normal run. An earlier revision
// bounded the ctx at LeaseDuration + 30s, which silently
// defeated the heartbeat: even with the DB lease alive, the
// worker's local ctx would cancel Compute at the lease ceiling
// and re-trigger the doom loop #479 was filed to close. See
// processSafetyBound for the rationale on the new bound.
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
			slog.Warn("embedjobs: claim failed",
				"worker_id", w.cfg.WorkerID, logKeyError, err)
			cancel()
			return
		}
		// Fan out to sibling goroutines: a successful claim implies
		// the queue had at least one runnable job, so another idle
		// goroutine may have more to do. Notify is buffered to size
		// 1 so this coalesces with an already-pending wake. Placed
		// after the error check so a DB outage that storms Claim
		// errors does not also storm sibling-warn logs.
		w.Notify()
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

	progress := func(completed int) {
		// Best-effort progress publish. Errors are logged at debug
		// level only; a missed update just delays the UI tick by
		// one chunk and the next call (or the final Complete which
		// sets embedding_count) corrects it. UpdateProgress already
		// no-ops when the lease has rotated, so a stale write can
		// not clobber a new holder's count.
		if perr := w.cfg.Store.UpdateProgress(ctx, job.ID, w.cfg.WorkerID, completed); perr != nil {
			slog.Debug("embedjobs: update_progress failed",
				logKeyJobID, job.ID, "embedded_so_far", completed, logKeyError, perr)
		}
	}
	persistBatch := func(batch []ComputedEmbedding) error {
		// Per-batch durable persistence. Forwards to the
		// Persister's batch upsert so a job that fails on chunk N
		// still has vectors for chunks 0..N-1 saved. The next
		// attempt's ListExisting pass sees them via the dedup map
		// and skips the upstream call — the doom loop described
		// in #479 is closed by this single call. Errors
		// short-circuit the compute pass via a wrapping fmt.Errorf
		// in fillFreshEmbeddings.
		if perr := w.cfg.Persister.UpsertBatch(ctx, job.CatalogID, job.SpecName, batch); perr != nil {
			return fmt.Errorf("persist batch: %w", perr)
		}
		return nil
	}
	// Start the heartbeat. The goroutine renews the lease while
	// Compute runs so a slow embed pass on a CPU-only provider
	// does not look "abandoned" to the reaper at the 10-minute
	// mark and get its context canceled mid-batch. The heartbeat
	// stops when the deferred cancel fires (Compute returns).
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go w.heartbeat(hbCtx, job)

	rows, err := w.cfg.Computer.Compute(ctx, ComputeRequest{
		Content:      content,
		SpecName:     job.SpecName,
		Existing:     existing,
		BatchSize:    w.cfg.BatchSize,
		Progress:     progress,
		PersistBatch: persistBatch,
	})
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

// heartbeat renews the job's lease at lease/heartbeatDivisor
// cadence while the surrounding embed pass is making forward
// progress. Stops on ctx.Done (the caller's deferred cancel after
// Compute returns) or on the first ErrNotFound from RenewLease
// (which means the lease has rotated to another worker — the
// current worker is no longer in charge and should not keep
// stamping a lease it does not own).
//
// Errors other than ErrNotFound are logged at warn but do not
// stop the heartbeat: a transient DB blip would otherwise let
// the reaper see an unrenewed lease, kill the context, and
// abort the embed pass — exactly the failure mode the heartbeat
// exists to prevent. The next tick re-attempts the renewal.
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
				// Lease rotated. Stop heartbeating; the new
				// holder owns the row.
				slog.Info("embedjobs: heartbeat stopping; lease rotated",
					logKeyJobID, job.ID, "worker_id", w.cfg.WorkerID)
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("embedjobs: lease renewal failed; will retry next tick",
					logKeyJobID, job.ID, "worker_id", w.cfg.WorkerID, logKeyError, err)
			}
		}
	}
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
