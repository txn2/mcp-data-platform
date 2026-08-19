package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Running a script from its own page (#1363). The route queues a run of the
// APPROVED version and nothing else, so the assertions here are about what got
// queued, what it was bound against, and which requests are refused before
// anything reaches the queue.

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

// grantedStore is the fixture every admitted run starts from: the global script
// is executable, and its approved version declares one connection parameter
// granted exactly one connection.
func grantedStore() *stubStore {
	store := approvedPortalStore()
	store.version.Params = []script.Param{
		{Name: "source", Type: script.ParamTypeConnection, Required: true},
	}
	store.version.Grants = script.Grants{
		Roles: []string{"analyst"}, Connections: []string{"warehouse"},
		Capabilities: []string{script.CapabilityQuery},
	}
	store.version.Version = 3
	approvedAt := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	store.version.ApprovedAt = &approvedAt
	return store
}

func TestPortalRunScript_QueuesTheApprovedVersion(t *testing.T) {
	store := grantedStore()
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
	assert.Equal(t, "sver_1", runs.queued.VersionID, "the run must name the APPROVED version")
	assert.Equal(t, "warehouse", runs.queued.Params["source"])
	assert.Equal(t, "carol@example.com", runs.queued.RequestedBy)
}

// TestPortalRunScript_RecordsThePortalAsTheTrigger pins the label the owner
// reads in their own run history: they asked for this run on this page, and
// recording it as an agent's tool call would be a false statement about who
// did what.
func TestPortalRunScript_RecordsThePortalAsTheTrigger(t *testing.T) {
	deps, runs := runDeps(grantedStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotNil(t, runs.queued)
	assert.Equal(t, script.TriggerPortal, runs.queued.Trigger)
}

// TestPortalRunScript_RefusesAConnectionTheGrantDoesNotCover is the reason the
// connection parameter type exists (#1361): the failure lands on the person
// who typed the value, not on a run they stopped watching.
func TestPortalRunScript_RefusesAConnectionTheGrantDoesNotCover(t *testing.T) {
	deps, runs := runDeps(grantedStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehosue"}}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "warehouse", "the refusal must name what it may reach")
	assert.Nil(t, runs.queued, "a run that cannot succeed must not be queued")
}

// TestPortalRunScript_RefusesAValueTheContractRejects covers the ordinary bind
// failure: the contract's own message, at the surface that asked for it.
func TestPortalRunScript_RefusesAValueTheContractRejects(t *testing.T) {
	store := grantedStore()
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

// TestPortalRunScript_RefusesWhenNothingIsApproved is the gate's own answer,
// verbatim: the route must not be able to run what run_script would decline.
func TestPortalRunScript_RefusesWhenNothingIsApproved(t *testing.T) {
	store := portalStore()
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "no approved version")
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_RefusesADisabledScript proves the gate is asked rather
// than approximated: a disabled script has an approved version and still must
// not run.
func TestPortalRunScript_RefusesADisabledScript(t *testing.T) {
	store := grantedStore()
	store.scripts[1].Enabled = false
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "disabled")
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_RefusesACallerWhoDoesNotOwnIt answers not-yours exactly
// as does-not-exist, for the same reason every other owner route does.
func TestPortalRunScript_RefusesACallerWhoDoesNotOwnIt(t *testing.T) {
	deps, runs := runDeps(grantedStore(), stranger)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_TakesNoBodyForAScriptThatNeedsNoValues keeps the simplest
// request simple: a script whose parameters are all optional is run by asking.
func TestPortalRunScript_TakesNoBodyForAScriptThatNeedsNoValues(t *testing.T) {
	store := grantedStore()
	store.version.Params = nil
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, "")

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.NotNil(t, runs.queued)
	assert.Empty(t, runs.queued.Params)
}

func TestPortalRunScript_RefusesAnUnreadableBody(t *testing.T) {
	deps, runs := runDeps(grantedStore(), carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{"params":`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Nil(t, runs.queued)
}

func TestPortalRunScript_ReportsAFailedEnqueue(t *testing.T) {
	deps, runs := runDeps(grantedStore(), carol)
	runs.enqueueErr = errors.New("the queue is unavailable")
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "failed to queue")
}

func TestPortalRunScript_ReportsAnUnreadableApprovedVersion(t *testing.T) {
	store := grantedStore()
	store.versionErr = errors.New("the version store is unavailable")
	deps, runs := runDeps(store, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath,
		`{"params":{"source":"warehouse"}}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Nil(t, runs.queued)
}

// TestPortalRunScript_IsUnmountedWithoutARunStore keeps a deployment that keeps
// no runs from serving a control that has nowhere to put one.
func TestPortalRunScript_IsUnmountedWithoutARunStore(t *testing.T) {
	deps := portalDeps(grantedStore(), nil, nil, carol)
	rec := servePortalRequest(t, deps, http.MethodPost, runPath, `{}`)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "about:blank",
		"the route must be absent from the mux, not answering from the handler")
}
