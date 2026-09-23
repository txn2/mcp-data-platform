//go:build integration

package scriptstore

// The real-schema proof for what a run reports while it executes and hands
// back when it ends (#1845, #1847): the progress columns, the result, the
// canceled status the check constraint admits, and the cancel request a
// worker reads back from its own fenced write.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// enqueued puts one run of a fresh script on the queue.
func enqueued(ctx context.Context, t *testing.T, s *Store, id string) {
	t.Helper()
	sc, version := savedScript(ctx, t, s, "report-"+id)
	require.NoError(t, s.Enqueue(ctx, &script.Run{
		ID: id, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version, Trigger: script.TriggerTool,
	}))
}

func TestRealDB_ProgressAndLogAreReadableWhileTheRunExecutes(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	enqueued(ctx, t, s, "dpx_live")
	run, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)

	done, total := int64(3), int64(10)
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	requested, _, err := s.RecordProgress(ctx, run.Lease(), script.RunLive{
		Progress: &script.RunProgress{Message: "step", Done: &done, Total: &total, At: at},
		Log:      "line 1\nline 2\n",
	})
	require.NoError(t, err)
	assert.False(t, requested)

	live, err := s.GetRun(ctx, "dpx_live")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusRunning, live.Status)
	assert.Equal(t, "line 1\nline 2\n", live.Log, "the log so far is on the running run")
	require.NotNil(t, live.Progress)
	assert.Equal(t, "step", live.Progress.Message)
	assert.Equal(t, int64(3), *live.Progress.Done)
	assert.Equal(t, int64(10), *live.Progress.Total)
	assert.True(t, at.Equal(live.Progress.At))

	// An unchanged report touches nothing; a report with no progress keeps the
	// last one.
	_, _, err = s.RecordProgress(ctx, run.Lease(), script.RunLive{Unchanged: true, Log: "ignored"})
	require.NoError(t, err)
	_, _, err = s.RecordProgress(ctx, run.Lease(), script.RunLive{Log: "line 1\nline 2\nline 3\n"})
	require.NoError(t, err)
	live, err = s.GetRun(ctx, "dpx_live")
	require.NoError(t, err)
	assert.Equal(t, "line 1\nline 2\nline 3\n", live.Log)
	assert.Equal(t, "step", live.Progress.Message)

	// A worker that lost the run neither writes nor learns anything.
	_, _, err = s.RecordProgress(ctx, script.RunLease{RunID: "dpx_live", Worker: "worker-b", Attempt: 1}, script.RunLive{Log: "x"})
	require.ErrorIs(t, err, script.ErrLeaseLost)
}

func TestRealDB_FinishKeepsTheResultAndTheLastProgress(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	enqueued(ctx, t, s, "dpx_result")
	run, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)

	done := int64(10)
	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{
		Status: script.RunStatusSucceeded, Log: "ok\n",
		Result:   []byte(`{"total": 42, "ids": ["a", "b"]}`),
		Progress: &script.RunProgress{Message: "done", Done: &done, At: time.Now().UTC()},
	}))
	got, err := s.GetRun(ctx, "dpx_result")
	require.NoError(t, err)
	assert.JSONEq(t, `{"total": 42, "ids": ["a", "b"]}`, string(got.Result))
	require.NotNil(t, got.Progress)
	assert.Equal(t, "done", got.Progress.Message)
	assert.Nil(t, got.Progress.Total)

	enqueued(ctx, t, s, "dpx_noresult")
	run, err = s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{Status: script.RunStatusSucceeded}))
	got, err = s.GetRun(ctx, "dpx_noresult")
	require.NoError(t, err)
	assert.Nil(t, got.Result, "a run that set no result has none, not an empty value")
	assert.Nil(t, got.Progress)
}

// TestRealDB_CancelActsOnWhatTheRunWas covers every prior status: a pending
// run is canceled and never claimed, a running one is marked and its worker
// learns it from its next report, and a finished one is left alone.
func TestRealDB_CancelActsOnWhatTheRunWas(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	enqueued(ctx, t, s, "dpx_queued")
	prior, err := s.CancelRun(ctx, "dpx_queued", "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusPending, prior)
	queued, err := s.GetRun(ctx, "dpx_queued")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusCanceled, queued.Status)
	assert.Equal(t, "canceled by jane@example.com", queued.Error)
	assert.NotNil(t, queued.FinishedAt)
	_, err = s.Claim(ctx, "worker-a", time.Minute)
	require.ErrorIs(t, err, script.ErrNoWork, "a canceled run is never claimed")

	enqueued(ctx, t, s, "dpx_running")
	run, err := s.Claim(ctx, "worker-a", time.Minute)
	require.NoError(t, err)
	prior, err = s.CancelRun(ctx, "dpx_running", "sam@example.com")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusRunning, prior)
	requested, by, err := s.RecordProgress(ctx, run.Lease(), script.RunLive{Unchanged: true})
	require.NoError(t, err)
	assert.True(t, requested)
	assert.Equal(t, "sam@example.com", by)
	prior, err = s.CancelRun(ctx, "dpx_running", "someone-else@example.com")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusRunning, prior)
	_, by, err = s.RecordProgress(ctx, run.Lease(), script.RunLive{Unchanged: true})
	require.NoError(t, err)
	assert.Equal(t, "sam@example.com", by, "the first request is the one recorded")

	require.NoError(t, s.Finish(ctx, run.Lease(), script.RunResult{
		Status: script.RunStatusCanceled, Error: "canceled by sam@example.com",
	}))
	finished, err := s.GetRun(ctx, "dpx_running")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusCanceled, finished.Status, "the status check admits canceled")
	prior, err = s.CancelRun(ctx, "dpx_running", "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusCanceled, prior)

	_, err = s.CancelRun(ctx, "dpx_nope", "jane@example.com")
	require.ErrorIs(t, err, script.ErrRunNotFound)

	purged, err := s.PurgeRuns(ctx, -time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(2), purged, "canceled runs age out like any other finished run")
}
