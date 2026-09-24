package scriptexec

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// abandonedRun plants a run whose worker died holding it: running, lease
// expired, reclaims as given.
func abandonedRun(t *testing.T, runs *fakeRuns, reclaims int) *script.Run {
	t.Helper()
	_, _, run := executableState()
	expired := time.Now().Add(-time.Minute)
	run.Trigger = script.TriggerSchedule
	run.Status, run.LockedBy, run.LockedUntil = script.RunStatusRunning, "worker-gone", &expired
	run.Attempt, run.Reclaims = reclaims+1, reclaims
	runs.mu.Lock()
	runs.queue = append(runs.queue, run)
	runs.mu.Unlock()
	return run
}

func metricsFor(t *testing.T) *observability.Metrics {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m
}

// TestWorker_FailsARunWhoseReclaimsAreSpent holds #1860: a run whose workers
// kept dying is failed by any worker's loop, never executed again, counted,
// and its owner is told why.
func TestWorker_FailsARunWhoseReclaimsAreSpent(t *testing.T) {
	sc, v, _ := executableState()
	runs := &fakeRuns{}
	abandonedRun(t, runs, runstate.DefaultMaxReclaims)
	notifier := &fakeNotifier{}
	m := metricsFor(t)
	exec := &fakeExecutor{out: succeeded}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: exec, load: fakeLoad{}, notifier: notifier, metrics: m,
	})
	drainAll(w)

	assert.Zero(t, exec.called, "a run whose reclaims are spent is not executed again")
	got, err := runs.GetRun(context.Background(), "dpx_1")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusFailed, got.Status)
	assert.Equal(t, runstate.CauseWorkerLost, got.Cause)
	require.Len(t, notifier.payloads, 1, "the owner of a scheduled run hears it failed")
	assert.Equal(t, runstate.CauseWorkerLost, notifier.payloads[0].Cause)
	assert.Equal(t, notification.KindScriptRun, notifier.payloads[0].Kind)
	body := scrapeWorkerMetrics(t, m)
	assert.Contains(t, body, "script_run_reclaims_total{")
	assert.Contains(t, body, `outcome="failed"`)

	// The sweep runs at most once per poll interval.
	abandonedRun(t, runs, runstate.DefaultMaxReclaims)
	w.drain()
	assert.Len(t, notifier.payloads, 1)
}

// TestWorker_TakesOverARunUnderTheCap: a run whose worker died once is taken
// over and executed, and the take-over is counted.
func TestWorker_TakesOverARunUnderTheCap(t *testing.T) {
	sc, v, _ := executableState()
	runs := &fakeRuns{}
	abandonedRun(t, runs, 0)
	m := metricsFor(t)
	exec := &fakeExecutor{out: succeeded}
	w := newWorker(workerConfig{
		runs: runs, scripts: &fakeScripts{script: sc}, versions: &fakeVersions{version: v},
		runner: exec, load: fakeLoad{}, metrics: m,
	})
	drainAll(w)

	assert.Equal(t, 1, exec.called)
	got, err := runs.GetRun(context.Background(), "dpx_1")
	require.NoError(t, err)
	assert.Equal(t, 1, got.Reclaims)
	require.Len(t, got.Attempts, 1)
	assert.Equal(t, runstate.AttemptLeaseExpired, got.Attempts[0].Outcome)
	assert.Contains(t, scrapeWorkerMetrics(t, m), `outcome="reexecuted"`)
}

// failingAbandoned is a store whose sweep fails, which the worker logs and
// survives.
type failingAbandoned struct{ *fakeRuns }

func (failingAbandoned) FailAbandoned(context.Context, int) ([]script.Run, error) {
	return nil, errors.New("the database is down")
}

func TestWorker_SurvivesASweepThatFails(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	w.cfg.runs = failingAbandoned{runs}
	drainAll(w)
	assert.Equal(t, 1, exec.called, "the queue is still drained")
}

// TestWorker_RecordsHowARequeuedAttemptEnded: a platform fault is a retried
// attempt, a shutdown a released one.
func TestWorker_RecordsHowARequeuedAttemptEnded(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, *retryable("the warehouse was unreachable"))
	drainAll(w)
	require.Len(t, runs.ends, 1)
	assert.Equal(t, attemptEnd{outcome: runstate.AttemptRetried, reason: "the warehouse was unreachable"}, runs.ends[0])

	run := &script.Run{ID: "dpx_1", LockedBy: w.id, Attempt: 2}
	runs.queue[0].LockedBy, runs.queue[0].Attempt = w.id, 2
	w.release(context.Background(), run)
	require.Len(t, runs.ends, 2)
	assert.Equal(t, runstate.AttemptReleased, runs.ends[1].outcome)
}

// TestWorker_APlatformFaultPastTheBudgetIsRecordedAsThePlatforms: the last
// attempt of a run the platform could not open is failed with that cause.
func TestWorker_APlatformFaultPastTheBudgetIsRecordedAsThePlatforms(t *testing.T) {
	w, runs, _ := newTestWorker(t, nil, *retryable("opening the run's session failed"))
	w.cfg.maxAttempts = 1
	drainAll(w)
	results := runs.results()
	require.Len(t, results, 1)
	assert.Equal(t, runstate.CausePlatform, results[0].Cause)
}

// TestAttemptFrom_CarriesTheCauseAndThePeak holds the runner's half: the
// engine's failure becomes the recorded cause, and its measured peak the
// run's metric.
func TestAttemptFrom_CarriesTheCauseAndThePeak(t *testing.T) {
	up := attemptFrom(&scriptrun.Result{PeakMemory: 4096},
		scriptguard.NewUpstreamError("api_export", errors.New("i/o timeout")))
	assert.Equal(t, script.RunStatusFailed, up.result.Status)
	assert.Equal(t, runstate.CauseUpstream, up.result.Cause)
	assert.Equal(t, int64(4096), up.result.Metrics.PeakMemoryBytes)
	assert.False(t, up.retryable, "an upstream failure is never re-executed by the platform")

	mem := attemptFrom(nil, errors.Join(errors.New("in platform.query"), scriptguard.ErrMemoryBudget))
	assert.Equal(t, runstate.CauseMemory, mem.result.Cause)

	ok := attemptFrom(&scriptrun.Result{}, nil)
	assert.Empty(t, ok.result.Cause)
}
