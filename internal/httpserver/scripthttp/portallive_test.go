package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/runcontrol"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// finishingRuns answers every read of the run it queued with that run in the
// state finish describes, standing in for the worker executing it elsewhere.
type finishingRuns struct {
	*queueingRuns
	finish func(*script.Run)
}

func (f *finishingRuns) GetRun(_ context.Context, id string) (*script.Run, error) {
	if f.queued == nil || f.queued.ID != id {
		return nil, script.ErrRunNotFound
	}
	out := *f.queued
	f.finish(&out)
	return &out, nil
}

func finishingDeps(finish func(*script.Run)) (Deps, *finishingRuns) {
	deps, queueing := runDeps(runnableStore(), carol)
	runs := &finishingRuns{queueingRuns: queueing, finish: finish}
	deps.Runs = runs
	return deps, runs
}

// TestPortalRunScript_WaitAnswersWithTheFinishedRun is #1845's first
// criterion: a run that finishes inside the wait answers 200 with the run,
// carrying the value it returned.
func TestPortalRunScript_WaitAnswersWithTheFinishedRun(t *testing.T) {
	deps, _ := finishingDeps(func(r *script.Run) {
		r.Status, r.Result = script.RunStatusSucceeded, []byte(`{"total": 42}`)
	})
	rec := servePortalRequest(t, deps, http.MethodPost, runPath+"?wait=30", `{"params":{"source":"warehouse"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var run portalRunDetail
	decodeInto(t, rec, &run)
	assert.Equal(t, script.RunStatusSucceeded, run.Status)
	assert.JSONEq(t, `{"total": 42}`, string(run.Result))
}

// TestPortalRunScript_AWaitThatRunsOutAnswersQueued keeps the asynchronous
// answer for a run still going when the wait ends, with its id to follow.
func TestPortalRunScript_AWaitThatRunsOutAnswersQueued(t *testing.T) {
	deps, runs := finishingDeps(func(r *script.Run) { r.Status = script.RunStatusRunning })
	started := time.Now()
	rec := servePortalRequest(t, deps, http.MethodPost, runPath+"?wait=1", `{"params":{"source":"warehouse"}}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.GreaterOrEqual(t, time.Since(started), time.Second, "it waited the second it was given")
	var body runResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, runs.queued.ID, body.RunID)
}

func TestPortalRunScript_RefusesABadWait(t *testing.T) {
	for _, wait := range []string{"-1", "soon", "1.5"} {
		deps, runs := runDeps(runnableStore(), carol)
		rec := servePortalRequest(t, deps, http.MethodPost, runPath+"?wait="+wait, `{"params":{"source":"warehouse"}}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, wait)
		assert.Nil(t, runs.queued, "nothing is queued for a request that is refused")
	}
}

func TestWaitFor_IsHeldToTheCap(t *testing.T) {
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/x?wait=9999", http.NoBody)
	require.NoError(t, err)
	wait, ok := waitFor(nil, r)
	require.True(t, ok)
	assert.Equal(t, time.Duration(runcontrol.MaxWaitSeconds)*time.Second, wait)
}

const cancelPath = "/api/v1/portal/scripts/script_2/runs/run_2/cancel"

// TestPortalCancelRun is #1847's cancel over HTTP, under the same rule that
// reads a run: the owner and the requester may, a stranger gets 404.
func TestPortalCancelRun(t *testing.T) {
	runs := &stubRuns{
		runs:        []script.Run{{ID: "run_2", ScriptID: "script_2", Status: script.RunStatusRunning, RequestedBy: "bob@example.com"}},
		cancelPrior: script.RunStatusRunning,
	}
	rec := servePortalRequest(t, portalDeps(runnableStore(), runs, nil, carol), http.MethodPost, cancelPath, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body cancelResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, string(runcontrol.CancelRequested), body.Outcome)
	assert.Contains(t, body.Message, "within seconds")
	assert.Equal(t, []string{"run_2 by carol@example.com"}, runs.canceled)

	requester := &stubRuns{runs: runs.runs, cancelPrior: script.RunStatusPending}
	rec = servePortalRequest(t, portalDeps(runnableStore(), requester, nil, stranger), http.MethodPost, cancelPath, "")
	require.Equal(t, http.StatusOK, rec.Code, "whoever asked for the run may stop it")

	other := &stubRuns{runs: []script.Run{{ID: "run_2", ScriptID: "script_2", RequestedBy: "someone@example.com"}}}
	rec = servePortalRequest(t, portalDeps(runnableStore(), other, nil, stranger), http.MethodPost, cancelPath, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, other.canceled)
}

func TestPortalCancelRun_StoreFailures(t *testing.T) {
	gone := &stubRuns{runs: []script.Run{{ID: "run_2", ScriptID: "script_2"}}, cancelErr: script.ErrRunNotFound}
	rec := servePortalRequest(t, portalDeps(runnableStore(), gone, nil, carol), http.MethodPost, cancelPath, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	broken := &stubRuns{runs: []script.Run{{ID: "run_2", ScriptID: "script_2"}}, cancelErr: errors.New("boom")}
	rec = servePortalRequest(t, portalDeps(runnableStore(), broken, nil, carol), http.MethodPost, cancelPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestPortalGetRun_CarriesProgressAndResult: a running run shows its latest
// progress, and a finished one its result.
func TestPortalGetRun_CarriesProgressAndResult(t *testing.T) {
	done, total := int64(3), int64(10)
	requested := time.Now()
	runs := &stubRuns{runs: []script.Run{{
		ID: "run_1", ScriptID: "script_1", Status: script.RunStatusRunning, Log: "so far\n",
		Progress:          &script.RunProgress{Message: "step", Done: &done, Total: &total},
		CancelRequestedAt: &requested, CancelRequestedBy: "jane@example.com",
		Result: []byte(`[1,2]`),
	}}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs/run_1")
	require.Equal(t, http.StatusOK, rec.Code)
	var run portalRunDetail
	decodeInto(t, rec, &run)
	require.NotNil(t, run.Progress)
	assert.Equal(t, int64(3), *run.Progress.Done)
	assert.True(t, run.CancelRequested)
	assert.Equal(t, "jane@example.com", run.CancelRequestedBy)
	assert.JSONEq(t, `[1,2]`, string(run.Result))
	assert.Equal(t, "so far\n", run.Log)
}
