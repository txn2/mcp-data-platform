package scriptexec

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// longLoop keeps a run executing well past a 1ms report interval; only a
// cancel or the step limit, raised here, ends it early.
const longLoop = `
platform.progress("counting", done=1, total=2)
print("started")
for i in range(40000000):
    pass
platform.result({"finished": True})
`

// liveRun is a runner over one claimed run whose reports land every
// millisecond, with what executing it takes.
type liveRun struct {
	runner *runner
	runs   *fakeRuns
	run    *script.Run
	script *script.Script
	ver    *script.Version
}

// execute runs the claimed run to its outcome.
func (l liveRun) execute() attempt {
	return l.runner.execute(context.Background(), l.run, l.script, l.ver)
}

func liveRunner(t *testing.T, source string) liveRun {
	t.Helper()
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	v.Source = source
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	claimed, err := runs.Claim(context.Background(), "worker-a", time.Minute, runstate.DefaultMaxReclaims)
	require.NoError(t, err)
	r := newRunner(runs, Config{
		Server: identityServer(t, &seen),
		Limits: scriptrun.PlatformLimits{MaxSteps: 1 << 40},
	})
	r.reportEvery = time.Millisecond
	return liveRun{runner: r, runs: runs, run: claimed, script: sc, ver: v}
}

// TestRunner_ReportsProgressAndTheLogWhileRunning is #1847's first criterion
// at the runner: a running run's progress and log reach its row before it
// ends, and #1845's result reaches the terminal outcome.
func TestRunner_ReportsProgressAndTheLogWhileRunning(t *testing.T) {
	live := liveRunner(t, `
platform.progress("counting", done=1, total=2)
print("started")
for i in range(3000000):
    pass
platform.result({"finished": True})
`)
	runs := live.runs
	out := live.execute()

	require.Equal(t, script.RunStatusSucceeded, out.result.Status, out.result.Error)
	assert.JSONEq(t, `{"finished": true}`, string(out.result.Result))
	require.NotNil(t, out.result.Progress)
	assert.Equal(t, int64(1), *out.result.Progress.Done)

	runs.mu.Lock()
	defer runs.mu.Unlock()
	require.NotEmpty(t, runs.reports, "the run reported while it executed")
	assert.False(t, runs.reports[0].Unchanged, "the first report always writes")
	var sawBoth bool
	for _, rep := range runs.reports {
		if !rep.Unchanged && rep.Log == "started\n" && rep.Progress != nil && rep.Progress.Message == "counting" {
			sawBoth = true
		}
	}
	assert.True(t, sawBoth, "a report carried the progress and the log so far")
	// The loop between the print and platform.result reports nothing new for
	// hundreds of ticks, and those ticks rewrite nothing. The last tick is not
	// the one to read: it can land after platform.result, which is a change.
	var unchanged int
	for _, rep := range runs.reports {
		if rep.Unchanged {
			unchanged++
		}
	}
	assert.Positive(t, unchanged, "once the loop reports nothing new, nothing is rewritten")
}

// TestRunner_ACancelRequestStopsTheRun ends a running run canceled, naming
// who asked, within one report of the request.
func TestRunner_ACancelRequestStopsTheRun(t *testing.T) {
	live := liveRunner(t, longLoop)
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _, _ = live.runs.CancelRun(context.Background(), live.run.ID, "sam@example.com")
	}()
	started := time.Now()
	out := live.execute()

	assert.Equal(t, script.RunStatusCanceled, out.result.Status)
	assert.Equal(t, "canceled by sam@example.com", out.result.Error)
	assert.Less(t, time.Since(started), 10*time.Second, "the run stopped instead of finishing its loop")
	assert.Nil(t, out.result.Result, "a canceled run did not reach its result")
}

// TestRunner_ALostLeaseStopsTheRun stops an execution whose run another
// worker has taken over: every write it made from then on would be refused.
func TestRunner_ALostLeaseStopsTheRun(t *testing.T) {
	live := liveRunner(t, longLoop)
	live.runs.mu.Lock()
	live.runs.writeErr = script.ErrLeaseLost
	live.runs.mu.Unlock()
	out := live.execute()
	assert.Equal(t, script.RunStatusFailed, out.result.Status, "a lost run is not a canceled one")
}

// TestWorker_ACancelRequestedBeforeTheClaimEndsTheRunUnexecuted covers a run
// whose worker stopped before acting on the request: whoever claims it next
// finishes it canceled without running the script.
func TestWorker_ACancelRequestedBeforeTheClaimEndsTheRunUnexecuted(t *testing.T) {
	w, runs, exec := newTestWorker(t, nil, succeeded)
	requested := time.Now()
	runs.mu.Lock()
	runs.queue[0].CancelRequestedAt, runs.queue[0].CancelRequestedBy = &requested, "sam@example.com"
	runs.mu.Unlock()
	drainAll(w)

	assert.Zero(t, exec.called)
	results := runs.results()
	require.Len(t, results, 1)
	assert.Equal(t, script.RunStatusCanceled, results[0].Status)
	assert.Equal(t, "canceled by sam@example.com", results[0].Error)
}

func TestCancelledError(t *testing.T) {
	assert.Equal(t, "canceled on request", cancelledError(""))
	assert.Equal(t, "canceled by a@b.c", cancelledError("a@b.c"))
}

// TestOutputWriter_RecordsAToolsOutputWithoutClaimingTheName is #1854 at the
// writer: the tool's asset is on the run row and in the run's outputs, and a
// later platform.export of the same name is still its own write.
func TestOutputWriter_RecordsAToolsOutputWithoutClaimingTheName(t *testing.T) {
	h := newWriterHarness(t)
	out := script.RunOutput{Tool: "trino_export", Name: "daily", Destination: script.DestinationPortal, AssetID: "a1", AssetVersion: 1}
	h.writer.RecordToolOutput(context.Background(), out)

	h.runs.mu.Lock()
	recorded := append([]script.RunOutput(nil), h.runs.outputs...)
	h.runs.mu.Unlock()
	require.Len(t, recorded, 1, "the output reached the run row, under the run's lease")
	assert.Equal(t, "trino_export", recorded[0].Tool)
	assert.Len(t, h.writer.run.Outputs, 1)
	assert.NoError(t, h.writer.refuseRepeat("daily", script.DestinationPortal),
		"the tool's write does not stand in for a platform.export of that name")

	// A draft's writer has no run row, and keeps the record on the attempt.
	draft := newOutputWriter(h.writer.deps, nil, claimedRun{run: &script.Run{ID: "draft"}, script: h.writer.script, version: testVersion()}, h.caller)
	draft.RecordToolOutput(context.Background(), out)
	assert.Len(t, draft.run.Outputs, 1)

	// A run whose lease moved still lists the output on this attempt; the row
	// belongs to the worker that took it over.
	h.runs.mu.Lock()
	h.runs.writeErr = script.ErrLeaseLost
	h.runs.mu.Unlock()
	h.writer.RecordToolOutput(context.Background(), out)
	assert.Len(t, h.writer.run.Outputs, 2)
}
