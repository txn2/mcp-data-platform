package draftview

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestOfReportsWhatTheDraftDid renders the shapes a draft ends in: a success
// that saved state, a failure whose save_state a run would discard beside the
// checkpoint it would keep, and a run the engine never produced a result for.
func TestOfReportsWhatTheDraftDid(t *testing.T) {
	saved := Of(&scriptdraft.Outcome{
		RunID: "dpx_1", Recorded: true,
		Result: &scriptrun.Result{
			Log:    "hello\n",
			State:  &script.StateWrite{Value: map[string]any{"through": 2}},
			Writes: []scriptrun.WriteRecord{{Tool: "manage_asset"}},
		},
	})
	assert.Equal(t, script.RunStatusSucceeded, saved.Status)
	assert.Equal(t, "dpx_1", saved.Recording)
	assert.Equal(t, "hello\n", saved.Log)
	assert.Equal(t, map[string]any{"through": 2}, saved.State)
	assert.False(t, saved.StateCheckpoint)
	assert.Nil(t, saved.StateDiscarded)
	assert.Len(t, saved.Writes, 1)

	failed := Of(&scriptdraft.Outcome{
		RunID: "dpx_2", Err: errors.New("upstream gave up"),
		Result: &scriptrun.Result{
			State:      &script.StateWrite{Value: map[string]any{"through": 9}},
			Checkpoint: &script.StateWrite{Value: map[string]any{"through": 4}, Checkpoint: true},
		},
	})
	assert.Equal(t, script.RunStatusFailed, failed.Status)
	assert.Equal(t, "upstream gave up", failed.Error)
	assert.NotEmpty(t, failed.Message)
	assert.Equal(t, map[string]any{"through": 4}, failed.State, "the checkpoint is what a run keeps")
	assert.True(t, failed.StateCheckpoint)
	assert.Equal(t, map[string]any{"through": 9}, failed.StateDiscarded)
	assert.Empty(t, failed.Recording)
	assert.NotNil(t, failed.Writes, "an empty list is [], never null")

	bare := Of(&scriptdraft.Outcome{RunID: "dpx_3", Err: errors.New("did not parse")})
	assert.Equal(t, script.RunStatusFailed, bare.Status)
	assert.Nil(t, bare.State)
	assert.NotNil(t, bare.Outputs)
	assert.Equal(t, map[string]any{}, orEmpty(nil))
}
