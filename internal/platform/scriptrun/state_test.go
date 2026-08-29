package scriptrun

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The script's state (#1537): run.state on the way in, platform.save_state on
// the way out, and the validator's reading of both.

// executeWithState runs source with the state a caller pinned.
func executeWithState(t *testing.T, source string, state map[string]any) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "run_1", FireTime: fireTime,
		Caller: &recordingCaller{}, State: state,
	})
}

func TestRun_StateIsReadFromTheRunRecord(t *testing.T) {
	result, err := executeWithState(t, `
print(run.state.get("synced_through", "never"))
print(run.state["count"] + 1)
print(sorted(run.state.keys()))
`, map[string]any{"synced_through": "2026-08-28", "count": float64(2)})
	require.NoError(t, err)
	assert.Equal(t, "2026-08-28\n3\n[\"count\", \"synced_through\"]\n", result.Log, "a whole number crosses as an int")
	assert.Nil(t, result.State, "reading state stages no write")
}

func TestRun_AScriptThatNeverSavedReadsAnEmptyObject(t *testing.T) {
	result, err := executeWithState(t, `print(run.state.get("synced_through", "never"), len(run.state))`, nil)
	require.NoError(t, err)
	assert.Equal(t, "never 0\n", result.Log)
}

func TestRun_StateIsFrozen(t *testing.T) {
	_, err := executeWithState(t, `run.state["x"] = 1`, map[string]any{})
	require.Error(t, err, "the record a run explains itself from cannot be rewritten by the run")
	assert.Contains(t, err.Error(), "frozen")
}

func TestRun_SaveStateStagesTheLastValue(t *testing.T) {
	result, err := executeWithState(t, `
platform.save_state({"synced_through": "2026-08-27"})
platform.save_state({"synced_through": "2026-08-28", "rows": 3, "ids": ["a", "b"], "nested": {"ok": True}})
`, nil)
	require.NoError(t, err)
	require.NotNil(t, result.State)
	assert.Equal(t, map[string]any{
		"synced_through": "2026-08-28", "rows": int64(3), "ids": []any{"a", "b"}, "nested": map[string]any{"ok": true},
	}, result.State.Value, "calling save_state twice stages the last value; the run's write is one write")
}

func TestRun_SaveStateWithAnEmptyObjectIsAWrite(t *testing.T) {
	result, err := executeWithState(t, `platform.save_state({})`, map[string]any{"old": "value"})
	require.NoError(t, err)
	require.NotNil(t, result.State)
	assert.Equal(t, map[string]any{}, result.State.Value)
}

func TestRun_SaveStateRefusals(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{"a non-dict", `platform.save_state("mark")`, "in platform.save_state"},
		{"a value that cannot be JSON", `platform.save_state({"f": lambda x: x})`, `state key "f"`},
		{"a non-string key", `platform.save_state({1: "x"})`, "keys must be strings"},
		{"over the bound", `platform.save_state({"blob": "x" * ` + strconv.Itoa(script.MaxStateBytes) + `})`, "over the"},
		{"too many keys to fit", `platform.save_state({str(i): 1 for i in range(` + strconv.Itoa(script.MaxStateBytes/5+1) + `)})`, "more than the"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executeWithState(t, tt.source, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Nil(t, result.State, "a refused save stages nothing")
		})
	}
}

func TestRun_AFailedRunStillReportsWhatItStaged(t *testing.T) {
	// The engine reports the staged state on failure too; the store is what
	// declines to apply it. Keeping the report lets a draft's account say what
	// the code would have saved even when it then failed.
	result, err := executeWithState(t, `
platform.save_state({"synced_through": "2026-08-28"})
fail("export refused")
`, nil)
	require.Error(t, err)
	require.NotNil(t, result.State)
}

func TestValidate_ReportsStateUse(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		reads, saves bool
	}{
		{"neither", `print(run.fire_time)`, false, false},
		{"reads", `since = run.state.get("since", "")`, true, false},
		{"saves", `platform.save_state({"since": run.fire_time})`, false, true},
		{"both", "since = run.state.get(\"since\", \"\")\nplatform.save_state({\"since\": run.fire_time})\n", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Validate(tt.source)
			require.True(t, report.OK, report.Findings)
			assert.Equal(t, tt.reads, report.Reads)
			assert.Equal(t, tt.saves, report.Saves)
			if tt.saves {
				assert.Contains(t, report.Capabilities, CapabilitySaveState)
			}
		})
	}
}

func TestCapabilities_IncludeSaveState(t *testing.T) {
	assert.Contains(t, Capabilities, CapabilitySaveState)
	report := Validate(`platform.save_stat({})`)
	assert.False(t, report.OK, "a misspelled member is refused, naming the real set")
	assert.Contains(t, report.Findings[0].Hint, CapabilitySaveState)
}
