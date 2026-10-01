package scriptlayer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestDraftResult_WithoutAnEngineResult covers the outcome of a run that never
// reached the interpreter: the response still has to describe itself, and every
// reader of it is walking a nil result.
func TestDraftResult_WithoutAnEngineResult(t *testing.T) {
	sc := &script.Script{ID: "sc_1", Name: "ingest"}

	failed := draftResult(sc, &scriptdraft.Outcome{RunID: "r1", Err: errors.New("boom")})
	assert.Equal(t, "failed", failed[fieldStatus])
	assert.Equal(t, "boom", failed["error"])
	assert.NotContains(t, failed, "refused_write", "nothing was refused, so nothing is named")
	assert.Contains(t, failed["message"], "fails the same way again, fix the script")
	assert.NotContains(t, failed["message"], "deterministic", "a script with I/O is not (#1935)")

	succeeded := draftResult(sc, &scriptdraft.Outcome{RunID: "r2"})
	assert.Equal(t, "succeeded", succeeded[fieldStatus])
	assert.Contains(t, succeeded["message"], "Nothing was persisted")
	assert.Equal(t, []scriptrun.WriteRecord{}, succeeded["writes"],
		"an empty answer is an empty list, not null")
	assert.NotContains(t, succeeded, "saved", "a draft of a saved script says nothing about saving")

	unsaved := draftResult(&script.Script{Name: "new"}, &scriptdraft.Outcome{RunID: "r3"})
	assert.Equal(t, false, unsaved["saved"], "a draft of a script not saved yet says so")
}

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

// TestDraftResult_State reports what a platform run would commit and what it
// would discard (#2002, #2003): a failed draft's save_state is not its state.
func TestDraftResult_State(t *testing.T) {
	sc := &script.Script{ID: "sc_1", Name: "ingest"}
	failed := draftResult(sc, &scriptdraft.Outcome{RunID: "r1", Err: errors.New("boom"), Result: &scriptrun.Result{
		State:      &script.StateWrite{Value: map[string]any{"through": 9}},
		Checkpoint: &script.StateWrite{Value: map[string]any{"through": 4}, Checkpoint: true},
	}})
	assert.Equal(t, map[string]any{"through": 4}, failed["state"])
	assert.Equal(t, true, failed["state_checkpoint"])
	assert.Equal(t, map[string]any{"through": 9}, failed["state_discarded"])

	saved := draftResult(sc, &scriptdraft.Outcome{RunID: "r2", Result: &scriptrun.Result{
		State: &script.StateWrite{Value: map[string]any{"through": 9}},
	}})
	assert.Equal(t, map[string]any{"through": 9}, saved["state"])
	assert.NotContains(t, saved, "state_checkpoint")
	assert.NotContains(t, saved, "state_discarded")
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
