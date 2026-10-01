package scriptdraft

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestOutcomeState holds a draft to what a platform run commits (#2002, #2003).
func TestOutcomeState(t *testing.T) {
	saved := &script.StateWrite{Value: map[string]any{"through": 3}}
	cp := &script.StateWrite{Value: map[string]any{"through": 2}, Checkpoint: true}
	failed := errors.New("page 3 would not answer")

	ok := (&Outcome{Result: &scriptrun.Result{State: saved, Checkpoint: cp}}).State()
	assert.Equal(t, DraftState{Committed: saved.Value}, ok, "a successful run's save_state replaces its checkpoint")

	onlyCP := (&Outcome{Result: &scriptrun.Result{Checkpoint: cp}}).State()
	assert.Equal(t, DraftState{Committed: cp.Value, Checkpoint: true}, onlyCP)

	bad := (&Outcome{Result: &scriptrun.Result{State: saved, Checkpoint: cp}, Err: failed}).State()
	assert.Equal(t, DraftState{Committed: cp.Value, Checkpoint: true, Discarded: saved.Value}, bad)

	assert.Equal(t, DraftState{}, (*Outcome)(nil).State())
	assert.Equal(t, DraftState{}, (&Outcome{}).State())

	msg := (&Outcome{Result: &scriptrun.Result{State: saved, Checkpoint: cp}, Err: failed}).Persisted("draft")
	assert.Contains(t, msg, "state_discarded")
	assert.Contains(t, msg, "platform.checkpoint reported")
}
