package scriptlayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/runcontrol"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// resultSource hands back an answer and reports its progress on the way.
const resultSource = `
res = platform.query(connection="warehouse", sql="SELECT 1")
platform.progress("counted", done=1, total=1)
platform.result({"total": 42, "day": run.params["day"]})
`

// TestIntegration_RunScriptHandsBackTheResult is #1845 over MCP: run_script
// and get_run both carry the value platform.result set, as JSON, beside the
// run's last progress report.
func TestIntegration_RunScriptHandsBackTheResult(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, resultSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "2026-09-22"}})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.Equal(t, map[string]any{"total": float64(42), "day": "2026-09-22"}, out["result"])

	got := awaitRunResult(ctx, t, session, str(out, "run_id"))
	assert.Equal(t, map[string]any{"total": float64(42), "day": "2026-09-22"}, got["result"])
	progress, ok := got["progress"].(map[string]any)
	require.True(t, ok, got)
	assert.Equal(t, "counted", progress["message"])
	assert.Equal(t, float64(1), progress["done"])
}

// TestIntegration_ADraftReportsTheResultAndProgress: a draft reports what a
// platform run would hand back, without persisting anything.
func TestIntegration_ADraftReportsTheResultAndProgress(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, resultSource)
	session := connectAgent(ctx, t, h.server)
	out, isErr := callTool(ctx, t, session, map[string]any{
		"command": cmdRunDraft, "name": "daily", "args": map[string]any{"day": "x"},
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, map[string]any{"total": float64(42), "day": "x"}, out["result"])
	assert.NotNil(t, out["progress"])
}

// TestIntegration_CancelRunOnAQueuedRun is #1847's cancel over MCP: a queued
// run is canceled and never starts, and canceling it again says it had
// already finished.
func TestIntegration_CancelRunOnAQueuedRun(t *testing.T) {
	ctx := context.Background()
	h := execServerWithWorker(t, false, "warehouse")
	authorScript(t, h, resultSource)
	session := connectAgent(ctx, t, h.server)

	queued, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}, "wait_seconds": -1})
	require.False(t, isErr, queued["error"])

	out, isErr := callTool(ctx, t, session, map[string]any{"command": cmdCancelRun, "run_id": str(queued, "run_id")})
	require.False(t, isErr, out["error"])
	assert.Equal(t, string(runcontrol.CanceledQueued), out["outcome"])
	assert.Contains(t, out["message"], "will not run")

	worker := startWorker(t, h)
	defer func() { _ = worker.Stop(ctx) }()
	got := awaitRunResult(ctx, t, session, str(queued, "run_id"))
	assert.Equal(t, script.RunStatusCanceled, got["status"])
	assert.Nil(t, got["result"], "a canceled run never executed")
	assert.Contains(t, got["message"], "canceled")

	again, isErr := callTool(ctx, t, session, map[string]any{"command": cmdCancelRun, "run_id": str(queued, "run_id")})
	require.False(t, isErr, again["error"])
	assert.Equal(t, string(runcontrol.CancelAlreadyFinished), again["outcome"])
	assert.Contains(t, again["message"], "already finished (canceled)")
}

func TestIntegration_CancelRunRefusals(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	session := connectAgent(ctx, t, h.server)
	out, isErr := callTool(ctx, t, session, map[string]any{"command": cmdCancelRun})
	require.True(t, isErr)
	assert.Contains(t, out["error"], "run_id is required")
	out, isErr = callTool(ctx, t, session, map[string]any{"command": cmdCancelRun, "run_id": "dpx_none"})
	require.True(t, isErr)
	assert.Contains(t, out["error"], "run not found")
}

// TestIntegration_AnExportToolsFileIsARunOutput is #1854's criterion: a run
// whose only output is a trino_export call lists that asset under outputs in
// run_script and get_run, marked with the tool that wrote it.
func TestIntegration_AnExportToolsFileIsARunOutput(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `
platform.call("trino_export", {"sql": "SELECT * FROM big", "name": "big-report", "format": "csv"})
`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{"name": "daily", "args": map[string]any{"day": "x"}})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	outputs, ok := out["outputs"].([]any)
	require.True(t, ok, out)
	require.Len(t, outputs, 1)
	first, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "trino_export", first["tool"])
	assert.Equal(t, "big-report", first["name"])
	assert.Equal(t, "asset-export-big-report", first["asset_id"])
	assert.Equal(t, float64(1), first["asset_version"])
	assert.Equal(t, float64(52000), first["row_count"])
	assert.Equal(t, "portal", first["destination"])

	got := awaitRunResult(ctx, t, session, str(out, "run_id"))
	assert.Equal(t, outputs, got["outputs"])
}
