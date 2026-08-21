package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Running a script from its own page (#1363). The route queues a run of the
// script's CURRENT version — its latest saved one — so the assertions here are
// about what got queued, what it was bound against, and which requests the run
// gate refuses before anything reaches the queue.

// runPath is the route under test, on the global script carol owns.
const runPath = "/api/v1/portal/scripts/script_2/runs"

// queueingRuns records what was enqueued, so a test asserts on the run the
// route created rather than on a status code that would pass either way.
type queueingRuns struct {
	*stubRuns
	queued     *script.Run
	enqueueErr error
}

func (q *queueingRuns) Enqueue(_ context.Context, r *script.Run) error {
	if q.enqueueErr != nil {
		return q.enqueueErr
	}
	r.Status = script.RunStatusPending
	q.queued = r
	return nil
}

// runDeps assembles the portal deps with an enqueueing run store.
func runDeps(store *stubStore, user *PortalIdentity) (Deps, *queueingRuns) {
	runs := &queueingRuns{stubRuns: &stubRuns{}}
	deps := portalDeps(store, runs.stubRuns, nil, user)
	deps.Runs = runs
	return deps, runs
}

// runnableStore is the fixture every admitted run starts from: the global
// script's current version is 3, and that version declares one string
// parameter.
func runnableStore() *stubStore {
	store := portalStore()
	store.scripts[1].Version = 3
	store.version = &script.Version{
		ID: "sver_3", ScriptID: "script_2", Version: 3, Source: reportSource,
		Author: "carol@example.com", AuthorRoles: []string{"dp_analyst"},
		Status: script.VersionStatusApplied,
		Params: []script.Param{{Name: "source", Type: script.ParamTypeString, Required: true}},
	}
	return store
}

// TestPortalRunScript_QueuesTheCurrentVersion pins the version a run executes:
// the script's latest saved one, loaded by the number the live row carries.
func TestPortalRunScript_QueuesTheCurrentVersion(t *testing.T) {
	store := runnableStore()
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var body runResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, script.RunStatusPending, body.Status)
	assert.Equal(t, 3, body.Version)

	require.NotNil(t, runs.queued, "nothing was queued")
	assert.Equal(t, body.RunID, runs.queued.ID)
	assert.Equal(t, "sver_3", runs.queued.VersionID, "the run must name the CURRENT version")
	assert.Equal(t, 3, runs.queued.Version)
	assert.Equal(t, "warehouse", runs.queued.Params["source"])
	assert.Equal(t, "carol@example.com", runs.queued.RequestedBy)
}

// TestPortalRunScript_RecordsThePortalAsTheTrigger pins the label the owner
// reads in their own run history: they asked for this run on this page, and
// recording it as an agent's tool call would be a false statement about who
// did what.
func TestPortalRunScript_RecordsThePortalAsTheTrigger(t *testing.T) {
	deps, runs := runDeps(runnableStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotNil(t, runs.queued)
	assert.Equal(t, script.TriggerPortal, runs.queued.Trigger)
}

// TestPortalRunScript_RefusesAValueTheContractRejects covers the ordinary bind
// failure: the contract's own message, at the surface that asked for it.
func TestPortalRunScript_RefusesAValueTheContractRejects(t *testing.T) {
	store := runnableStore()
	store.version.Params = []script.Param{
		{Name: "report_date", Type: script.ParamTypeDate, Required: true},
	}
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"report_date":"not-a-date"}}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "report_date")
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_SurfacesTheRunGatesRefusalVerbatim proves the gate is
// asked rather than approximated: the response carries script.RefuseRun's own
// words, so this route and the contract document cannot disagree about whether
// a run would happen.
func TestPortalRunScript_SurfacesTheRunGatesRefusalVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*script.Script)
	}{
		{"disabled", func(sc *script.Script) { sc.Enabled = false }},
		{"deprecated", func(sc *script.Script) { sc.Status = script.StatusDeprecated }},
		{"superseded", func(sc *script.Script) {
			sc.Status, sc.SupersededBy = script.StatusSuperseded, "shared-report-v2"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := runnableStore()
			tc.mutate(&store.scripts[1])
			want := script.RefuseRun(&store.scripts[1])
			require.Error(t, want, "the fixture must be one the gate refuses")

			deps, runs := runDeps(store, carol)
			rec := servePortalRequest(t, deps, http.MethodPost, runPath,
				`{"params":{"source":"warehouse"}}`)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, want.Error(), decode(t, rec)["detail"],
				"the refusal must be the gate's, verbatim")
			assert.Nil(t, runs.queued, "a refused run must not be queued")
		})
	}
}

// TestPortalRunScript_RefusesACallerWhoDoesNotOwnIt answers not-yours exactly
// as does-not-exist, for the same reason every other owner route does.
func TestPortalRunScript_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	deps, runs := runDeps(runnableStore(), stranger)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_TakesNoBodyForAScriptThatNeedsNoValues keeps the simplest
// request simple: a script whose parameters are all optional is run by asking.
func TestPortalRunScript_TakesNoBodyForAScriptThatNeedsNoValues(t *testing.T) {
	store := runnableStore()
	store.version.Params = nil
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, "")

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotNil(t, runs.queued)
	assert.Empty(t, runs.queued.Params)
}

func TestPortalRunScript_RefusesAnUnreadableBody(t *testing.T) {
	deps, runs := runDeps(runnableStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{"params":`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, runs.queued)
}

func TestPortalRunScript_ReportsAFailedEnqueue(t *testing.T) {
	deps, runs := runDeps(runnableStore(), carol)
	runs.enqueueErr = errors.New("the queue is unavailable")
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "failed to queue")
}

func TestPortalRunScript_ReportsAnUnreadableCurrentVersion(t *testing.T) {
	store := runnableStore()
	store.versionErr = errors.New("the version store is unavailable")
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_ReportsAMissingCurrentVersion covers the row the live
// script points at not existing in its history, which is the platform's own
// inconsistency rather than the caller's request.
func TestPortalRunScript_ReportsAMissingCurrentVersion(t *testing.T) {
	store := runnableStore()
	store.version.Version = 2
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing from its history")
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_IsUnmountedWithoutARunStore keeps a deployment that keeps
// no runs from serving a control that has nowhere to put one.
func TestPortalRunScript_IsUnmountedWithoutARunStore(t *testing.T) {
	deps := portalDeps(runnableStore(), nil, nil, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{}`)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "about:blank",
		"the route must be absent from the mux, not answering from the handler")
}
