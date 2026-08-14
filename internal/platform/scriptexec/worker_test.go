package scriptexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	claimErr error
	writeErr error
	purged   int64
	purgeErr error
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

func (f *fakeRuns) Claim(_ context.Context, worker string, _ time.Duration) (*script.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	for _, r := range f.queue {
		if r.Status != script.RunStatusPending || !f.due(r) {
			continue
		}
		r.Status, r.LockedBy = script.RunStatusRunning, worker
		r.Attempt++
		out := *r
		return &out, nil
	}
	return nil, script.ErrNoWork
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

func (f *fakeRuns) Retry(_ context.Context, lease script.RunLease, cause string, backoff time.Duration) error {
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
	return nil
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
// run against them.
func executableState() (*script.Script, *script.Version, *script.Run) {
	approvedAt := time.Now().UTC()
	v := &script.Version{
		ID: "sver_1", ScriptID: "script_1", Version: 3, Source: "print(1)",
		AuthorRoles: []string{"analyst"}, ApprovedBy: "admin@example.com", ApprovedAt: &approvedAt,
		Grants: script.Grants{Roles: []string{"analyst"}, Capabilities: []string{script.CapabilityQuery}},
	}
	sc := &script.Script{
		ID: "script_1", Name: "daily", Scope: script.ScopePersonal, OwnerEmail: "jane@example.com",
		Enabled: true, Status: script.StatusActive, ApprovedVersionID: v.ID,
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
		runner: exec,
	}), runs, exec
}

// succeeded is the attempt a clean run produces.
var succeeded = attempt{result: script.RunResult{Status: script.RunStatusSucceeded, Log: "done"}}

// TestWorker_ExecutesADueRunAndRecordsTheResult is the happy path: claim,
// execute, finish.
func TestWorker_ExecutesADueRunAndRecordsTheResult(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	w.drain()

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
		{"approval moved to another version", func(sc *script.Script, _ *script.Version) {
			sc.ApprovedVersionID = "sver_9"
		}, "is not any more"},
		{"grant removed", func(_ *script.Script, v *script.Version) {
			v.Grants = script.Grants{}
		}, "no approval grant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, runs, exec := newTestWorker(t, tt.mutate, succeeded)
			w.drain()

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
		w.drain()
		require.Len(t, runs.results(), 1)
		assert.Contains(t, runs.results()[0].Error, "no longer exists")
	})

	t.Run("version gone", func(t *testing.T) {
		w, runs, _ := newTestWorker(t, nil, succeeded)
		w.cfg.versions = &fakeVersions{}
		w.drain()
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
			w.drain()

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
	w.drain()

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

	w.drain() // attempt 1: retried with a backoff
	runs.dueAt = nil
	w.drain() // attempt 2: budget spent, failed
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
	w.drain()
	assert.Empty(t, runs.results())
}

// TestWorker_ClaimFailuresStopTheDrain pins that a broken queue backs off to
// the next tick instead of spinning.
func TestWorker_ClaimFailuresStopTheDrain(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	runs.claimErr = errors.New("boom")
	w.drain()
	assert.Zero(t, exec.called)
}

// TestWorker_PurgeIsThrottled pins that the retention sweep runs once per
// window rather than on every drain.
func TestWorker_PurgeIsThrottled(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	w.drain()
	w.drain()
	assert.Equal(t, int64(1), runs.purged)
}

func TestWorker_PurgeFailureIsNotFatal(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	runs.purgeErr = errors.New("boom")
	w.drain()
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
	w.Stop()
	w.Stop()
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

// TestWorker_ShutdownReleasesTheRunItWasHolding pins that stopping is prompt and
// honest: the run in flight is canceled rather than waited out, and it goes
// back on the queue rather than being recorded as failed, because a shutdown
// decided nothing about it.
func TestWorker_ShutdownReleasesTheRunItWasHolding(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, succeeded)
	blocker := &blockingExecutor{entered: make(chan struct{})}
	w.cfg.runner = blocker

	w.Start(context.Background())
	select {
	case <-blocker.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker never picked up the queued run")
	}

	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited for the run instead of canceling it")
	}

	assert.Empty(t, runs.results(), "a shutdown must not record a verdict on the run")
	require.Len(t, runs.retried, 1)
	assert.Contains(t, runs.retried[0], "shut down")

	requeued, err := runs.GetRun(context.Background(), "dpx_1")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusPending, requeued.Status,
		"another replica picks it up rather than waiting out the lease")
}
