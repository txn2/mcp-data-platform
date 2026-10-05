package script

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateState(t *testing.T) {
	t.Run("a JSON object under the bound is accepted", func(t *testing.T) {
		require.NoError(t, ValidateState(map[string]any{
			"synced_through": "2026-08-28T06:00:00Z", "count": 3, "ids": []any{"a", "b"}, "nested": map[string]any{"k": true},
		}))
		require.NoError(t, ValidateState(nil), "no state is a valid empty object")
	})

	t.Run("a value that is not JSON-representable is refused naming its key", func(t *testing.T) {
		err := ValidateState(map[string]any{"ok": 1, "bad": make(chan int)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `state key "bad"`)
	})

	t.Run("the whole object is bounded", func(t *testing.T) {
		err := ValidateState(map[string]any{"blob": strings.Repeat("x", MaxStateBytes)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "over the")
		assert.Contains(t, err.Error(), "keep a table as a resource")
	})
}

func TestState_WrittenBy(t *testing.T) {
	assert.Equal(t, "nobody", (*State)(nil).WrittenBy())
	assert.Equal(t, "nobody", EmptyState("s").WrittenBy())
	assert.Equal(t, "run dpx_1", (&State{RunID: "dpx_1"}).WrittenBy())
	assert.Equal(t, "jane@example.com", (&State{UpdatedBy: "jane@example.com"}).WrittenBy())
	assert.Equal(t, map[string]any{}, EmptyState("s").Value, "the empty state is an object, never nil")
}

func TestStateConflictError_NamesTheWriterAndMatchesTheSentinel(t *testing.T) {
	err := &StateConflictError{Read: 2, Current: 3, WrittenBy: "run dpx_9"}
	assert.ErrorIs(t, err, ErrStateConflict)
	assert.Contains(t, err.Error(), "read revision 2")
	assert.Contains(t, err.Error(), "run dpx_9 wrote revision 3")
	assert.Contains(t, err.Error(), "outputs stand")
	var typed *StateConflictError
	assert.True(t, errors.As(err, &typed))
}

func TestContractStateOf(t *testing.T) {
	at := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	t.Run("revision 0 has no timestamp", func(t *testing.T) {
		cs := ContractStateOf(StateUse{}, EmptyState("s"))
		assert.Zero(t, cs.Revision)
		assert.Nil(t, cs.UpdatedAt)
		assert.Equal(t, "State: keeps none; nothing has been saved.", cs.line(false))
	})
	t.Run("a saved state carries its revision and time", func(t *testing.T) {
		cs := ContractStateOf(StateUse{Reads: true, Saves: true}, &State{Revision: 3, UpdatedAt: at})
		assert.Equal(t, int64(3), cs.Revision)
		require.NotNil(t, cs.UpdatedAt)
		assert.Equal(t, "State: reads and saves state, so a run continues from the previous run's save; revision 3, last changed 2026-08-28 06:00 UTC.", cs.line(false))
	})
	t.Run("the lopsided uses are named", func(t *testing.T) {
		assert.Contains(t, ContractStateOf(StateUse{Saves: true}, nil).line(false), "saves state and never reads it")
		assert.Contains(t, ContractStateOf(StateUse{Reads: true}, nil).line(false), "reads state and never saves it")
		assert.Empty(t, (*ContractState)(nil).line(false))
	})
	t.Run("the contract text carries the line", func(t *testing.T) {
		c := BuildContract(liveScript(), nil, nil)
		assert.NotContains(t, c.Text(), "State:", "a contract composed without a state read says nothing about it")
		c.State = ContractStateOf(StateUse{Saves: true}, &State{Revision: 1, UpdatedAt: at})
		assert.Contains(t, c.Text(), "State: saves state and never reads it; revision 1")
	})
}

func TestContractForReader_WithholdsRunsAndStateFromOthers(t *testing.T) {
	finished := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	c := BuildContract(
		&Script{ID: "s1", Name: "daily", OwnerEmail: "o@example.com", Enabled: true, Status: StatusActive, Version: 2, Source: "def main():\n    pass\n"},
		nil,
		&Run{Version: 2, FinishedAt: &finished, Outputs: []RunOutput{{Name: "private-report", AssetID: "a1", AssetVersion: 1}}},
	)
	c.State = ContractStateOf(StateUse{Reads: true, Saves: true}, &State{Revision: 3, UpdatedAt: finished})
	assert.Equal(t, "def main():\n    pass\n", c.Source)

	owner := c.ForReader(true)
	assert.Equal(t, c, owner, "the owner and administrators read it whole")
	assert.Contains(t, owner.Text(), "private-report")

	other := c.ForReader(false)
	assert.True(t, other.RunsWithheld)
	assert.Nil(t, other.LastRun)
	require.NotNil(t, other.State)
	assert.Zero(t, other.State.Revision)
	assert.Nil(t, other.State.UpdatedAt)
	assert.True(t, other.State.Reads && other.State.Saves, "what the source does with state is the definition")
	text := other.Text()
	assert.NotContains(t, text, "private-report")
	assert.NotContains(t, text, "revision 3")
	assert.Contains(t, text, "Last successful run: shown to the script's owner and administrators.")
	assert.Contains(t, text, "what it saved is shown to the script's owner and administrators.")
	assert.NotNil(t, c.LastRun, "ForReader leaves the contract it was given alone")
}
