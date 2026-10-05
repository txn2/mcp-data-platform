package scriptlayer

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/openrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestExclusiveIsSetWithUpdateAndShownByGet is #1986's manage_script
// criterion: update sets the setting, get reports it, and an update that does
// not send it leaves it alone.
func TestExclusiveIsSetWithUpdateAndShownByGet(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	on := true

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Exclusive: &on})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, true, resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))["exclusive"])

	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", DisplayName: "Daily"})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, true, resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))["exclusive"],
		"an update that does not send exclusive leaves it alone")

	off := false
	res = call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Exclusive: &off})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, false, resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGet, Name: "daily"}))["exclusive"])
}

// TestTurningExclusiveOnWithRunsOpenIsRefusedWithTheReason passes the store's
// refusal through in the caller's words rather than as an internal failure.
func TestTurningExclusiveOnWithRunsOpenIsRefusedWithTheReason(t *testing.T) {
	h, store := newFailingHandle()
	createDaily(t, h)
	store.updateErr = fmt.Errorf("updating script: %w: %s", openrun.ErrOpen, openrun.BlockedMessage)
	on := true

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Exclusive: &on})
	assert.True(t, res.IsError)
	assert.Equal(t, openrun.BlockedMessage, resultText(res))
}

// TestRunScript_RefusedNamingTheOpenRun is #1986's run_script criterion at the
// tool: the refusal names the open run and says nothing was queued.
func TestRunScript_RefusedNamingTheOpenRun(t *testing.T) {
	h, _, runs := runnableHandle(t)
	started := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	runs.enqueueErr = fmt.Errorf("enqueueing the run: %w", &openrun.Error{Open: openrun.Run{
		ID: "run_open", Trigger: script.TriggerSchedule, Status: script.RunStatusRunning, StartedAt: &started,
	}})

	out := runScriptCall(t, h, runScriptInput{Name: "daily"})
	assert.Contains(t, out["error"], "run run_open (started by its schedule, running since 2026-10-04T09:00:00Z)")
	assert.Contains(t, out["error"], "nothing was queued")
}

// TestRunSummaryCarriesASkipsReason is the tool's half of a skipped fire
// (#1986): runs and get_run say which run it waited on, and a run of any
// other status carries no reason.
func TestRunSummaryCarriesASkipsReason(t *testing.T) {
	sc := &script.Script{Name: "daily"}
	skipped := &script.Run{
		ID: "r2", Status: script.RunStatusSkippedOverlap,
		Error: "run r1 (started by run_script, running since 2026-10-04T09:00:00Z) was still open when this fire came due, so this fire was skipped",
	}
	assert.Contains(t, runSummary(sc, skipped)["reason"], "run r1 (started by run_script")
	assert.Contains(t, runResult(sc, skipped)["reason"], "run r1 (started by run_script")

	succeeded := &script.Run{ID: "r3", Status: script.RunStatusSucceeded}
	assert.NotContains(t, runSummary(sc, succeeded), "reason")
}
