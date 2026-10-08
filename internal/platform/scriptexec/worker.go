package scriptexec

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptadmit"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Worker defaults.
const (
	// defaultPollEvery is the fallback poll interval when LISTEN/NOTIFY does
	// not wake the worker. It is short relative to the notification queue's
	// because somebody is usually waiting on a run_script call, and the query
	// it costs is one indexed lookup that returns nothing.
	defaultPollEvery = 5 * time.Second

	// leaseMargin is how far a claim's lease outlives the run timeout. The
	// lease must exceed the longest a run can take or a still-running run
	// would look abandoned and be claimed a second time while the first is
	// mid-flight; the margin is what a crashed worker's run waits before
	// another replica reclaims it. The lease is derived from the configured
	// timeout (LeaseFor) rather than set beside it, so raising one cannot
	// leave the other behind (#1843).
	leaseMargin = 5 * time.Minute

	// shedEvery is how often adaptive admission reads memory to decide whether
	// to stop a run. Memory can climb faster than the queue poll.
	shedEvery = time.Second

	// shedReason is what a run stopped to relieve memory is requeued with.
	shedReason = "the worker was under memory pressure; the run was stopped and requeued for a replica with headroom"

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

	// drainWindowCap bounds how long a stopping worker waits for the run in
	// flight to finish on its own. It is deliberately short against a run's own
	// ceiling: the point is to let a run that is nearly done finish rather than
	// be thrown away and re-executed from the start elsewhere, not to hold a
	// rolling deploy open for the length of a report.
	drainWindowCap = 3 * time.Second

	// drainBudgetShare is the most of the caller's remaining shutdown budget
	// the wait may take. The budget is not the worker's: it belongs to every
	// component the lifecycle stops, and the ones registered before this go
	// after it. Half, capped, leaves them a working share.
	drainBudgetShare = 2

	// releaseReserve is the slice of the shutdown budget held back for the
	// release write, and the bound on that write. Without it a worker that
	// spent the whole budget waiting would be killed still holding the lease,
	// and the run would sit until the lease expired instead of being picked up
	// by the replica that replaced it.
	releaseReserve = 2 * time.Second
)

// ScriptReader is the script lookup the execution side needs, narrowed to the
// one method so nothing here depends on the whole store contract.
type ScriptReader interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
}

// VersionReader is the version lookup the execution side needs. The worker
// reads by id because a run is queued against one — only an id names one
// immutable snapshot for the life of a script — and the scheduler reads the
// script's current version by number to build the run it materializes.
type VersionReader interface {
	GetVersionByID(ctx context.Context, id string) (*script.Version, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
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
// final for the run, because a script that already wrote an output must not be
// re-executed on the chance that its last call was a transient fault; its
// cause says whether running it again is expected to help (#1935). That boundary is deliberate:
// a query engine being unreachable reaches the runner as a tool error
// indistinguishable from bad SQL, and guessing between them by matching strings
// would trade a visible failure for a silent double-write.
type attempt struct {
	result    script.RunResult
	retryable bool
	// requeue is how a retryable attempt that goes back on the queue is
	// recorded in the run's history: retried by default, shed for a run
	// stopped to relieve memory (#1860).
	requeue string
}

// workerConfig is what the run worker needs to drain the queue.
type workerConfig struct {
	runs     script.RunStore
	scripts  ScriptReader
	versions VersionReader
	runner   executor
	notifier Notifier
	// metrics records what the run queue is doing, so an operator watching the
	// platform sees a failing or slowing automation without querying the run
	// table (#1307). Nil is a no-op: every method on *observability.Metrics is
	// nil-safe, and a deployment without observability still executes runs.
	metrics   *observability.Metrics
	retention time.Duration
	// recordings is swept with the runs, at retention (#1939).
	recordings  scriptrec.Store
	pollEvery   time.Duration
	lease       time.Duration
	maxAttempts int
	// maxReclaims is how many times a run is taken over from a worker whose
	// lease expired before it is failed instead (#1860).
	maxReclaims int
	// admission decides whether another run may be claimed (#1843); load is
	// what adaptive admission reads, procload when nil.
	admission scriptadmit.Admission
	load      scriptadmit.LoadSource
}

// LeaseFor is the claim lease for a run timeout: the timeout plus the margin a
// crashed worker's run waits before it is reclaimed.
func LeaseFor(runTimeout time.Duration) time.Duration {
	return runTimeout + leaseMargin
}

// worker claims due runs and executes each on its own goroutine, as many at
// once as its admission allows (#1843).
//
// A run holds a Starlark heap the interpreter cannot cap, so how many execute
// at once is the lever that bounds the memory a replica reaches. Adaptive
// admission, the default, pulls it from the replica's measured memory and CPU
// rather than from a number an operator must size for the worst script: a
// replica full of runs waiting on a query engine keeps claiming, and one near
// its limits stops, leaving the rest queued for itself or another replica
// through SKIP LOCKED. Nothing is refused, so nothing fails for want of room.
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
	// inFlight counts the claimed runs executing. A shutdown waits only for
	// them: time the loop spends in the queue's own calls is not work worth
	// saving, and waiting it out would delay the cancel that unblocks them.
	inFlight atomic.Int32
	admit    scriptadmit.Admitter
	// slots holds each executing run's cancel, keyed by run id, which is how a
	// run is stopped to relieve memory without stopping the others.
	slotsMu sync.Mutex
	slots   map[string]*slot
	// lastPurge throttles the retention sweep and lastAbandoned the sweep of
	// runs whose reclaims are spent. Only the loop goroutine reads them.
	lastPurge     time.Time
	lastAbandoned time.Time
	// queueHadWork records whether the last claim found a run, so a refusal
	// is counted only when there was work to refuse.
	queueHadWork bool
}

// slot is one executing run.
type slot struct {
	cancel  context.CancelFunc
	started time.Time
	// shed is set when the worker stopped the run to relieve memory, which
	// turns its outcome into a requeue rather than a failure.
	shed atomic.Bool
	// overMemory holds why the worker stopped the run as the only one
	// executing past the shed threshold (#1861), which turns its outcome into
	// a memory failure: there is no smaller replica to requeue it for.
	overMemory atomic.Pointer[string]
}

// newWorker creates a run worker, applying defaults for zero config values.
func newWorker(cfg workerConfig) *worker {
	if cfg.pollEvery <= 0 {
		cfg.pollEvery = defaultPollEvery
	}
	if cfg.lease <= 0 {
		cfg.lease = LeaseFor(scriptrun.RunTimeout)
	}
	if cfg.maxAttempts <= 0 {
		cfg.maxAttempts = defaultMaxAttempts
	}
	if cfg.maxReclaims <= 0 {
		cfg.maxReclaims = runstate.DefaultMaxReclaims
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
		admit:     scriptadmit.NewAdmitter(cfg.admission, cfg.load),
		slots:     map[string]*slot{},
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

// Stop drains the worker: claiming stops at once, a run in flight is given a
// drain window to finish, and what the window does not cover is canceled and
// released.
//
// Both halves earn their place in a rolling deploy. Waiting is what keeps a run
// seconds from done from being discarded and re-executed from the start on
// another replica. Bounding the wait is what keeps a run with most of a
// ten-minute budget left from holding the pod open past its termination grace
// period; a run canceled that way is not failed, because a shutdown decides
// nothing about it, so it goes straight back on the queue rather than sitting
// there until its lease expires. The outputs it already wrote are recorded on
// its row, so the replica that picks it up does not write them twice.
//
// The wait happens only when a run is actually executing. A loop sitting in the
// queue's own calls has nothing worth saving, and waiting it out would only
// delay the cancel that unblocks it. What follows the wait is bounded too: the
// cancel ends the interpreter, and the write that records the outcome is
// bounded by shutdownWrite.
func (w *worker) Stop(ctx context.Context) {
	w.stopOnce.Do(func() { close(w.stopCh) })
	if w.inFlight.Load() > 0 && !w.awaitIdle(drainWindow(ctx)) {
		slog.InfoContext(ctx, "scripts: the drain window is spent; canceling and releasing the runs still executing")
	}
	w.cancelRun()
	w.wg.Wait()
}

// awaitIdle waits up to d for the worker loop to exit, reporting whether it
// did. A worker that never started, or whose run finished inside the window,
// returns immediately; an exhausted budget waits for nothing.
func (w *worker) awaitIdle(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	idle := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(idle)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-idle:
		return true
	case <-timer.C:
		return false
	}
}

// shutdownWrite builds the context for a store write made while the worker is
// stopping. It survives the run's cancellation, because the one thing a
// shutting-down worker must still manage is recording what happened to the run
// it was holding — and it is bounded by the reserve the drain window held back,
// because a worker blocked on an unreachable database would otherwise spend the
// pod's whole termination grace period on a write whose only alternative,
// letting the lease expire, costs one reap interval.
func shutdownWrite(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), releaseReserve)
}

// drainWindow is how much of the caller's shutdown budget the run in flight may
// spend finishing. Deriving it from the deadline is what aligns the worker with
// the HTTP drain that precedes it: the worker never spends time the process
// does not have, never returns so late that the release write is cut off, and
// never takes more than its share of a budget the whole lifecycle is spending.
// A caller with no deadline — a stdio session, a test — gets the cap.
func drainWindow(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return drainWindowCap
	}
	remaining := time.Until(deadline)
	return min(remaining/drainBudgetShare, remaining-releaseReserve, drainWindowCap)
}

// run is the poll/wakeup loop. A run finishing wakes it, since a freed slot
// may admit the next one; adaptive admission also reads memory every
// shedEvery, to stop a run before the container is killed with all of them.
//
// The memory reading is its own loop beside the poll, on the same goroutine
// count as before plus one: a poll that is draining does not delay a shed, and
// each reading is also exported as the admission's load.
func (w *worker) run() {
	defer w.wg.Done()
	w.wg.Go(func() {
		bgloop.Run(w.runCtx, bgloop.Loop{
			Name: bgloop.NameScriptShed, Every: shedEvery, Stop: w.stopCh, SpanPerUnit: true,
			Body: func(ctx context.Context) error {
				w.recordLoad(ctx)
				w.maybeShed(ctx)
				return nil
			},
		})
	})
	bgloop.Run(w.runCtx, bgloop.Loop{
		Name: bgloop.NameScriptWorker, Every: w.cfg.pollEvery, Immediate: true,
		Wake: w.wakeup, Stop: w.stopCh, SpanPerUnit: true,
		Body: w.drain,
	})
}

// recordLoad exports the load admission decides on, as a share of each limit
// the platform can measure.
func (w *worker) recordLoad(ctx context.Context) {
	s := w.admit.Load()
	if s.MemoryKnown {
		w.cfg.metrics.RecordScriptWorkerLoad(ctx, scriptadmit.RefusedMemory, s.MemoryPercent/percentScale)
	}
	if s.CPUKnown {
		w.cfg.metrics.RecordScriptWorkerLoad(ctx, scriptadmit.RefusedCPU, s.CPUPercent/percentScale)
	}
}

// percentScale turns procload's percentages into the 0-1 share the load gauge
// reports.
const percentScale = 100

// drain claims due runs while admission allows, until none remain or the
// worker stops. Each claimed run executes on its own goroutine.
func (w *worker) drain(ctx context.Context) error {
	w.maybePurge(ctx)
	w.maybeFailAbandoned(ctx)
	for !bgloop.Stopped(ctx, w.stopCh) {
		if ok, reason := w.admit.Admit(int(w.inFlight.Load())); !ok {
			if w.queueHadWork {
				w.cfg.metrics.RecordScriptAdmissionRefused(ctx, reason)
			}
			return nil
		}
		more, err := w.processNext(ctx)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
	return nil
}

// maybePurge runs the retention sweep at most once per purgeEvery.
func (w *worker) maybePurge(ctx context.Context) {
	if time.Since(w.lastPurge) < purgeEvery {
		return
	}
	w.lastPurge = time.Now()
	purged, err := w.cfg.runs.PurgeRuns(ctx, w.cfg.retention)
	if err != nil {
		slog.WarnContext(ctx, "scripts: run retention sweep failed", logKeyError, err)
		return
	}
	bgloop.Purged(ctx, bgloop.NameScriptWorker, purged)
	if purged > 0 {
		slog.InfoContext(ctx, "scripts: run retention sweep", "rows", purged, "retention", w.cfg.retention)
	}
	w.purgeRecordings(ctx)
}

// purgeRecordings sweeps the recordings past retention that no test keeps.
func (w *worker) purgeRecordings(ctx context.Context) {
	if w.cfg.recordings == nil {
		return
	}
	purged, err := w.cfg.recordings.Purge(ctx, w.cfg.retention)
	if err != nil {
		slog.WarnContext(ctx, "scripts: recording retention sweep failed", logKeyError, err)
		return
	}
	if purged > 0 {
		slog.InfoContext(ctx, "scripts: recording retention sweep", "rows", purged, "retention", w.cfg.retention)
	}
}

// maybeFailAbandoned fails the runs whose workers kept dying, at most once per
// poll interval (#1860).
//
// A run whose lease expired with its reclaims spent is never claimed again, so
// nothing would ever end it; any worker's loop does, which is what lets the
// cap hold with no survivor of the run's own workers. Each one is counted and
// its owner alerted, as a run failed any other way is.
func (w *worker) maybeFailAbandoned(ctx context.Context) {
	if time.Since(w.lastAbandoned) < w.cfg.pollEvery {
		return
	}
	w.lastAbandoned = time.Now()
	failed, err := w.cfg.runs.FailAbandoned(ctx, w.cfg.maxReclaims)
	if err != nil {
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "scripts: failing runs whose workers stopped failed", logKeyError, err)
		}
		return
	}
	for i := range failed {
		run := &failed[i]
		slog.WarnContext(ctx, "scripts: failed a run whose workers kept stopping without a result", // #nosec G706 -- structured slog call; error sanitized
			logKeyRunID, run.ID, "reclaims", run.Reclaims, logKeyError, logsan.SanitizeForLog(run.Error))
		w.cfg.metrics.RecordScriptRunReclaim(ctx, observability.ReclaimFailed)
		sc, readErr := w.cfg.scripts.GetByID(ctx, run.ScriptID)
		if readErr != nil {
			slog.WarnContext(ctx, "scripts: reading the script of an abandoned run failed", logKeyRunID, run.ID, logKeyError, readErr)
		}
		result := script.RunResult{Status: script.RunStatusFailed, Error: run.Error, Log: run.Log, Cause: runstate.CauseWorkerLost}
		w.cfg.metrics.RecordScriptRun(ctx, observability.ScriptRunAttrs{
			Script: scriptName(sc, run), Trigger: run.Trigger, Status: script.RunStatusFailed,
		}, 0)
		w.cfg.metrics.RecordScriptRunFailure(ctx, runstate.CauseWorkerLost)
		w.notifyFailure(ctx, run, sc, result)
	}
}

// processNext claims and executes one run, reporting whether more may remain
// and a claim that failed.
func (w *worker) processNext(ctx context.Context) (bool, error) {
	run, err := w.cfg.runs.Claim(ctx, w.id, w.cfg.lease, w.cfg.maxReclaims)
	w.queueHadWork = err == nil
	if errors.Is(err, script.ErrNoWork) {
		return false, nil
	}
	if err != nil {
		// A claim that failed because the worker is shutting down is not a fault
		// worth reporting; every stop would log one.
		if ctx.Err() != nil {
			return false, nil
		}
		slog.WarnContext(ctx, "scripts: claiming a run failed", logKeyError, err)
		return false, fmt.Errorf("scripts: claiming a run: %w", err)
	}
	// A claim in flight when the stop landed hands back work this worker will
	// not finish: the drain window belongs to the run already executing, not to
	// one that has not started. Give it straight back rather than spend the
	// window on it.
	select {
	case <-w.stopCh:
		releaseCtx, cancel := shutdownWrite(ctx)
		defer cancel()
		w.release(releaseCtx, run)
		return false, nil
	default:
	}
	if run.Reclaimed {
		// The worker that held it stopped without reporting a result.
		slog.WarnContext(ctx, "scripts: took over a run whose worker stopped without a result",
			logKeyRunID, run.ID, logKeyAttempt, run.Attempt, "reclaims", run.Reclaims)
		w.cfg.metrics.RecordScriptRunReclaim(ctx, observability.ReclaimReexecuted)
	}
	w.cfg.metrics.RecordScriptQueueWait(ctx, queueWait(run))
	w.launch(run)
	return true, nil
}

// queueWait is how long a claimed run waited after it became due.
func queueWait(run *script.Run) time.Duration {
	if run.ScheduledFor.IsZero() {
		return 0
	}
	claimed := time.Now()
	if run.StartedAt != nil {
		claimed = *run.StartedAt
	}
	return claimed.Sub(run.ScheduledFor)
}

// launch executes a claimed run on its own goroutine under a context of its
// own, derived from the worker's so a shutdown still reaches it.
func (w *worker) launch(run *script.Run) {
	ctx, cancel := context.WithCancel(w.runCtx)
	s := &slot{cancel: cancel, started: time.Now()}
	w.slotsMu.Lock()
	w.slots[run.ID] = s
	w.slotsMu.Unlock()
	w.inFlight.Add(1)
	w.wg.Go(func() {
		defer w.Notify()
		defer func() {
			w.slotsMu.Lock()
			delete(w.slots, run.ID)
			w.slotsMu.Unlock()
			cancel()
			w.inFlight.Add(-1)
		}()
		// The run is a unit of background work with its own root span, which
		// every tool call it makes is a child of (scriptsession carries it).
		_ = bgloop.Unit(ctx, bgloop.NameScriptRun, func(ctx context.Context) error {
			trace.SpanFromContext(ctx).SetAttributes(attribute.String(spanAttrRunID, run.ID))
			return w.processRun(ctx, run, s)
		})
	})
}

// maybeShed stops the most recently started run when memory is past the shed
// threshold, one per call, so memory has a reading to fall before the next.
// The newest run is chosen because it has the least work to lose.
//
// A run already stopped is not counted: it is on its way out, and counting it
// would shed the last run still executing.
//
// The last run is not requeued: a run over the line on its own is that
// script's own size, and would rebuild the same heap wherever it ran next. It
// is failed instead (#1861), because the alternative is the kernel killing the
// replica with every session on it -- and a failed run is recoverable where a
// killed replica, whose run is then reclaimed onto the next one, is not.
func (w *worker) maybeShed(ctx context.Context) {
	w.slotsMu.Lock()
	defer w.slotsMu.Unlock()
	var (
		newest   *slot
		newestID string
		live     int
	)
	for id, s := range w.slots {
		if s.shed.Load() || s.overMemory.Load() != nil {
			continue
		}
		live++
		if newest == nil || s.started.After(newest.started) {
			newest, newestID = s, id
		}
	}
	if newest == nil {
		return
	}
	if live == 1 {
		if over, reason := w.admit.OverShed(); over {
			slog.WarnContext(ctx, "scripts: stopping the only run executing; the replica is out of memory", logKeyRunID, newestID)
			newest.overMemory.Store(&reason)
			newest.cancel()
		}
		return
	}
	if !w.admit.Shed(live) {
		return
	}
	slog.WarnContext(ctx, "scripts: stopping a run to relieve memory", logKeyRunID, newestID)
	w.cfg.metrics.RecordScriptRunShed(ctx)
	newest.shed.Store(true)
	newest.cancel()
}

// processRun loads what the claimed run needs and executes it, then resolves
// the run to a terminal state or back onto the queue. ctx is the run's own. A
// run recorded as failed is counted by its cause and returned as an error,
// so the run's unit is counted as one that failed.
func (w *worker) processRun(ctx context.Context, run *script.Run, s *slot) error {
	// Bracketing the execution rather than counting it at the end: a run that
	// never finishes never records a terminal observation, and a worker wedged
	// on one is exactly what this gauge exists to show.
	w.cfg.metrics.ScriptRunStarted(ctx)
	defer w.cfg.metrics.ScriptRunFinished(ctx)
	started := time.Now()
	slog.InfoContext(ctx, "scripts: running", logKeyRunID, run.ID,
		"script_id", logsan.SanitizeForLog(run.ScriptID), "version", run.Version, logKeyAttempt, run.Attempt)
	sc, v, loadErr := w.load(ctx, run)
	var outcome attempt
	switch {
	case loadErr != nil:
		outcome = *loadErr
	case run.CancelRequestedAt != nil:
		// A cancel requested before this claim -- the worker that held the
		// run stopped before acting on it -- ends the run here, unexecuted.
		outcome = attempt{result: script.RunResult{
			Status: script.RunStatusCanceled, Error: cancelledError(run.CancelRequestedBy),
		}}
	default:
		outcome = w.cfg.runner.execute(ctx, run, sc, v)
	}
	// A run stopped to relieve memory has not failed: it goes back on the
	// queue, spending the platform's retry budget rather than the script's.
	// One stopped as the only run executing past the threshold has nowhere
	// smaller to go, and fails on memory. Either that reported success as the
	// cancel landed finished.
	if outcome.result.Status != script.RunStatusSucceeded {
		if reason := s.overMemory.Load(); reason != nil {
			outcome.result.Status, outcome.result.Error, outcome.result.Cause = script.RunStatusFailed, *reason, runstate.CauseMemory
			outcome.retryable = false
		} else if s.shed.Load() {
			outcome = attempt{
				result:    script.RunResult{Status: script.RunStatusFailed, Error: shedReason, Cause: runstate.CauseMemory},
				retryable: true, requeue: runstate.AttemptShed,
			}
		}
	}
	// The alert is raised only for a run that was actually recorded as failed.
	// A run released by a shutdown, or one returned to the queue for a retry,
	// has not failed — mailing about either would report an outcome the run has
	// not reached.
	if !w.resolve(run, outcome) {
		return nil
	}
	w.cfg.metrics.RecordScriptRun(ctx, observability.ScriptRunAttrs{
		Script: scriptName(sc, run), Trigger: run.Trigger, Status: outcome.result.Status,
	}, time.Since(started))
	w.notifyFailure(ctx, run, sc, outcome.result)
	if outcome.result.Status != script.RunStatusFailed {
		return nil
	}
	w.cfg.metrics.RecordScriptRunFailure(ctx, outcome.result.Cause)
	return fmt.Errorf("scripts: run %s failed (%s)", run.ID, outcome.result.Cause)
}

// scriptName labels an observation with the script's name, falling back to its
// id when the run could not load the script — which is itself a terminal
// outcome worth counting rather than dropping.
func scriptName(sc *script.Script, run *script.Run) string {
	if sc != nil {
		return sc.Name
	}
	return run.ScriptID
}

// load reads the script and the version the run names, and puts the script
// back through the run gate (script.RefuseRun) before anything executes.
//
// Whatever it managed to read is returned alongside the refusal, not discarded
// with it. A run refused at the gate still belongs to a script with an owner,
// and that owner is who has to hear that their scheduled report is no longer
// running.
func (w *worker) load(ctx context.Context, run *script.Run) (*script.Script, *script.Version, *attempt) {
	sc, err := w.cfg.scripts.GetByID(ctx, run.ScriptID)
	if err != nil {
		return nil, nil, retryable("reading the script failed: " + err.Error())
	}
	if sc == nil {
		return nil, nil, terminal("the script this run belongs to no longer exists", runstate.CausePlatform)
	}
	v, err := w.cfg.versions.GetVersionByID(ctx, run.VersionID)
	if err != nil {
		return sc, nil, retryable("reading the script version failed: " + err.Error())
	}
	if v == nil {
		return sc, nil, terminal("the version this run was queued against no longer exists", runstate.CausePlatform)
	}
	if refusal := script.RefuseRun(sc); refusal != nil {
		return sc, v, terminal(refusal.Error(), runstate.CauseScript)
	}
	return sc, v, nil
}

// terminal builds a failed, non-retryable attempt with the cause it is
// recorded under.
func terminal(reason, cause string) *attempt {
	return &attempt{result: script.RunResult{Status: script.RunStatusFailed, Error: reason, Cause: cause}}
}

// retryable builds a failed attempt the worker should try again: a platform
// fault, recorded as one if the attempt budget runs out first.
func retryable(reason string) *attempt {
	return &attempt{
		result:    script.RunResult{Status: script.RunStatusFailed, Error: reason, Cause: runstate.CausePlatform},
		retryable: true, requeue: runstate.AttemptRetried,
	}
}

// resolve writes the attempt's outcome: a terminal result, or a return to the
// queue when the failure was the platform's own and the attempt budget allows.
//
// Every write here uses a context of its own rather than the run's, because the
// run's context is what a shutdown cancels — and the one thing a shutting-down
// worker must still manage is recording what happened to the run it was holding.
//
// A shutdown releases the run rather than recording a verdict on it, with one
// exception: a run whose interpreter reported success finished, and the cancel
// that raced it decided nothing. Releasing that one would re-execute a script
// that had already done its work. Either way the write goes through
// shutdownWrite, which bounds it.
//
// It reports whether the run reached a terminal state HERE — which is what
// entitles the caller to tell somebody about the outcome. A released or retried
// run has not finished, and a Finish whose lease was lost was decided by
// another worker, which will report it.
func (w *worker) resolve(run *script.Run, a attempt) bool {
	ctx := context.WithoutCancel(w.runCtx)
	if w.runCtx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = shutdownWrite(w.runCtx)
		defer cancel()
		if a.result.Status != script.RunStatusSucceeded {
			w.release(ctx, run)
			return false
		}
	}
	if a.retryable && run.Attempt < w.cfg.maxAttempts {
		backoff := computeBackoff(run.Attempt)
		slog.Warn("scripts: run failed on a platform fault; retrying",
			logKeyRunID, run.ID, logKeyAttempt, run.Attempt, "backoff", backoff, logKeyError, a.result.Error)
		outcome := cmp.Or(a.requeue, runstate.AttemptRetried)
		if err := w.cfg.runs.Retry(ctx, run.Lease(), outcome, a.result.Error, backoff); err != nil {
			logLeaseAware("scripts: returning a run to the queue failed", run, err)
		}
		return false
	}
	if err := w.cfg.runs.Finish(ctx, run.Lease(), a.result); err != nil {
		logLeaseAware("scripts: recording a run result failed", run, err)
		return false
	}
	slog.Info("scripts: run finished", logKeyRunID, run.ID, "status", a.result.Status,
		"steps", a.result.Metrics.Steps, "duration_ms", a.result.Metrics.DurationMS)
	return true
}

// release hands a run back to the queue because this worker is shutting down,
// not because anything about the run failed. It bypasses the attempt budget for
// the same reason: a restart is not an attempt at the work. Its caller bounds
// the write.
func (w *worker) release(ctx context.Context, run *script.Run) {
	slog.InfoContext(ctx, "scripts: releasing a run at shutdown", logKeyRunID, run.ID, logKeyAttempt, run.Attempt)
	if err := w.cfg.runs.Retry(ctx, run.Lease(), runstate.AttemptReleased,
		"the worker executing this run shut down; it was requeued", 0); err != nil {
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
