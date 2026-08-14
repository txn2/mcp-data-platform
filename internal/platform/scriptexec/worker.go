package scriptexec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Worker defaults.
const (
	// defaultPollEvery is the fallback poll interval when LISTEN/NOTIFY does
	// not wake the worker. It is short relative to the notification queue's
	// because somebody is usually waiting on a run_script call, and the query
	// it costs is one indexed lookup that returns nothing.
	defaultPollEvery = 5 * time.Second

	// defaultLease bounds one attempt. It must exceed the longest a run can
	// take (scriptrun.ApprovedTimeout) or a still-running run would look
	// abandoned and be claimed a second time while the first is mid-flight; the
	// margin above that ceiling is what a crashed worker's run waits before
	// another replica reclaims it.
	defaultLease = 15 * time.Minute

	// defaultMaxAttempts bounds infrastructure retries per run. Script failures
	// never retry at all, so this budget only ever spends itself on the
	// platform's own faults.
	defaultMaxAttempts = 3

	// retryBackoffBase seeds the exponential retry backoff, and maxBackoffShift
	// caps its doubling (15s * 2^4 = 4m).
	retryBackoffBase = 15 * time.Second
	maxBackoffShift  = 4

	// purgeEvery throttles the run-retention sweep.
	purgeEvery = time.Hour
)

// ScriptReader is the script lookup the execution side needs, narrowed to the
// one method so nothing here depends on the whole store contract.
type ScriptReader interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
}

// VersionReader is the version lookup the execution side needs. It reads by id
// because the execution gate stores an id: only an id names one immutable
// snapshot for the life of a script.
type VersionReader interface {
	GetVersionByID(ctx context.Context, id string) (*script.Version, error)
}

// executor runs one claimed run and reports how it ended.
type executor interface {
	execute(ctx context.Context, run *script.Run, sc *script.Script, v *script.Version) attempt
}

// attempt is the outcome of one execution attempt.
//
// The retryable flag is decided by WHERE the failure happened, never by reading
// an error message. Everything outside the interpreter — opening the run's
// session, reading the script or its version — is the platform's own fault and
// is retried. Everything the interpreter reports is the script's outcome and is
// final, because a Starlark error on the same inputs reproduces exactly, and
// because a script that already wrote an output must not be re-executed on the
// chance that its last call was a transient fault. That boundary is deliberate:
// a query engine being unreachable reaches the runner as a tool error
// indistinguishable from bad SQL, and guessing between them by matching strings
// would trade a visible failure for a silent double-write.
type attempt struct {
	result    script.RunResult
	retryable bool
}

// workerConfig is what the run worker needs to drain the queue.
type workerConfig struct {
	runs        script.RunStore
	scripts     ScriptReader
	versions    VersionReader
	runner      executor
	retention   time.Duration
	pollEvery   time.Duration
	lease       time.Duration
	maxAttempts int
}

// worker claims due runs and executes them, one at a time.
//
// One at a time is deliberate. A run holds a Starlark heap the interpreter
// cannot cap, so the number of concurrent runs per replica is the one lever
// that bounds the memory a pathological script can reach; SKIP LOCKED spreads
// load across replicas rather than across goroutines here.
type worker struct {
	cfg    workerConfig
	id     string
	wakeup chan struct{}
	stopCh chan struct{}
	// runCtx is canceled by Stop, which is what keeps shutdown from waiting
	// out a run that may have ten minutes left on its clock.
	runCtx    context.Context //nolint:containedctx // the lifetime of the worker's execution, canceled by Stop
	cancelRun context.CancelFunc
	stopOnce  sync.Once
	wg        sync.WaitGroup
	started   atomic.Bool
	// lastPurge throttles the retention sweep. Only the run goroutine reads it.
	lastPurge time.Time
}

// newWorker creates a run worker, applying defaults for zero config values.
func newWorker(cfg workerConfig) *worker {
	if cfg.pollEvery <= 0 {
		cfg.pollEvery = defaultPollEvery
	}
	if cfg.lease <= 0 {
		cfg.lease = defaultLease
	}
	if cfg.maxAttempts <= 0 {
		cfg.maxAttempts = defaultMaxAttempts
	}
	if cfg.retention <= 0 {
		cfg.retention = DefaultRunRetention
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	return &worker{
		cfg:       cfg,
		id:        workerID(),
		wakeup:    make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
		runCtx:    runCtx,
		cancelRun: cancelRun,
	}
}

// workerID labels this replica's claims. It only has to distinguish concurrent
// workers from one another, which the run id of its own first claim cannot do,
// so it is a process-scoped random token generated the same way run ids are.
func workerID() string {
	id, err := generateWorkerToken()
	if err != nil {
		// A worker that cannot name itself would fence every one of its own
		// writes against a colliding name. Falling back to the process start
		// time is weaker than random but still distinguishes replicas that did
		// not start in the same nanosecond.
		return fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	return id
}

// Notify wakes the worker without waiting for the next poll tick. Safe from any
// goroutine; a flurry of calls coalesces into one wakeup.
func (w *worker) Notify() {
	select {
	case w.wakeup <- struct{}{}:
	default:
	}
}

// Start launches the worker loop. Idempotent.
func (w *worker) Start(_ context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.run()
}

// Stop terminates the worker loop and waits for the run in flight.
//
// The run in flight is CANCELED rather than waited out: an approved run may
// have most of a ten-minute budget left, and a shutdown that waits for it turns
// a rolling restart into a stall. A run canceled this way is not failed — a
// shutdown decided nothing about it — so it is released back to the queue for
// another replica to pick up immediately, and the outputs it already wrote are
// on its row, so re-running it does not write them twice.
func (w *worker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.cancelRun()
	})
	w.wg.Wait()
}

// run is the poll/wakeup loop.
func (w *worker) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(w.cfg.pollEvery)
	defer ticker.Stop()
	for {
		w.drain()
		select {
		case <-w.stopCh:
			return
		case <-w.wakeup:
		case <-ticker.C:
		}
	}
}

// drain executes due runs until none remain or the worker stops.
func (w *worker) drain() {
	ctx := w.runCtx
	w.maybePurge(ctx)
	for {
		select {
		case <-w.stopCh:
			return
		default:
		}
		if !w.processNext(ctx) {
			return
		}
	}
}

// maybePurge runs the retention sweep at most once per purgeEvery.
func (w *worker) maybePurge(ctx context.Context) {
	if time.Since(w.lastPurge) < purgeEvery {
		return
	}
	w.lastPurge = time.Now()
	purged, err := w.cfg.runs.PurgeRuns(ctx, w.cfg.retention)
	if err != nil {
		slog.Warn("scripts: run retention sweep failed", logKeyError, err)
		return
	}
	if purged > 0 {
		slog.Info("scripts: run retention sweep", "rows", purged, "retention", w.cfg.retention)
	}
}

// processNext claims and executes one run, reporting whether more may remain.
func (w *worker) processNext(ctx context.Context) bool {
	run, err := w.cfg.runs.Claim(ctx, w.id, w.cfg.lease)
	if errors.Is(err, script.ErrNoWork) {
		return false
	}
	if err != nil {
		// A claim that failed because the worker is shutting down is not a fault
		// worth reporting; every stop would log one.
		if ctx.Err() == nil {
			slog.Warn("scripts: claiming a run failed", logKeyError, err)
		}
		return false
	}
	w.processRun(ctx, run)
	return true
}

// processRun loads what the claimed run needs and executes it, then resolves
// the run to a terminal state or back onto the queue.
func (w *worker) processRun(ctx context.Context, run *script.Run) {
	slog.Info("scripts: running", logKeyRunID, run.ID,
		"script_id", logsan.SanitizeForLog(run.ScriptID), "version", run.Version, "attempt", run.Attempt)
	sc, v, loadErr := w.load(ctx, run)
	if loadErr != nil {
		w.resolve(ctx, run, *loadErr)
		return
	}
	w.resolve(ctx, run, w.cfg.runner.execute(ctx, run, sc, v))
}

// load reads the script and the version the run names, and puts both back
// through the execution gate (script.RefuseRun) before anything executes.
func (w *worker) load(ctx context.Context, run *script.Run) (*script.Script, *script.Version, *attempt) {
	sc, err := w.cfg.scripts.GetByID(ctx, run.ScriptID)
	if err != nil {
		return nil, nil, retryable("reading the script failed: " + err.Error())
	}
	if sc == nil {
		return nil, nil, terminal("the script this run belongs to no longer exists")
	}
	v, err := w.cfg.versions.GetVersionByID(ctx, run.VersionID)
	if err != nil {
		return nil, nil, retryable("reading the script version failed: " + err.Error())
	}
	if v == nil {
		return nil, nil, terminal("the version this run was queued against no longer exists")
	}
	if refusal := script.RefuseRun(sc, v, run); refusal != nil {
		return nil, nil, terminal(refusal.Error())
	}
	return sc, v, nil
}

// terminal builds a failed, non-retryable attempt.
func terminal(reason string) *attempt {
	return &attempt{result: script.RunResult{Status: script.RunStatusFailed, Error: reason}}
}

// retryable builds a failed attempt the worker should try again.
func retryable(reason string) *attempt {
	return &attempt{result: script.RunResult{Status: script.RunStatusFailed, Error: reason}, retryable: true}
}

// resolve writes the attempt's outcome: a terminal result, or a return to the
// queue when the failure was the platform's own and the attempt budget allows.
//
// Every write here uses a context of its own rather than the run's, because the
// run's context is what a shutdown cancels — and the one thing a shutting-down
// worker must still manage is recording what happened to the run it was holding.
func (w *worker) resolve(runCtx context.Context, run *script.Run, a attempt) {
	ctx := context.WithoutCancel(runCtx)
	if runCtx.Err() != nil {
		w.release(ctx, run)
		return
	}
	if a.retryable && run.Attempt < w.cfg.maxAttempts {
		backoff := computeBackoff(run.Attempt)
		slog.Warn("scripts: run failed on a platform fault; retrying",
			logKeyRunID, run.ID, "attempt", run.Attempt, "backoff", backoff, logKeyError, a.result.Error)
		if err := w.cfg.runs.Retry(ctx, run.Lease(), a.result.Error, backoff); err != nil {
			logLeaseAware("scripts: returning a run to the queue failed", run, err)
		}
		return
	}
	if err := w.cfg.runs.Finish(ctx, run.Lease(), a.result); err != nil {
		logLeaseAware("scripts: recording a run result failed", run, err)
		return
	}
	slog.Info("scripts: run finished", logKeyRunID, run.ID, "status", a.result.Status,
		"steps", a.result.Metrics.Steps, "duration_ms", a.result.Metrics.DurationMS)
}

// release hands a run back to the queue because this worker is shutting down,
// not because anything about the run failed. It bypasses the attempt budget for
// the same reason: a restart is not an attempt at the work.
func (w *worker) release(ctx context.Context, run *script.Run) {
	slog.Info("scripts: releasing a run at shutdown", logKeyRunID, run.ID, "attempt", run.Attempt)
	if err := w.cfg.runs.Retry(ctx, run.Lease(), "the worker executing this run shut down; it was requeued", 0); err != nil {
		// Nothing more to do: the lease expires on its own and another replica
		// reclaims the run, which is the slower path to the same place.
		logLeaseAware("scripts: releasing a run at shutdown failed", run, err)
	}
}

// logLeaseAware reports a failed run write, distinguishing the expected case —
// this worker's lease expired and another replica took the run over — from a
// real store failure, so a slow replica does not fill the log with errors for
// work that was correctly picked up elsewhere.
func logLeaseAware(msg string, run *script.Run, err error) {
	if errors.Is(err, script.ErrLeaseLost) {
		slog.Warn(msg+": the run was reclaimed by another worker", logKeyRunID, run.ID)
		return
	}
	slog.Error(msg, logKeyRunID, run.ID, logKeyError, err)
}

// computeBackoff returns retryBackoffBase * 2^(attempt-1), capped.
func computeBackoff(attempt int) time.Duration {
	shift := min(max(attempt-1, 0), maxBackoffShift)
	return retryBackoffBase * (1 << shift)
}
