package scriptdraft

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestReport_WithoutAnEngineResult covers the outcome of a run that never
// reached the interpreter: the response still has to describe itself, and every
// reader of it is walking a nil result.
func TestReport_WithoutAnEngineResult(t *testing.T) {
	sc := &script.Script{ID: "sc_1", Name: "ingest"}

	failed := Report(sc, &Outcome{RunID: "r1", Err: errors.New("boom")})
	assert.Equal(t, "failed", failed["status"])
	assert.Equal(t, "boom", failed["error"])
	assert.NotContains(t, failed, "refused_write", "nothing was refused, so nothing is named")
	assert.Contains(t, failed["message"], "fails the same way again, fix the script")
	assert.NotContains(t, failed["message"], "deterministic", "a script with I/O is not (#1935)")

	succeeded := Report(sc, &Outcome{RunID: "r2"})
	assert.Equal(t, "succeeded", succeeded["status"])
	assert.Contains(t, succeeded["message"], "Nothing was persisted")
	assert.Equal(t, []scriptrun.WriteRecord{}, succeeded["writes"],
		"an empty answer is an empty list, not null")
	assert.NotContains(t, succeeded, "saved", "a draft of a saved script says nothing about saving")

	unsaved := Report(&script.Script{Name: "new"}, &Outcome{RunID: "r3"})
	assert.Equal(t, false, unsaved["saved"], "a draft of a script not saved yet says so")
}

// TestReport_State reports what a platform run would commit and what it
// would discard (#2002, #2003): a failed draft's save_state is not its state.
func TestReport_State(t *testing.T) {
	sc := &script.Script{ID: "sc_1", Name: "ingest"}
	failed := Report(sc, &Outcome{RunID: "r1", Err: errors.New("boom"), Result: &scriptrun.Result{
		State:      &script.StateWrite{Value: map[string]any{"through": 9}},
		Checkpoint: &script.StateWrite{Value: map[string]any{"through": 4}, Checkpoint: true},
	}})
	assert.Equal(t, map[string]any{"through": 4}, failed["state"])
	assert.Equal(t, true, failed["state_checkpoint"])
	assert.Equal(t, map[string]any{"through": 9}, failed["state_discarded"])

	saved := Report(sc, &Outcome{RunID: "r2", Result: &scriptrun.Result{
		State: &script.StateWrite{Value: map[string]any{"through": 9}},
	}})
	assert.Equal(t, map[string]any{"through": 9}, saved["state"])
	assert.NotContains(t, saved, "state_checkpoint")
	assert.NotContains(t, saved, "state_discarded")
}
