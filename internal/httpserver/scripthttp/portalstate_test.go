package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// A script's state on its page (#1537): read by its owner and an
// administrator, reset by the same two, and read by a draft.

const statePath = "/api/v1/portal/scripts/script_2/state"

// stubStates is the state store: one state per script, reset by SetState with
// the revision moved, and failing on demand.
type stubStates struct {
	states map[string]*script.State
	err    error
	// setBy records who the last reset was attributed to.
	setBy string
}

func newStubStates() *stubStates { return &stubStates{states: map[string]*script.State{}} }

func (s *stubStates) GetState(_ context.Context, scriptID string) (*script.State, error) {
	if s.err != nil {
		return nil, s.err
	}
	if st, ok := s.states[scriptID]; ok {
		return st, nil
	}
	return script.EmptyState(scriptID), nil
}

func (s *stubStates) SetState(_ context.Context, scriptID string, value map[string]any, by string) (*script.State, error) {
	if s.err != nil {
		return nil, s.err
	}
	var revision int64
	if prior, ok := s.states[scriptID]; ok {
		revision = prior.Revision
	}
	s.setBy = by
	st := &script.State{ScriptID: scriptID, Value: value, Revision: revision + 1, UpdatedBy: by, UpdatedAt: time.Now().UTC()}
	s.states[scriptID] = st
	return st, nil
}

// stateDeps assembles the portal deps with a state store for one caller.
func stateDeps(states *stubStates, user *PortalIdentity) Deps {
	deps := portalDeps(portalStore(), nil, nil, user)
	deps.States = states
	return deps
}

// The state is the owner's and an administrator's, refused to everybody else
// with the same answer as a script that does not exist.
func TestPortalState_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := servePortalRequest(t, stateDeps(newStubStates(), stranger), method, statePath, `{"state":{}}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, method)
	}
}

func TestPortalState_UnmountedWithoutAStateStore(t *testing.T) {
	rec := servePortalRequest(t, portalDeps(portalStore(), nil, nil, carol), http.MethodGet, statePath, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The run detail carries the state the run read and what it wrote (#1537), so
// a run explains itself from its own row.
func TestPortalGetRun_CarriesTheStateReadAndWritten(t *testing.T) {
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_1", ScriptID: "script_1", Status: script.RunStatusSucceeded,
		StateRevision: 2, StateRead: map[string]any{"synced_through": "2026-08-27"},
		StateWritten: map[string]any{"synced_through": "2026-08-28"}, StateRevisionWritten: 3,
	}, {
		ID: "run_0", ScriptID: "script_1", Status: script.RunStatusSucceeded,
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs/run_1")
	require.Equal(t, http.StatusOK, rec.Code)
	var run portalRunDetail
	decodeInto(t, rec, &run)
	assert.Equal(t, int64(2), run.StateRevision)
	assert.Equal(t, "2026-08-27", run.StateRead["synced_through"])
	assert.Equal(t, "2026-08-28", run.StateWritten["synced_through"])
	assert.Equal(t, int64(3), run.StateRevisionWritten)

	rec = servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs/run_0")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"state_read":{}`, "a run written before state existed read the empty object")
	assert.NotContains(t, rec.Body.String(), "state_written", "a run that saved nothing carries no written state")
}

// A draft reads the live state and reports what it would have saved, and the
// account keeps it beside the outputs; nothing is written to the state.
func TestPortalDryRunSource_ReadsLiveStateAndReportsWhatItWouldHaveSaved(t *testing.T) {
	deps, runner, accounts := draftDeps(portalStore(), carol)
	states := newStubStates()
	states.states["script_2"] = &script.State{ScriptID: "script_2", Value: map[string]any{"synced_through": "2026-08-27"}, Revision: 1}
	deps.States = states
	runner.outcome = &scriptdraft.Outcome{
		RunID:  "run_draft_1",
		Result: &scriptrun.Result{State: &script.StateWrite{Value: map[string]any{"synced_through": "2026-08-28"}}},
	}

	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "2026-08-27", runner.got.State["synced_through"], "the draft reads the live state")
	var body dryRunResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, "2026-08-28", body.State["synced_through"])
	assert.Contains(t, body.Message, "did not save it")
	require.NotNil(t, accounts.recorded)
	assert.Equal(t, "2026-08-28", accounts.recorded.StateWritten["synced_through"])
	assert.Equal(t, int64(1), states.states["script_2"].Revision, "nothing was persisted")
}

func TestPortalDryRunSource_ReadsAnEmptyStateWhereNoneIsKept(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, runner.got.State)
	assert.NotContains(t, rec.Body.String(), `"state"`, "a draft that saved nothing reports no state")
}

func TestPortalDryRunSource_ReadsAnEmptyStateWhenTheReadFails(t *testing.T) {
	deps, runner, _ := draftDeps(portalStore(), carol)
	states := newStubStates()
	states.err = errors.New("boom")
	deps.States = states
	rec := servePortalRequest(t, deps, http.MethodPost, dryRunPath, draftBody(draftSource))
	require.Equal(t, http.StatusOK, rec.Code, "a failed state read does not fail the draft")
	assert.Nil(t, runner.got.State, "the draft reads {} rather than failing")
}
