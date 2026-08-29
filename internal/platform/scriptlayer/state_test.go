package scriptlayer

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// manage_script command=state (#1537): read, replace and clear the one object
// a script carries between runs.

// stateScript creates jane's script and returns the handle and store.
func stateScript(t *testing.T) (*Handle, *memStore) {
	t.Helper()
	h, store := newHandle()
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdCreate, Name: "sync", Source: "print(run.state)"})
	require.False(t, res.IsError, resultText(res))
	return h, store
}

func TestState_GetIsTheDefaultAndReportsAnEmptyObjectAtRevisionZero(t *testing.T) {
	h, _ := stateScript(t)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdState, Name: "sync"})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, map[string]any{}, fields["state"], "a script that never saved has {} rather than null")
	assert.Equal(t, float64(0), fields["revision"])
	assert.NotContains(t, fields, "written_by", "nobody wrote revision 0")
}

func TestState_SetReplacesTheWholeObjectAsTheCaller(t *testing.T) {
	h, store := stateScript(t)
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdState, Name: "sync", StateAction: stateActionSet,
		State: map[string]any{"synced_through": "2026-08-01"},
	})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, float64(1), fields["revision"])
	assert.Equal(t, "jane@example.com", fields["written_by"])
	assert.Contains(t, fields["message"], "fails at its write")
	assert.Equal(t, "2026-08-01", store.states["script_1"].Value["synced_through"])

	got := call(t, h, authorCtx(), manageScriptInput{Command: cmdState, Name: "sync", StateAction: stateActionGet})
	assert.Equal(t, map[string]any{"synced_through": "2026-08-01"}, resultFields(t, got)["state"])
}

func TestState_ClearResetsToAnEmptyObjectAndMovesTheRevision(t *testing.T) {
	h, store := stateScript(t)
	_, err := store.SetState(context.Background(), "script_1", map[string]any{"synced_through": "2026-08-01"}, "jane@example.com")
	require.NoError(t, err)

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdState, Name: "sync", StateAction: stateActionClear})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, map[string]any{}, fields["state"])
	assert.Equal(t, float64(2), fields["revision"], "a clear is a write: it moves the revision so a run in flight fails at its write")
	assert.Contains(t, fields["message"], "starts from {}")
}

func TestState_AnAdministratorReachesAnotherPersonsState(t *testing.T) {
	h, _ := stateScript(t)
	res := call(t, h, adminCtx(), manageScriptInput{
		Command: cmdState, Name: "sync", OwnerEmail: "jane@example.com", StateAction: stateActionClear,
	})
	require.False(t, res.IsError, resultText(res))
}

func TestState_AStrangerIsAnsweredNotFound(t *testing.T) {
	h, _ := stateScript(t)
	res := call(t, h, callerCtx("bob@example.com", "analyst"), manageScriptInput{Command: cmdState, Name: "sync"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")
}

func TestState_Refusals(t *testing.T) {
	h, store := stateScript(t)
	tests := []struct {
		name  string
		input manageScriptInput
		want  string
	}{
		{"set without an object", manageScriptInput{Command: cmdState, Name: "sync", StateAction: stateActionSet}, "state is required"},
		{"an unknown action", manageScriptInput{Command: cmdState, Name: "sync", StateAction: "bump"}, "unknown state_action"},
		{"an oversized object", manageScriptInput{
			Command: cmdState, Name: "sync", StateAction: stateActionSet,
			State: map[string]any{"blob": strings.Repeat("x", script.MaxStateBytes)},
		}, "over the"},
		{"no name", manageScriptInput{Command: cmdState}, "name is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := call(t, h, authorCtx(), tt.input)
			require.True(t, res.IsError, resultText(res))
			assert.Contains(t, resultText(res), tt.want)
		})
	}
	assert.Empty(t, store.states, "nothing was written")
}

func TestState_StoreFailuresAreReportedWithoutDetail(t *testing.T) {
	h, store := stateScript(t)
	store.stateErr = errors.New("connection reset")
	for _, action := range []string{stateActionGet, stateActionClear} {
		res := call(t, h, authorCtx(), manageScriptInput{Command: cmdState, Name: "sync", StateAction: action})
		require.True(t, res.IsError)
		assert.NotContains(t, resultText(res), "connection reset")
	}
}

// TestState_WritesAreRefusedFromInsideARun pins that a run's one way to write
// state is platform.save_state under the compare-and-set: a run that could
// reset state through the tool would step around it, and could reset another
// script's state for the next run to read.
func TestState_WritesAreRefusedFromInsideARun(t *testing.T) {
	h, _ := stateScript(t)
	pc := middleware.GetPlatformContext(authorCtx())
	inRun := *pc
	inRun.Source = middleware.SourceScript
	ctx := middleware.WithPlatformContext(context.Background(), &inRun)

	for _, action := range []string{stateActionSet, stateActionClear} {
		res := call(t, h, ctx, manageScriptInput{
			Command: cmdState, Name: "sync", StateAction: action, State: map[string]any{},
		})
		require.True(t, res.IsError, action)
		assert.Contains(t, resultText(res), "cannot be called from inside a script run")
	}
	res := call(t, h, ctx, manageScriptInput{Command: cmdState, Name: "sync"})
	assert.False(t, res.IsError, "reading is still admitted: it changes nothing")
}

func TestState_UnavailableWithoutAStateStore(t *testing.T) {
	h := New(Config{Store: storeWithoutState{newMemStore()}, AdminPersona: "admin"})
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdState, Name: "sync"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "keeps no script state")
}

// storeWithoutState is a script.Store that is not a script.StateStore, which
// is the shape of a store that predates state. Embedding the interface rather
// than the fake keeps the fake's state methods off the type.
type storeWithoutState struct{ script.Store }

func TestStateSchema_AdvertisesTheActions(t *testing.T) {
	schema, ok := manageScriptSchema().(map[string]any)
	require.True(t, ok)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	action, ok := props["state_action"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{stateActionGet, stateActionSet, stateActionClear}, action[keyEnum])
	state, ok := props["state"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, state[keyDescription], strconv.Itoa(script.MaxStateBytes))
}

// liveState is what a draft reads: the live state, nil where none is kept or
// the read failed, which the run reads as {}.
func TestLiveState(t *testing.T) {
	h, store := stateScript(t)
	sc, err := store.GetByID(context.Background(), "script_1")
	require.NoError(t, err)

	assert.Empty(t, h.liveState(context.Background(), sc), "nothing saved reads as the empty object")
	_, err = store.SetState(context.Background(), sc.ID, map[string]any{"k": "v"}, "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"k": "v"}, h.liveState(context.Background(), sc))

	store.stateErr = errors.New("boom")
	assert.Nil(t, h.liveState(context.Background(), sc), "a failed read leaves the draft reading {} rather than failing it")

	none := New(Config{Store: storeWithoutState{newMemStore()}, AdminPersona: "admin"})
	assert.Nil(t, none.liveState(context.Background(), sc))
}

func TestOrEmptyParams(t *testing.T) {
	assert.Equal(t, map[string]any{}, orEmptyParams(nil))
	assert.Equal(t, map[string]any{"k": 1}, orEmptyParams(map[string]any{"k": 1}))
}
