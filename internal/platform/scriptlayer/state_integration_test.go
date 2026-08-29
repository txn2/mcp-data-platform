package scriptlayer

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptexec"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The assembled-system proof for #1537: a script carries one object of state
// from run to run through the real tool, the real worker and the real engine.
// The store is the in-memory one, modeling the compare-and-set the real
// store's Finish applies; the database-level proof of that predicate is the
// RealDB suite in internal/platform/scriptstore.

// watermarkSource reads its watermark, pulls from it, exports, and saves the
// new mark. The mark is the pinned fire time, so two runs save different
// values without a clock.
const watermarkSource = `since = run.state.get("synced_through", "never")
print("since " + since)
res = platform.query(connection="warehouse", sql="SELECT region, total FROM sales WHERE d > :since", params={"since": since})
platform.export(name="delta-" + run.run_id, rows=res["rows"], format="csv")
platform.save_state({"synced_through": run.fire_time, "rows": res["row_count"]})
`

// str reads a string field of a tool result, "" when it is absent.
func str(fields map[string]any, key string) string {
	s, _ := fields[key].(string)
	return s
}

// stateOf reads a script's state through the tool.
func stateOf(t *testing.T, h execHarness) map[string]any {
	t.Helper()
	res := call(t, h.handle, authorCtx(), manageScriptInput{Command: cmdState, Name: "daily"})
	require.False(t, res.IsError, resultText(res))
	return resultFields(t, res)
}

// TestIntegration_ASecondRunReadsWhatTheFirstSaved is the acceptance case: run
// twice, and the second run's run.state is the first run's save, with each run
// row explaining the revision it read and the one it wrote.
func TestIntegration_ASecondRunReadsWhatTheFirstSaved(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, watermarkSource)
	session := connectAgent(ctx, t, h.server)

	first, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}})
	require.False(t, isErr, first["error"])
	require.Equal(t, script.RunStatusSucceeded, first["status"], first)
	assert.Contains(t, first["log"], "since never")
	assert.Equal(t, float64(0), first["state_revision"], "the first run read revision 0")
	assert.Equal(t, map[string]any{}, first["state_read"])
	assert.Equal(t, float64(1), first["state_revision_written"])
	written, ok := first["state_written"].(map[string]any)
	require.True(t, ok, first)
	assert.Equal(t, float64(2), written["rows"])

	st := stateOf(t, h)
	assert.Equal(t, float64(1), st["revision"])
	assert.Equal(t, "run "+str(first, "run_id"), st["written_by"])

	second, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}})
	require.False(t, isErr, second["error"])
	require.Equal(t, script.RunStatusSucceeded, second["status"], second)
	assert.Contains(t, second["log"], "since "+str(written, "synced_through"), "the second run reads what the first saved")
	assert.Equal(t, float64(1), second["state_revision"])
	assert.Equal(t, float64(2), second["state_revision_written"])

	single, isErr := callTool(ctx, t, session, map[string]any{"command": cmdGetRun, "run_id": second["run_id"]})
	require.False(t, isErr, single)
	assert.Equal(t, float64(1), single["state_revision"], "get_run explains the run from its own row")
	assert.NotNil(t, single["state_written"])
}

// TestIntegration_AFailedRunLeavesTheStateWhereItWas: the export fails after
// save_state was called, and the watermark does not move.
func TestIntegration_AFailedRunLeavesTheStateWhereItWas(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `
platform.save_state({"synced_through": run.fire_time})
platform.export(name="delta", rows=[{"a": 1}], format="csv", destination="nowhere")
`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.NotContains(t, out, "state_written")

	st := stateOf(t, h)
	assert.Equal(t, float64(0), st["revision"], "a watermark never moves past work that did not happen")
	assert.Equal(t, map[string]any{}, st["state"])
}

// TestIntegration_TwoRunsReadingOneRevisionCannotBothWrite: both runs are
// created before either executes, so both read revision 0; the worker then
// executes them in order, the first writes revision 1, and the second fails at
// its write naming the first, its output already recorded.
func TestIntegration_TwoRunsReadingOneRevisionCannotBothWrite(t *testing.T) {
	ctx := context.Background()
	h := execServerWithWorker(t, false, "warehouse")
	authorScript(t, h, watermarkSource)
	session := connectAgent(ctx, t, h.server)

	// Queued while nothing executes, so both rows pin revision 0.
	a, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}, "wait_seconds": -1})
	require.False(t, isErr, a["error"])
	b, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}, "wait_seconds": -1})
	require.False(t, isErr, b["error"])

	worker := startWorker(t, h)
	defer func() { _ = worker.Stop(ctx) }()
	doneA := awaitRunResult(ctx, t, session, str(a, "run_id"))
	doneB := awaitRunResult(ctx, t, session, str(b, "run_id"))

	assert.Equal(t, script.RunStatusSucceeded, doneA["status"], doneA)
	assert.Equal(t, float64(1), doneA["state_revision_written"])
	assert.Equal(t, script.RunStatusFailed, doneB["status"], "the second write is refused rather than lost")
	assert.Contains(t, doneB["error"], "run "+str(a, "run_id"), "the failure names the run that wrote")
	assert.Contains(t, doneB["error"], "outputs stand")
	outputs, ok := doneB["outputs"].([]any)
	require.True(t, ok)
	assert.Len(t, outputs, 1, "the loser's output was produced from the state it read, and stands")

	st := stateOf(t, h)
	assert.Equal(t, float64(1), st["revision"])
}

// startWorker starts a run worker over the harness's stores, for a test that
// assembled the system with the worker off so it could queue runs first.
func startWorker(t *testing.T, h execHarness) *scriptexec.Handle {
	t.Helper()
	worker := scriptexec.New(scriptexec.Config{
		Runs: h.runs, Scripts: h.store, Versions: h.store,
		Server: h.server, Audit: h.audit,
		Export:       scriptexec.ExportDeps{Assets: h.assets, Versions: h.versions, S3: h.s3, Bucket: "assets", Prefix: "portal"},
		Destinations: []script.Destination{acmeDrop()},
	})
	require.NotNil(t, worker)
	h.runs.mu.Lock()
	h.runs.notify = worker.Notify
	h.runs.mu.Unlock()
	require.NoError(t, worker.Start(context.Background()))
	return worker
}

// awaitRunResult polls get_run until the run is terminal.
func awaitRunResult(ctx context.Context, t *testing.T, session *mcp.ClientSession, runID string) map[string]any {
	t.Helper()
	require.Eventually(t, func() bool {
		out, isErr := callTool(ctx, t, session, map[string]any{"command": cmdGetRun, "run_id": runID})
		if isErr {
			return false
		}
		s, _ := out["status"].(string)
		return s == script.RunStatusSucceeded || s == script.RunStatusFailed
	}, 10*time.Second, 25*time.Millisecond)
	out, _ := callTool(ctx, t, session, map[string]any{"command": cmdGetRun, "run_id": runID})
	return out
}

// TestIntegration_AClearUnderARunInFlightFailsItsWrite: the reset moved the
// revision after the run read it, so the run fails at its write and the next
// run starts from {}.
func TestIntegration_AClearUnderARunInFlightFailsItsWrite(t *testing.T) {
	ctx := context.Background()
	h := execServerWithWorker(t, false, "warehouse")
	authorScript(t, h, watermarkSource)
	session := connectAgent(ctx, t, h.server)
	_, err := h.store.SetState(ctx, "script_1", map[string]any{"synced_through": "2026-08-01T00:00:00Z"}, "jane@example.com")
	require.NoError(t, err)

	// A run pinned at revision 1, then the clear lands before it executes.
	queued, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}, "wait_seconds": -1})
	require.False(t, isErr, queued["error"])
	cleared := call(t, h.handle, authorCtx(), manageScriptInput{Command: cmdState, Name: "daily", StateAction: stateActionClear})
	require.False(t, cleared.IsError, resultText(cleared))
	assert.Equal(t, float64(2), resultFields(t, cleared)["revision"])

	worker := startWorker(t, h)
	defer func() { _ = worker.Stop(ctx) }()
	done := awaitRunResult(ctx, t, session, str(queued, "run_id"))
	assert.Equal(t, script.RunStatusFailed, done["status"], done)
	assert.Contains(t, done["error"], "jane@example.com wrote revision 2")
	assert.Equal(t, float64(1), done["state_revision"], "the run row still says what it read")

	next, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}})
	require.False(t, isErr, next["error"])
	assert.Equal(t, script.RunStatusSucceeded, next["status"], next)
	assert.Contains(t, next["log"], "since never", "the next run starts from {}")
	assert.Equal(t, float64(2), next["state_revision"])
}

// TestIntegration_ADraftReadsLiveStateAndPersistsNothing: the draft reads the
// state a platform run would read and reports what it would have saved, and
// the state is untouched.
func TestIntegration_ADraftReadsLiveStateAndPersistsNothing(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, watermarkSource)
	_, err := h.store.SetState(ctx, "script_1", map[string]any{"synced_through": "2026-08-01T00:00:00Z"}, "jane@example.com")
	require.NoError(t, err)

	res := call(t, h.handle, authorCtx(), manageScriptInput{Command: cmdRunDraft, Name: "daily", Args: map[string]any{"day": "x"}})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, "succeeded", fields["status"], fields)
	assert.Contains(t, fields["log"], "since 2026-08-01T00:00:00Z", "the draft reads the live state")
	state, ok := fields["state"].(map[string]any)
	require.True(t, ok, "the draft reports the state it would have saved")
	assert.Equal(t, float64(2), state["rows"])
	assert.Contains(t, fields["message"], "did not save it")

	st := stateOf(t, h)
	assert.Equal(t, float64(1), st["revision"], "nothing was persisted")
	saved, _ := st["state"].(map[string]any)
	assert.Equal(t, "2026-08-01T00:00:00Z", saved["synced_through"])
}

// TestIntegration_AScriptThatNeverSavesHasNoWrittenState: the run row carries
// no state written and the contract says it keeps none.
func TestIntegration_AScriptThatNeverSavesHasNoWrittenState(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "2026-08-12"}})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.NotContains(t, out, "state_written")
	assert.NotContains(t, out, "state_revision_written")
	assert.Equal(t, float64(0), out["state_revision"])
	assert.Equal(t, float64(0), stateOf(t, h)["revision"])
}

// TestIntegration_AScriptCannotResetStateFromInsideARun closes the door beside
// the one save_state guards: the tool's set and clear are refused to a run.
func TestIntegration_AScriptCannotResetStateFromInsideARun(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `platform.call("manage_script", {"command": "state", "name": "daily", "state_action": "clear"})`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.Contains(t, out["error"], "cannot be called from inside a script run")
}
