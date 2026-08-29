package scriptexec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRunner_HandsTheScriptTheStateItsRowRecorded pins the input half of the
// state contract (#1537): the script reads the state pinned on the run row at
// creation, not a fresh read, and what it saves travels on the result.
func TestRunner_HandsTheScriptTheStateItsRowRecorded(t *testing.T) {
	var seen middleware.PlatformContext
	sc, v, run := executableState()
	run.LockedBy, run.Attempt = "worker-a", 1
	run.StateRevision = 4
	run.StateRead = map[string]any{"synced_through": "2026-08-27"}
	v.Source = `
print(run.state["synced_through"])
platform.save_state({"synced_through": run.fire_time})
`
	runs := &fakeRuns{}
	require.NoError(t, runs.Enqueue(context.Background(), run))
	run.LockedBy, run.Attempt = "worker-a", 1

	audit := &recordingAudit{}
	r := newRunner(runs, Config{Server: identityServer(t, &seen), Audit: audit})
	out := r.execute(context.Background(), run, sc, v)

	require.Equal(t, script.RunStatusSucceeded, out.result.Status, out.result.Error)
	assert.Contains(t, out.result.Log, "2026-08-27")
	require.NotNil(t, out.result.State, "the staged state reaches the store on the result")
	assert.Equal(t, run.FireTime.UTC().Format("2006-01-02T15:04:05Z"), out.result.State.Value["synced_through"])
	events := audit.all()
	require.Len(t, events, 1)
	assert.Equal(t, int64(4), events[0].Parameters["state_revision"], "the lifecycle event names the revision the run read")
}

// TestAttemptFrom_CarriesStagedStateWhateverTheOutcome pins that the runner
// does not decide whether state is applied: a failed result still carries what
// was staged, and the store applies it only to a success.
func TestAttemptFrom_CarriesStagedStateWhateverTheOutcome(t *testing.T) {
	staged := &script.StateWrite{Value: map[string]any{"k": "v"}}
	ok := attemptFrom(&scriptrun.Result{State: staged}, nil)
	assert.Same(t, staged, ok.result.State)

	failed := attemptFrom(&scriptrun.Result{State: staged}, assert.AnError)
	assert.Equal(t, script.RunStatusFailed, failed.result.Status)
	assert.Same(t, staged, failed.result.State)

	assert.Nil(t, attemptFrom(&scriptrun.Result{}, nil).result.State, "a run that never saved stages nothing")
}
