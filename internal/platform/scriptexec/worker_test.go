package scriptexec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeRuns is an in-memory run queue that models the real store's contract:
// claims hand out one run at a time under a lease, and every write is fenced on
// the lease it was taken under, so a stale worker's write is refused rather
// than applied. A fake that skipped the fencing would let a worker test pass
// while the real store rejected the same sequence.
type fakeRuns struct {
	mu sync.Mutex
	// queue holds the rows; dueAt models scheduled_for, so a run returned to
	// the queue with a backoff is not immediately claimable again. Without
	// that the fake would let a retry storm look like an orderly retry.
	queue    []*script.Run
	dueAt    map[string]time.Time
	finished []script.RunResult
	retried  []string
	outputs  []script.RunOutput
	// claims counts every claim attempt, which is how a replica that must not
	// claim is held to it.
	claims   int
	claimErr error
	writeErr error
	// blockWrites models a database that accepts the statement and never
	// answers, which is what makes an unbounded write at shutdown dangerous.
	blockWrites bool
	purged      int64
	purgeErr    error
	// reports are the live snapshots a running run wrote (#1847).
	reports []script.RunLive
	// ends are how each requeued attempt ended (#1860).
	ends []attemptEnd
}

func (f *fakeRuns) Enqueue(_ context.Context, r *script.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Status = script.RunStatusPending
	f.queue = append(f.queue, r)
	return nil
}

// due reports whether a pending run's backoff has elapsed.
func (f *fakeRuns) due(r *script.Run) bool {
	when, ok := f.dueAt[r.ID]
	return !ok || !time.Now().Before(when)
}

func (f *fakeRuns) GetRun(_ context.Context, id string) (*script.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.queue {
		if r.ID == id {
			out := *r
			return &out, nil
		}
	}
	return nil, script.ErrRunNotFound
}

func (*fakeRuns) ListRuns(context.Context, script.RunFilter) ([]script.Run, error) {
	return nil, nil
}

// Claim models the real store: a due pending run, or a running one whose
// lease expired while its reclaims are under the cap, which counts the
// reclaim and records the dead attempt.
func (f *fakeRuns) Claim(_ context.Context, worker string, lease time.Duration, maxReclaims int) (*script.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	now := time.Now()
	for _, r := range f.queue {
		reclaim := r.Status == script.RunStatusRunning && leaseExpired(r, now) && r.Reclaims < maxReclaims
		if !reclaim && (r.Status != script.RunStatusPending || !f.due(r)) {
			continue
		}
		if reclaim {
			r.Reclaims++
			r.Attempts = append(r.Attempts, runstate.Attempt{
				Attempt: r.Attempt, Worker: r.LockedBy, EndedAt: r.LockedUntil, Outcome: runstate.AttemptLeaseExpired,
			})
		}
		until := now.Add(lease)
		r.Status, r.LockedBy, r.LockedUntil = script.RunStatusRunning, worker, &until
		r.Attempt++
		out := *r
		out.Reclaimed = reclaim
		return &out, nil
	}
	return nil, script.ErrNoWork
}

// leaseExpired reports whether a running row's lease has run out.
func leaseExpired(r *script.Run, now time.Time) bool {
	return r.LockedUntil != nil && r.LockedUntil.Before(now)
}

// FailAbandoned models the real store: running rows whose lease expired with
// their reclaims spent are failed and returned.
func (f *fakeRuns) FailAbandoned(_ context.Context, maxReclaims int) ([]script.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []script.Run{}
	now := time.Now()
	for _, r := range f.queue {
		if r.Status != script.RunStatusRunning || !leaseExpired(r, now) || r.Reclaims < maxReclaims {
			continue
		}
		r.Status, r.Cause = script.RunStatusFailed, runstate.CauseWorkerLost
		r.Error = fmt.Sprintf("the worker executing this run stopped without reporting a result %d times (last held by %s)",
			r.Reclaims+1, r.LockedBy)
		r.LockedUntil, r.LockedBy = nil, ""
		out = append(out, *r)
	}
	return out, nil
}

// held reports whether the lease still matches the stored row.
func (f *fakeRuns) held(lease script.RunLease) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	for _, r := range f.queue {
		if r.ID == lease.RunID && r.LockedBy == lease.Worker && r.Attempt == lease.Attempt {
			return nil
		}
	}
	return script.ErrLeaseLost
}

func (f *fakeRuns) RecordOutput(_ context.Context, lease script.RunLease, out script.RunOutput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.held(lease); err != nil {
		return err
	}
	f.outputs = append(f.outputs, out)
	return nil
}

func (f *fakeRuns) Finish(_ context.Context, lease script.RunLease, res script.RunResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.held(lease); err != nil {
		return err
	}
	for _, r := range f.queue {
		if r.ID == lease.RunID {
			r.Status = res.Status
		}
	}
	f.finished = append(f.finished, res)
	return nil
}

// attemptEnd is how one requeued attempt ended, as Retry recorded it.
type attemptEnd struct{ outcome, reason string }

func (f *fakeRuns) Retry(ctx context.Context, lease script.RunLease, outcome, reason string, backoff time.Duration) error {
	cause := reason
	if f.blocked() {
		<-ctx.Done()
		return fmt.Errorf("the database never answered: %w", ctx.Err())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.held(lease); err != nil {
		return err
	}
	for _, r := range f.queue {
		if r.ID == lease.RunID {
			r.Status, r.Error = script.RunStatusPending, cause
			if f.dueAt == nil {
				f.dueAt = map[string]time.Time{}
			}
			f.dueAt[r.ID] = time.Now().Add(backoff)
		}
	}
	f.retried = append(f.retried, cause)
	f.ends = append(f.ends, attemptEnd{outcome: outcome, reason: reason})
	return nil
}

// RecordProgress models the real store: fenced on the lease, an unchanged
// report leaves the row alone, and the cancel flag is read back.
func (f *fakeRuns) RecordProgress(_ context.Context, lease script.RunLease, live script.RunLive) (requested bool, by string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if heldErr := f.held(lease); heldErr != nil {
		return false, "", heldErr
	}
	f.reports = append(f.reports, live)
	for _, r := range f.queue {
		if r.ID == lease.RunID {
			return r.CancelRequestedAt != nil, r.CancelRequestedBy, nil
		}
	}
	return false, "", nil
}

// CancelRun models the real store's transition from the run's prior status,
// ending a running run whose worker is gone directly.
func (f *fakeRuns) CancelRun(_ context.Context, id, by string) (prior, now string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.find(id)
	if !ok {
		return "", "", script.ErrRunNotFound
	}
	prior = r.Status
	at := time.Now().UTC()
	switch {
	case prior == script.RunStatusPending:
		r.Status, r.FinishedAt, r.Error = script.RunStatusCanceled, &at, "canceled by "+by
		r.CancelRequestedAt, r.CancelRequestedBy = &at, by
	case prior == script.RunStatusRunning && r.Liveness(at) != runstate.LivenessExecuting:
		r.Status, r.FinishedAt, r.Error = script.RunStatusCanceled, &at, "canceled by "+by+"; the worker had stopped reporting"
		r.LockedUntil, r.LockedBy = nil, ""
	case prior == script.RunStatusRunning && r.CancelRequestedAt == nil:
		r.CancelRequestedAt, r.CancelRequestedBy = &at, by
	}
	return prior, r.Status, nil
}

// find returns the stored row for id. The caller holds the lock.
func (f *fakeRuns) find(id string) (*script.Run, bool) {
	for _, r := range f.queue {
		if r.ID == id {
			return r, true
		}
	}
	return nil, false
}

func (f *fakeRuns) PurgeRuns(_ context.Context, _ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.purgeErr != nil {
		return 0, f.purgeErr
	}
	f.purged++
	return 3, nil
}

func (f *fakeRuns) blocked() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blockWrites
}

func (f *fakeRuns) claimCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claims
}

func (f *fakeRuns) results() []script.RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]script.RunResult(nil), f.finished...)
}

// fakeScripts and fakeVersions serve the two reads the worker makes.
type fakeScripts struct {
	script *script.Script
	err    error
}

func (f *fakeScripts) GetByID(context.Context, string) (*script.Script, error) {
	return f.script, f.err
}

type fakeVersions struct {
	version *script.Version
	err     error
}

func (f *fakeVersions) GetVersionByID(context.Context, string) (*script.Version, error) {
	return f.version, f.err
}

// GetVersion models the real store's contract: a number that is not in the
// history returns nil, nil. Answering any number with the stored version would
// let a caller pass the wrong one and still find a version there.
func (f *fakeVersions) GetVersion(_ context.Context, _ string, version int) (*script.Version, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.version == nil || f.version.Version != version {
		return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
	}
	return f.version, nil
}

// fakeExecutor stands in for the runner.
type fakeExecutor struct {
	out    attempt
	called int
}

func (f *fakeExecutor) execute(context.Context, *script.Run, *script.Script, *script.Version) attempt {
	f.called++
	return f.out
}

// executableState returns a script and version the gate admits, plus a pending
// run against them. The version is the script's current one, which is what a
// run executes.
func executableState() (*script.Script, *script.Version, *script.Run) {
	v := &script.Version{
		ID: "sver_1", ScriptID: "script_1", Version: 3, Source: "print(1)",
		Author: "jane@example.com", AuthorRoles: []string{"analyst"},
	}
	sc := &script.Script{
		ID: "script_1", Name: "daily", OwnerEmail: "jane@example.com",
		Enabled: true, Status: script.StatusActive, Version: v.Version,
	}
	run := &script.Run{
		ID: "dpx_1", ScriptID: sc.ID, VersionID: v.ID, Version: v.Version, Trigger: script.TriggerTool,
	}
	return sc, v, run
}

// newTestWorker assembles a worker over the fakes with one pending run queued.
func newTestWorker(t *testing.T, mutate func(*script.Script, *script.Version), out attempt) (*worker, *fakeRuns, *fakeExecutor) {
	t.Helper()
	sc, v, run := executableState()
	if mutate != nil {
		mutate(sc, v)
	}
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	exec := &fakeExecutor{out: out}
	return newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: exec, load: fakeLoad{},
	}), runs, exec
}

// drainAll drains the queue and waits for every run it launched, which is the
// synchronous view a test of one run's outcome needs.
func drainAll(w *worker) {
	w.drain()
	w.wg.Wait()
}

// succeeded is the attempt a clean run produces.
var succeeded = attempt{result: script.RunResult{Status: script.RunStatusSucceeded, Log: "done"}}

// TestWorker_ExecutesADueRunAndRecordsTheResult is the happy path: claim,
// execute, finish.
func TestWorker_ExecutesADueRunAndRecordsTheResult(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	drainAll(w)

	assert.Equal(t, 1, exec.called)
	results := runs.results()
	require.Len(t, results, 1)
	assert.Equal(t, script.RunStatusSucceeded, results[0].Status)
	assert.Equal(t, "done", results[0].Log)
}

// TestWorker_ReChecksTheGateBeforeExecuting covers the states that change
// between queueing a run and running it. Each one must fail the run without
// ever reaching the interpreter.
func TestWorker_ReChecksTheGateBeforeExecuting(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*script.Script, *script.Version)
		wantErr string
	}{
		{"disabled after queueing", func(sc *script.Script, _ *script.Version) {
			sc.Enabled = false
		}, "disabled"},
		{"deprecated after queueing", func(sc *script.Script, _ *script.Version) {
			sc.Status = script.StatusDeprecated
		}, "deprecated"},
		{"superseded after queueing", func(sc *script.Script, _ *script.Version) {
			sc.Status, sc.SupersededBy = script.StatusSuperseded, "daily-v2"
		}, "superseded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, runs, exec := newTestWorker(t, tt.mutate, succeeded)
			drainAll(w)

			assert.Zero(t, exec.called, "a refused run must not reach the interpreter")
			results := runs.results()
			require.Len(t, results, 1)
			assert.Equal(t, script.RunStatusFailed, results[0].Status)
			assert.Contains(t, results[0].Error, tt.wantErr)
		})
	}
}

// TestWorker_MissingScriptOrVersionFailsTheRun covers the rows a deleted script
// or version leaves behind.
func TestWorker_MissingScriptOrVersionFailsTheRun(t *testing.T) {
	t.Run("script gone", func(t *testing.T) {
		w, runs, _ := newTestWorker(t, nil, succeeded)
		w.cfg.scripts = &fakeScripts{}
		drainAll(w)
		require.Len(t, runs.results(), 1)
		assert.Contains(t, runs.results()[0].Error, "no longer exists")
	})

	t.Run("version gone", func(t *testing.T) {
		w, runs, _ := newTestWorker(t, nil, succeeded)
		w.cfg.versions = &fakeVersions{}
		drainAll(w)
		require.Len(t, runs.results(), 1)
		assert.Contains(t, runs.results()[0].Error, "no longer exists")
	})
}

// TestWorker_StoreReadFailuresRetry pins the retry boundary from the other
// side: a read that failed says nothing about the script, so the run goes back
// on the queue rather than being marked failed.
func TestWorker_StoreReadFailuresRetry(t *testing.T) {
	for _, tt := range []struct {
		name  string
		apply func(*worker)
	}{
		{"script read", func(w *worker) { w.cfg.scripts = &fakeScripts{err: errors.New("boom")} }},
		{"version read", func(w *worker) { w.cfg.versions = &fakeVersions{err: errors.New("boom")} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, runs, _ := newTestWorker(t, nil, succeeded)
			tt.apply(w)
			drainAll(w)

			assert.Empty(t, runs.results(), "a platform fault is not a script failure")
			require.Len(t, runs.retried, 1)
			assert.Contains(t, runs.retried[0], "boom")
		})
	}
}

// TestWorker_ScriptFailuresNeverRetry is the determinism rule in the queue: the
// same version on the same inputs fails the same way, and a run that already
// wrote an output must not be replayed.
func TestWorker_ScriptFailuresNeverRetry(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, attempt{
		result: script.RunResult{Status: script.RunStatusFailed, Error: "Traceback: boom"},
	})
	drainAll(w)

	assert.Empty(t, runs.retried)
	require.Len(t, runs.results(), 1)
	assert.Equal(t, script.RunStatusFailed, runs.results()[0].Status)
}

// TestWorker_RetryBudgetIsBounded pins that infrastructure retries stop: past
// the budget the run is failed with the cause rather than requeued forever.
func TestWorker_RetryBudgetIsBounded(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	w.cfg.scripts = &fakeScripts{err: errors.New("boom")}
	w.cfg.maxAttempts = 2

	drainAll(w) // attempt 1: retried with a backoff
	runs.dueAt = nil
	drainAll(w) // attempt 2: budget spent, failed
	assert.Len(t, runs.retried, 1)
	require.Len(t, runs.results(), 1)
	assert.Contains(t, runs.results()[0].Error, "boom")
}

// TestWorker_LostLeaseIsNotAnError covers the reclaim case: another replica
// took the run over, so this worker's write is refused and that is the correct
// outcome, not a failure to report.
func TestWorker_LostLeaseIsNotAnError(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	runs.writeErr = script.ErrLeaseLost
	drainAll(w)
	assert.Empty(t, runs.results())
}

// TestWorker_ClaimFailuresStopTheDrain pins that a broken queue backs off to
// the next tick instead of spinning.
func TestWorker_ClaimFailuresStopTheDrain(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	runs.claimErr = errors.New("boom")
	drainAll(w)
	assert.Zero(t, exec.called)
}

// TestWorker_PurgeIsThrottled pins that the retention sweep runs once per
// window rather than on every drain.
func TestWorker_PurgeIsThrottled(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	drainAll(w)
	drainAll(w)
	assert.Equal(t, int64(1), runs.purged)
}

func TestWorker_PurgeFailureIsNotFatal(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	runs.purgeErr = errors.New("boom")
	drainAll(w)
	assert.Equal(t, 1, exec.called, "a failed sweep must not stop the queue draining")
}

// TestWorker_StartStopIsIdempotent covers the lifecycle contract the platform
// wires it under.
func TestWorker_StartStopIsIdempotent(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	ctx := context.Background()
	w.Start(ctx)
	w.Start(ctx)
	w.Notify()
	w.Notify() // a flurry coalesces
	require.Eventually(t, func() bool { return len(runs.results()) == 1 }, 2*time.Second, 5*time.Millisecond)
	w.Stop(ctx)
	w.Stop(ctx)
}

func TestComputeBackoff_GrowsAndCaps(t *testing.T) {
	assert.Equal(t, retryBackoffBase, computeBackoff(0))
	assert.Equal(t, retryBackoffBase, computeBackoff(1))
	assert.Equal(t, 2*retryBackoffBase, computeBackoff(2))
	assert.Equal(t, retryBackoffBase*(1<<maxBackoffShift), computeBackoff(99))
}

func TestWorkerID_IsDistinctPerWorker(t *testing.T) {
	assert.NotEqual(t, workerID(), workerID(),
		"two workers must not share a fencing name, or each could overwrite the other's runs")
}

// blockingExecutor holds a run until the test lets it go, standing in for a run
// with most of its clock left when the worker is asked to stop.
type blockingExecutor struct {
	entered chan struct{}
	once    sync.Once
}

func (b *blockingExecutor) execute(ctx context.Context, _ *script.Run, _ *script.Script, _ *script.Version) attempt {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	return attempt{result: script.RunResult{
		Status: script.RunStatusFailed, Error: "context canceled",
	}}
}

// startHolding starts a worker over the given executor and waits until it has
// claimed the queued run and entered execution.
func startHolding(t *testing.T, w *worker, entered <-chan struct{}) {
	t.Helper()
	w.Start(context.Background())
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker never picked up the queued run")
	}
}

// stopWithin stops the worker under a shutdown budget and fails if Stop does
// not return inside limit.
func stopWithin(t *testing.T, w *worker, budget, limit time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	stopped := make(chan struct{})
	go func() { w.Stop(ctx); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(limit):
		t.Fatal("Stop held the shutdown open past its budget")
	}
}

// TestWorker_ShutdownReleasesTheRunItWasHolding pins the far end of the drain:
// a run that outlasts the window is canceled rather than waited out, and it
// goes back on the queue rather than being recorded as failed, because a
// shutdown decided nothing about it.
func TestWorker_ShutdownReleasesTheRunItWasHolding(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	blocker := &blockingExecutor{entered: make(chan struct{})}
	w.cfg.runner = blocker
	startHolding(t, w, blocker.entered)

	// A budget just past the reserve leaves a drain window of ~100ms, which
	// this run cannot finish in.
	stopWithin(t, w, releaseReserve+100*time.Millisecond, 2*time.Second)

	assert.Empty(t, runs.results(), "a shutdown must not record a verdict on the run")
	require.Len(t, runs.retried, 1)
	assert.Contains(t, runs.retried[0], "shut down")

	requeued, err := runs.GetRun(context.Background(), "dpx_1")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusPending, requeued.Status,
		"another replica picks it up rather than waiting out the lease")
}

// TestWorker_ShutdownSpentItsBudgetReleasesImmediately covers the case where
// the drain arrives with nothing left: waiting for a window that has already
// closed would only push the release past the process's own deadline.
func TestWorker_ShutdownSpentItsBudgetReleasesImmediately(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	blocker := &blockingExecutor{entered: make(chan struct{})}
	w.cfg.runner = blocker
	startHolding(t, w, blocker.entered)

	stopWithin(t, w, -time.Second, 2*time.Second)

	require.Len(t, runs.retried, 1, "an exhausted budget releases rather than waits")
}

// finishingExecutor holds its run until the test releases it, standing in for a
// run that lands just inside the drain window.
type finishingExecutor struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *finishingExecutor) execute(ctx context.Context, _ *script.Run, _ *script.Script, _ *script.Version) attempt {
	f.once.Do(func() { close(f.entered) })
	select {
	case <-f.release:
		return succeeded
	case <-ctx.Done():
		return attempt{result: script.RunResult{Status: script.RunStatusFailed, Error: "context canceled"}}
	}
}

// TestWorker_ShutdownLetsARunFinishInsideTheWindow is the near end of the same
// drain, and the reason the window exists: a run seconds from done is worth
// waiting for, because releasing it costs a replica the whole run again.
func TestWorker_ShutdownLetsARunFinishInsideTheWindow(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	finisher := &finishingExecutor{entered: make(chan struct{}), release: make(chan struct{})}
	w.cfg.runner = finisher
	startHolding(t, w, finisher.entered)

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(finisher.release)
	}()
	stopWithin(t, w, releaseReserve+2*time.Second, 4*time.Second)

	assert.Empty(t, runs.retried, "a run that finished inside the window must not be requeued")
	require.Len(t, runs.results(), 1)
	assert.Equal(t, script.RunStatusSucceeded, runs.results()[0].Status)
}

// TestWorker_AClaimThatRacedTheStopIsReleasedNotExecuted covers the other edge
// of "a draining worker stops claiming": the stop landed while a claim query
// was already in flight, so the worker holds a run it has no window to execute.
func TestWorker_AClaimThatRacedTheStopIsReleasedNotExecuted(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	close(w.stopCh)

	assert.False(t, w.processNext(w.runCtx), "the drain is over; nothing more is claimed")
	assert.Zero(t, exec.called, "the drain window belongs to a run already executing")
	require.Len(t, runs.retried, 1)
	assert.Contains(t, runs.retried[0], "shut down")
}

// TestWorker_ARunThatSucceededAsTheCancelLandedIsRecorded covers the race at
// the edge of the window: the interpreter reported success, so the run is done
// and the cancel that arrived alongside it decided nothing. Releasing it would
// re-execute a script that had already done its work.
func TestWorker_ARunThatSucceededAsTheCancelLandedIsRecorded(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	run, err := runs.Claim(context.Background(), w.id, time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)

	w.cancelRun()
	w.resolve(run, succeeded)

	assert.Empty(t, runs.retried)
	require.Len(t, runs.results(), 1)
	assert.Equal(t, script.RunStatusSucceeded, runs.results()[0].Status)
}

// TestWorker_StoppingWithNoRunInFlightDoesNotWait pins that the window belongs
// to a run, not to the loop: a worker sitting in the queue's own calls has
// nothing worth saving, and waiting it out would only delay the cancel that
// unblocks those calls.
func TestWorker_StoppingWithNoRunInFlightDoesNotWait(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	runs.claimErr = errors.New("the database is not answering")
	w.Start(context.Background())

	// No deadline, so any wait would be the full cap.
	start := time.Now()
	w.Stop(context.Background())
	assert.Less(t, time.Since(start), drainWindowCap, "an idle worker stops immediately")
}

// TestShutdownWrite_SurvivesTheCancelAndIsStillBounded pins both halves of the
// contract for a write made while stopping: the run's cancellation must not
// erase the record of what the run did, and the write must not be able to
// spend the pod's whole termination grace period.
func TestShutdownWrite_SurvivesTheCancelAndIsStillBounded(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := shutdownWrite(parent)
	defer cancel()

	require.NoError(t, ctx.Err(), "a canceled run must still record what happened to it")
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "an unbounded write can outlive the process's own budget")
	assert.LessOrEqual(t, time.Until(deadline), releaseReserve)
}

// TestWorker_ShutdownDoesNotHangOnAWedgedStore is the same contract from the
// outside: the release write cannot reach the database, and the shutdown still
// completes rather than holding the pod open until it is killed.
func TestWorker_ShutdownDoesNotHangOnAWedgedStore(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	runs.blockWrites = true
	blocker := &blockingExecutor{entered: make(chan struct{})}
	w.cfg.runner = blocker
	startHolding(t, w, blocker.entered)

	stopWithin(t, w, releaseReserve+100*time.Millisecond, releaseReserve+3*time.Second)
}

// TestDrainWindow_ComesFromTheCallersBudget pins how the window is derived: the
// worker never spends time the process does not have, never spends the reserve
// the release write needs, never takes more than its share of a budget the rest
// of the lifecycle is also spending, and never holds a shutdown open for a
// whole run.
func TestDrainWindow_ComesFromTheCallersBudget(t *testing.T) {
	assert.Equal(t, drainWindowCap, drainWindow(context.Background()),
		"a caller with no deadline gets the cap")

	generous, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	assert.Equal(t, drainWindowCap, drainWindow(generous),
		"a long budget is still capped, or a shutdown would wait out a run")

	// Long enough that the cap does not bind, so the share does.
	shared, cancelShared := context.WithTimeout(context.Background(), 4*drainWindowCap)
	defer cancelShared()
	assert.LessOrEqual(t, drainWindow(shared), 4*drainWindowCap/drainBudgetShare,
		"the shutdown budget belongs to every component, not to this one")

	tight, cancelTight := context.WithTimeout(context.Background(), releaseReserve+time.Second)
	defer cancelTight()
	window := drainWindow(tight)
	assert.Positive(t, window)
	assert.Less(t, window, time.Second+time.Millisecond,
		"a tight budget keeps the reserve back for the release write")

	spent, cancelSpent := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelSpent()
	assert.Negative(t, drainWindow(spent), "a spent budget waits for nothing")
}

// TestWorker_RecordsTheRunItExecuted proves the metric reaches the exporter
// rather than only the recorder: the worker is assembled with a real Metrics,
// a run is drained through it, and the scrape is read back. A unit test that
// asserted a fake recorder was called would not show that the series an
// operator watches actually exists.
func TestWorker_RecordsTheRunItExecuted(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	w, _, _ := newTestWorker(t, nil, succeeded)
	w.cfg.metrics = m
	drainAll(w)

	body := scrapeWorkerMetrics(t, m)
	assert.Contains(t, body, "script_runs_total")
	assert.Contains(t, body, `script="daily"`)
	assert.Contains(t, body, `trigger="tool"`)
	assert.Contains(t, body, `status="succeeded"`)
	assert.Contains(t, body, "script_run_duration_seconds")
}

// A run whose script could not be loaded still counts: it reached a terminal
// state, and a script failing to load every night is exactly what an operator
// needs to see.
func TestWorker_RecordsARunThatCouldNotLoadItsScript(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	w, _, _ := newTestWorker(t, nil, succeeded)
	w.cfg.scripts = &fakeScripts{}
	w.cfg.metrics = m
	drainAll(w)

	body := scrapeWorkerMetrics(t, m)
	assert.Contains(t, body, `status="failed"`)
}

// scrapeWorkerMetrics reads the exporter's own output.
func scrapeWorkerMetrics(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}
