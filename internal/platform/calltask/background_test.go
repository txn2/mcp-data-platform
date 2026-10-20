package calltask

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/exportasync"
	"github.com/txn2/mcp-data-platform/internal/mcptask"
)

// TestExports_StartRunsApartReportsProgressAndIsFoundByKey is #2212 at the
// runner: a background export is a working call task marked as one, its walk
// reports progress the heartbeat writes, it is found by its caller and key,
// and it completes with the export's own result.
func TestExports_StartRunsApartReportsProgressAndIsFoundByKey(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store, Retention: time.Hour})
	exports := r.Exports()

	reqCtx, cancelReq := context.WithCancel(caller("jane", false))
	reported, release := make(chan struct{}), make(chan struct{})
	run, started, err := exports.Start(reqCtx, "api_export", "k1", func(ctx context.Context) (*mcp.CallToolResult, error) {
		exportasync.Report(ctx, 4, 400)
		close(reported)
		<-release
		return textResult(`{"asset_id":"a1"}`), nil
	})
	require.NoError(t, err)
	require.True(t, started)
	assert.Equal(t, exportasync.StatusRunning, run.Status)
	task := store.task(run.ID)
	assert.True(t, task.Background)
	assert.Equal(t, "k1", task.IdempotencyKey)
	cancelReq() // the request ending does not stop the export

	<-reported
	require.NoError(t, r.beat(context.Background()))
	found, err := exports.Find(caller("jane", false), "api_export", "k1")
	require.NoError(t, err)
	assert.Equal(t, exportasync.Progress{PagesFetched: 4, ItemsMerged: 400}, found.Progress, "the heartbeat wrote the walk's progress")

	again, started, err := exports.Start(caller("jane", false), "api_export", "k1", nil)
	require.NoError(t, err)
	assert.False(t, started, "a key in flight is joined, not started twice")
	assert.Equal(t, run.ID, again.ID)

	close(release)
	<-store.finished
	found, err = exports.Find(caller("jane", false), "api_export", "k1")
	require.NoError(t, err)
	assert.Equal(t, exportasync.StatusCompleted, found.Status)
	res, err := exportasync.Result(found)
	require.NoError(t, err)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, `{"asset_id":"a1"}`, text.Text)

	ended, err := exports.Ended(context.Background(), "jane", time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, ended, 1)
	assert.Equal(t, run.ID, ended[0].ID)

	_, err = exports.Find(caller("bob", false), "api_export", "k1")
	require.ErrorIs(t, err, exportasync.ErrNotFound, "another caller does not find it")
}

func TestExports_Declines(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store})
	_, _, err := r.Exports().Start(context.Background(), "api_export", "k", nil)
	require.ErrorIs(t, err, errNoCaller)
	_, err = r.Exports().Find(context.Background(), "api_export", "k")
	require.ErrorIs(t, err, exportasync.ErrNotFound)

	require.NoError(t, r.Drain(context.Background()))
	_, _, err = r.Exports().Start(caller("jane", false), "api_export", "k", nil)
	require.ErrorIs(t, err, errShuttingDown)

	store.createErr = assert.AnError
	_, _, err = New(Options{Store: store}).Exports().Start(caller("jane", false), "api_export", "k", nil)
	require.ErrorIs(t, err, assert.AnError)
}

// TestExports_AStaleRunIsReadAsFailed: a run whose replica stopped reporting
// is failed when it is read, so its caller can start it again.
func TestExports_AStaleRunIsReadAsFailed(t *testing.T) {
	store := newMemStore()
	now := time.Now()
	r := New(Options{Store: store, Now: func() time.Time { return now }})
	store.tasks["dpt_x"] = &Task{
		ID: "dpt_x", ToolName: "api_export", UserID: "jane", Status: mcptask.StatusWorking,
		IdempotencyKey: "k", Background: true, HeartbeatAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	found, err := r.Exports().Find(caller("jane", false), "api_export", "k")
	require.NoError(t, err)
	assert.Equal(t, exportasync.StatusFailed, found.Status)
	assert.Equal(t, msgReplicaGone, found.Message)
}
