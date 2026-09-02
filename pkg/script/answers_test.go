package script_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestDeleteMessage_NamesOnlyWhatTheScriptHad is the criterion the whole
// composition exists for: the account of a delete states what actually went,
// so a script that was never scheduled and carried no state is not reported as
// having lost either.
func TestDeleteMessage_NamesOnlyWhatTheScriptHad(t *testing.T) {
	tests := []struct {
		name    string
		removed script.Removed
		want    string
	}{
		{
			name:    "everything",
			removed: script.Removed{Schedule: true, Runs: true, State: true},
			want:    "daily is gone, with its saved versions, its schedule, its run history and the state it carried.",
		},
		{
			name:    "nothing but its own history",
			removed: script.Removed{},
			want:    "daily is gone, with its saved versions.",
		},
		{
			name:    "ran but was never scheduled",
			removed: script.Removed{Runs: true},
			want:    "daily is gone, with its saved versions and its run history.",
		},
		{
			name:    "scheduled and carried state, never ran",
			removed: script.Removed{Schedule: true, State: true},
			want:    "daily is gone, with its saved versions, its schedule and the state it carried.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := script.DeleteMessage("daily", tt.removed)
			assert.Contains(t, got, tt.want)
			assert.Contains(t, got,
				"The assets and resources it wrote remain, and they still record that it wrote them.",
				"what stayed is the half a person is most likely to be wrong about, so it is always stated")
		})
	}
}

// TestDeleteMessage_DoesNotNameWhatWasNotThere pins the negative directly: the
// clauses are absent, not merely reordered.
func TestDeleteMessage_DoesNotNameWhatWasNotThere(t *testing.T) {
	got := script.DeleteMessage("daily", script.Removed{})
	assert.NotContains(t, got, "schedule")
	assert.NotContains(t, got, "run history")
	assert.NotContains(t, got, "state it carried")
}

// TestSavedMessage covers the one thing a save has to tell its author: whether
// anything will execute what they just saved.
func TestSavedMessage(t *testing.T) {
	runnable := &script.Script{Name: "daily", Enabled: true, Status: script.StatusActive}
	assert.Contains(t, script.SavedMessage(runnable), "this version is what runs now")

	disabled := &script.Script{Name: "daily", Status: script.StatusActive}
	assert.Contains(t, script.SavedMessage(disabled), "Nothing executes this script: the script is disabled.")

	deprecated := &script.Script{Name: "daily", Enabled: true, Status: script.StatusDeprecated}
	assert.Contains(t, script.SavedMessage(deprecated), "deprecated")
	assert.NotContains(t, script.SavedMessage(deprecated), "runs now")

	superseded := &script.Script{
		Name: "daily", Enabled: true, Status: script.StatusSuperseded, SupersededBy: "weekly",
	}
	assert.Contains(t, script.SavedMessage(superseded), `superseded by "weekly"`)
}

// TestStateResetMessage holds the two halves apart: what the next run reads,
// and what happens to a run already in flight.
func TestStateResetMessage(t *testing.T) {
	cleared := script.StateResetMessage(true)
	assert.Contains(t, cleared, "State cleared.")
	assert.Contains(t, cleared, "starts from {}")

	replaced := script.StateResetMessage(false)
	assert.Contains(t, replaced, "State replaced.")
	assert.Contains(t, replaced, "reads this object")

	for _, msg := range []string{cleared, replaced} {
		assert.Contains(t, msg, "a run already in flight that read the previous revision fails at its write",
			"the in-flight failure is correct rather than a fault, and the caller has to be told so")
	}
}
