package scripthttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestPortalListRuns_LiveReadsTheRunsThatHaveNotEnded holds #1860: the
// script page reads its live runs with live=true.
func TestPortalListRuns_LiveReadsTheRunsThatHaveNotEnded(t *testing.T) {
	runs := &stubRuns{}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs?live=true")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, runs.lastFilter.Live)

	servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs")
	assert.False(t, runs.lastFilter.Live)
}

// TestPortalRun_ReportsCauseLivenessAndTheHolder holds #1859 and #1860 on the
// portal: a failure carries its cause and whether a retry is expected to help,
// and a running run whose worker stopped reporting names the holder, its
// lease and how the earlier attempt ended.
func TestPortalRun_ReportsCauseLivenessAndTheHolder(t *testing.T) {
	now := time.Now()
	until, seen := now.Add(10*time.Minute), now.Add(-5*time.Minute)
	runs := &stubRuns{runs: []script.Run{
		{ID: "run_1", ScriptID: "script_1", Status: script.RunStatusFailed, Error: "timeout", Cause: runstate.CauseUpstream},
		{ID: "run_2", ScriptID: "script_1", Status: script.RunStatusFailed, Error: "key error"},
		{
			ID: "run_3", ScriptID: "script_1", Status: script.RunStatusRunning, Attempt: 2, Reclaims: 1,
			LockedBy: "worker-gone", LockedUntil: &until, HeartbeatAt: &seen,
			Attempts: []runstate.Attempt{{Attempt: 1, Worker: "worker-first", Outcome: runstate.AttemptLeaseExpired}},
		},
	}}
	rec := servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs")
	require.Equal(t, http.StatusOK, rec.Code)
	var list portalRunListResponse
	decodeInto(t, rec, &list)
	require.Len(t, list.Data, 3)
	assert.Equal(t, runstate.CauseUpstream, list.Data[0].Cause)
	assert.True(t, list.Data[0].Retryable)
	assert.Equal(t, runstate.CauseScript, list.Data[1].Cause, "a failure recorded without a cause is the script's")
	assert.False(t, list.Data[1].Retryable)
	assert.Equal(t, runstate.LivenessUnresponsive, list.Data[2].Liveness)
	assert.Empty(t, list.Data[0].Liveness, "only a running run has a liveness")

	rec = servePortal(t, portalDeps(portalStore(), runs, nil, owner), "/api/v1/portal/scripts/script_1/runs/run_3")
	require.Equal(t, http.StatusOK, rec.Code)
	var detail portalRunDetail
	decodeInto(t, rec, &detail)
	assert.Equal(t, "worker-gone", detail.LockedBy)
	assert.Equal(t, 1, detail.Reclaims)
	require.NotNil(t, detail.HeartbeatAt)
	require.Len(t, detail.Attempts, 1)
	assert.Equal(t, runstate.AttemptLeaseExpired, detail.Attempts[0].Outcome)
}

// TestDryRunFailureMessage_ByCause: only a script error is called
// deterministic; a briefly unavailable upstream says to try again.
func TestDryRunFailureMessage_ByCause(t *testing.T) {
	assert.Contains(t, dryRunFailureMessage(&scriptrun.WriteRecord{Tool: "manage_asset"}, runstate.CauseScript), "allow_writes")
	assert.Contains(t, dryRunFailureMessage(nil, runstate.CauseUpstream), "temporarily unavailable")
	assert.Contains(t, dryRunFailureMessage(nil, runstate.CauseMemory), "append=True")
	assert.Contains(t, dryRunFailureMessage(nil, runstate.CauseScript), "deterministic")
}

func TestDraftMetrics_CarriesThePeak(t *testing.T) {
	m := draftMetrics(&scriptrun.Result{Steps: 5, PeakMemory: 4096})
	assert.Equal(t, int64(4096), m.PeakMemoryBytes)
}
