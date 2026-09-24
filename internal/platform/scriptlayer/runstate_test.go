package scriptlayer

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// queuedRun queues one run of "daily" and returns the stored row, which a test
// moves into the state it needs.
func queuedRun(t *testing.T, h *Handle, runs *stubRuns) *script.Run {
	t.Helper()
	queued := runScriptCall(t, h, runScriptInput{Name: "daily", WaitSeconds: -1})
	runID, ok := queued["run_id"].(string)
	require.True(t, ok)
	runs.mu.Lock()
	defer runs.mu.Unlock()
	return runs.byID[runID]
}

// TestGetRun_SaysWhyAFailedRunFailed holds #1859: a failure carries its cause,
// whether running it again is expected to help, and a message that sends the
// owner to the script only when the script is at fault.
func TestGetRun_SaysWhyAFailedRunFailed(t *testing.T) {
	cases := map[string]struct {
		cause     string
		retryable bool
		says      string
	}{
		"a script error":               {runstate.CauseScript, false, "Fix the script"},
		"a run recorded before causes": {"", false, "Fix the script"},
		"an upstream":                  {runstate.CauseUpstream, true, "temporarily unavailable"},
		"memory":                       {runstate.CauseMemory, false, "append=True"},
		"a lost worker":                {runstate.CauseWorkerLost, false, "stopped without reporting"},
		"the platform":                 {runstate.CausePlatform, false, "nothing in the script to fix"},
		"a state conflict":             {runstate.CauseStateConflict, true, "saved its state first"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, _, runs := runnableHandle(t)
			run := queuedRun(t, h, runs)
			run.Status, run.Error, run.Cause = script.RunStatusFailed, "boom", tc.cause
			run.Metrics.PeakMemoryBytes = 2048

			got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: run.ID}))
			want := tc.cause
			if want == "" {
				want = runstate.CauseScript
			}
			assert.Equal(t, want, got["cause"])
			assert.Equal(t, tc.retryable, got["retryable"])
			assert.Contains(t, got["message"], tc.says)
			metrics, _ := got["metrics"].(map[string]any)
			assert.EqualValues(t, 2048, metrics["peak_memory_bytes"])
		})
	}
}

// TestGetRun_NeverReadsAMissingWorkerAsRunning holds #1860: a running run
// whose worker stopped reporting says so, names the holder and the lease, and
// carries the attempt history; one whose worker reports carries no message.
func TestGetRun_NeverReadsAMissingWorkerAsRunning(t *testing.T) {
	h, _, runs := runnableHandle(t)
	run := queuedRun(t, h, runs)
	now := time.Now()
	until, seen := now.Add(10*time.Minute), now.Add(-5*time.Minute)
	run.Status, run.LockedBy, run.LockedUntil, run.HeartbeatAt = script.RunStatusRunning, "worker-gone", &until, &seen
	run.Attempt, run.Reclaims = 2, 1
	run.Attempts = []runstate.Attempt{{Attempt: 1, Worker: "worker-first", Outcome: runstate.AttemptLeaseExpired}}

	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: run.ID}))
	assert.Equal(t, runstate.LivenessUnresponsive, got["liveness"])
	assert.Equal(t, "worker-gone", got["locked_by"])
	assert.EqualValues(t, 1, got["reclaims"])
	assert.NotNil(t, got["locked_until"])
	assert.NotNil(t, got["heartbeat_at"])
	assert.Contains(t, got["message"], "stopped reporting")
	attempts, _ := got["attempts"].([]any)
	assert.Len(t, attempts, 1)

	expired := now.Add(-time.Minute)
	run.LockedUntil = &expired
	got = resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: run.ID}))
	assert.Equal(t, runstate.LivenessLeaseExpired, got["liveness"])
	assert.Contains(t, got["message"], "its lease ended")

	fresh := now
	run.LockedUntil, run.HeartbeatAt = &until, &fresh
	got = resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGetRun, RunID: run.ID}))
	assert.Equal(t, runstate.LivenessExecuting, got["liveness"])
	assert.NotContains(t, got, "message")
}

// TestGet_ListsTheScriptsLiveRuns holds #1860 item 3: the script lists the
// runs that have not ended, so an orphaned run is seen without its id.
func TestGet_ListsTheScriptsLiveRuns(t *testing.T) {
	h, _, runs := runnableHandle(t)
	live := queuedRun(t, h, runs)
	done := queuedRun(t, h, runs)
	done.Status = script.RunStatusSucceeded
	now := time.Now()
	until, seen := now.Add(10*time.Minute), now.Add(-5*time.Minute)
	live.Status, live.LockedBy, live.LockedUntil, live.HeartbeatAt = script.RunStatusRunning, "worker-gone", &until, &seen

	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	listed, _ := got["live_runs"].([]any)
	require.Len(t, listed, 1)
	row, _ := listed[0].(map[string]any)
	assert.Equal(t, live.ID, row["run_id"])
	assert.Equal(t, runstate.LivenessUnresponsive, row["liveness"])
	assert.Contains(t, row["message"], "stopped reporting")

	runs.listErr = errors.New("the run table is down")
	got = resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	listed, _ = got["live_runs"].([]any)
	assert.Empty(t, listed, "a listing that fails lists none rather than failing the script's read")
	assert.NotNil(t, got["live_runs"], "an empty list is a list")
}

// TestGet_ListsNoLiveRunsWithoutARunStore: a deployment that keeps no runs
// lists none.
func TestGet_ListsNoLiveRunsWithoutARunStore(t *testing.T) {
	store := newMemStore()
	h := New(Config{Store: store, AdminPersona: "admin"})
	require.False(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "daily", Source: "print(1)\n"}).IsError)
	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))
	listed, ok := got["live_runs"].([]any)
	require.True(t, ok)
	assert.Empty(t, listed)
}

// TestCancelRun_EndsARunWhoseWorkerIsGone: no worker will observe the request,
// so the run is canceled at once and the answer says so.
func TestCancelRun_EndsARunWhoseWorkerIsGone(t *testing.T) {
	h, _, runs := runnableHandle(t)
	run := queuedRun(t, h, runs)
	expired := time.Now().Add(-time.Minute)
	run.Status, run.LockedBy, run.LockedUntil = script.RunStatusRunning, "worker-gone", &expired

	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdCancelRun, RunID: run.ID}))
	assert.Equal(t, script.RunStatusCanceled, got[fieldStatus])
	assert.Equal(t, "canceled_orphaned", got["outcome"])
	assert.Contains(t, got["message"], "stopped reporting")
}

// TestHelp_ReportsTheMemoryBudget: the help names the budget a run meets.
func TestHelp_ReportsTheMemoryBudget(t *testing.T) {
	h := New(Config{Store: newMemStore(), RunLimits: scriptrun.PlatformLimits{MaxMemoryBytes: 64 << 20}})
	got := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdHelp}))
	limits, _ := got["limits"].(map[string]any)
	assert.EqualValues(t, 64<<20, limits["run_max_memory_bytes"])
	note, _ := limits["note"].(string)
	assert.Contains(t, note, "peak_memory_bytes")
	assert.Contains(t, note, "upstream_retryable")
}
