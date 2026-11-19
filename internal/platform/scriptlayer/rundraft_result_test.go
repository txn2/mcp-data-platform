package scriptlayer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestHandle_DraftExports hands the portal editor the same writer the tool's
// drafts use (#1822), and a nil Handle none.
func TestHandle_DraftExports(t *testing.T) {
	var none *Handle
	assert.Nil(t, none.DraftExports())

	called := false
	h := New(Config{DraftExports: func(scriptdraft.Target) scriptrun.Exporter {
		called = true
		return nil
	}})
	exports := h.DraftExports()
	if assert.NotNil(t, exports) {
		exports(scriptdraft.Target{})
	}
	assert.True(t, called, "the writer handed out is the one the layer was given")
}

// TestRunResult_Checkpoint marks a run's saved state as its checkpoint.
func TestRunResult_Checkpoint(t *testing.T) {
	sc := &script.Script{ID: "sc_1", Name: "ingest"}
	run := &script.Run{
		ID: "run_1", Status: script.RunStatusFailed,
		StateWritten: map[string]any{"through": 4}, StateRevisionWritten: 3, StateCheckpoint: true,
	}
	out := runResult(sc, run)
	assert.Equal(t, true, out["state_checkpoint"])
	assert.Equal(t, map[string]any{"through": 4}, out["state_written"])

	run.StateCheckpoint = false
	assert.NotContains(t, runResult(sc, run), "state_checkpoint")
}

// TestAddGateNotes_Warnings hands the author what the lint warned about on a
// save it went through with.
func TestAddGateNotes_Warnings(t *testing.T) {
	out := map[string]any{}
	addGateNotes(out, "src", scriptsave.Result{Lint: scriptlint.Result{
		Source:   "src",
		Warnings: []scriptrun.Finding{{Rule: scriptlint.RuleStateDiscardedOnFail, Line: 3}},
	}})
	assert.Len(t, out["warnings"], 1)

	none := map[string]any{}
	addGateNotes(none, "src", scriptsave.Result{Lint: scriptlint.Result{Source: "src"}})
	assert.NotContains(t, none, "warnings")
}
