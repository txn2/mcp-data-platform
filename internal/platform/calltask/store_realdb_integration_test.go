//go:build integration

package calltask

// Real-Postgres tests for the call_tasks store (#2009): each statement runs
// against the migrated table, so a column the migration names differently,
// or a cast the server refuses, fails here rather than in a running replica.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// A task is created working, stamped by its heartbeat, carries a cancel to
// the heartbeat, and ends once.
func TestCallTaskStoreLifecycle_RealDB(t *testing.T) {
	ctx := context.Background()
	s := NewPostgresStore(testdb.New(t))
	now := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, s.Create(ctx, Task{
		ID: "dpt_a", ToolName: "trino_export", UserID: "jane",
		StatusMessage: msgWorking, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))

	task, err := s.Get(ctx, "dpt_a")
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusWorking, task.Status)
	assert.Equal(t, "jane", task.UserID)
	assert.False(t, task.CancelRequested)
	assert.Empty(t, task.Result)

	cancel, err := s.Heartbeat(ctx, []string{"dpt_a", "dpt_missing"})
	require.NoError(t, err)
	assert.Empty(t, cancel)
	require.NoError(t, s.RequestCancel(ctx, "dpt_a"))
	cancel, err = s.Heartbeat(ctx, []string{"dpt_a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"dpt_a"}, cancel)

	require.NoError(t, s.Finish(ctx, "dpt_a", Ending{Status: mcptask.StatusCompleted, Message: msgCompleted, Result: json.RawMessage(`{"content":[]}`)}))
	require.NoError(t, s.Finish(ctx, "dpt_a", Ending{Status: mcptask.StatusFailed, Message: "late", Error: json.RawMessage(`{"code":-32603}`)}))
	task, err = s.Get(ctx, "dpt_a")
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusCompleted, task.Status, "a task ends once")
	assert.JSONEq(t, `{"content":[]}`, string(task.Result))
	assert.Empty(t, task.Error)

	cancel, err = s.Heartbeat(ctx, []string{"dpt_a"})
	require.NoError(t, err)
	assert.Empty(t, cancel, "an ended task is not stamped")

	_, err = s.Get(ctx, "dpt_missing")
	require.ErrorIs(t, err, errTaskNotFound)
}

// A working task whose heartbeat is older than the bound is failed; one
// stamped since is left working.
func TestCallTaskStoreFailStale_RealDB(t *testing.T) {
	ctx := context.Background()
	s := NewPostgresStore(testdb.New(t))
	now := time.Now().UTC()
	require.NoError(t, s.Create(ctx, Task{
		ID: "dpt_b", ToolName: "api_export", UserID: "jane",
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}))

	failed, err := s.FailStale(ctx, "dpt_b", now.Add(-2*time.Hour), msgReplicaGone, nil)
	require.NoError(t, err)
	assert.False(t, failed, "stamped after the bound")

	failed, err = s.FailStale(ctx, "dpt_b", now, msgReplicaGone, json.RawMessage(`{"code":-32603,"message":"gone"}`))
	require.NoError(t, err)
	assert.True(t, failed)
	task, err := s.Get(ctx, "dpt_b")
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusFailed, task.Status)
	assert.JSONEq(t, `{"code":-32603,"message":"gone"}`, string(task.Error))
}

// TestBackgroundExportStore_RealDB is #2212's store: one working run per
// caller, tool and key, a second start joins it, progress is written while it
// works, the latest run is found by key, and an ended one is reported to its
// caller after the watermark it is read from.
func TestBackgroundExportStore_RealDB(t *testing.T) {
	ctx := context.Background()
	s := NewPostgresStore(testdb.New(t))
	now := time.Now().UTC().Truncate(time.Microsecond)
	bg := func(id string) Task {
		return Task{ID: id, ToolName: "api_export", UserID: "jane", StatusMessage: msgWorking,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour), IdempotencyKey: "k1"}
	}

	first, created, err := s.CreateBackground(ctx, bg("dpt_1"))
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, "dpt_1", first.ID)

	joined, created, err := s.CreateBackground(ctx, bg("dpt_2"))
	require.NoError(t, err)
	assert.False(t, created, "a key already working is joined")
	assert.Equal(t, "dpt_1", joined.ID)
	assert.True(t, joined.Background)

	require.NoError(t, s.SetProgress(ctx, "dpt_1", json.RawMessage(`{"pages_fetched":3,"items_merged":30}`)))
	found, err := s.FindByKey(ctx, "jane", "api_export", "k1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"pages_fetched":3,"items_merged":30}`, string(found.Progress))

	_, err = s.FindByKey(ctx, "bob", "api_export", "k1")
	require.ErrorIs(t, err, errTaskNotFound, "another caller's key is not found")

	ended, err := s.EndedBackground(ctx, "jane", now.Add(-time.Minute), 10)
	require.NoError(t, err)
	assert.Empty(t, ended, "a working run has not ended")

	require.NoError(t, s.Finish(ctx, "dpt_1", Ending{Status: mcptask.StatusFailed, Message: "boom"}))
	ended, err = s.EndedBackground(ctx, "jane", now.Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, ended, 1)
	assert.Equal(t, "k1", ended[0].IdempotencyKey)

	// Once it ended, the key starts a new run, which is the one found.
	second, created, err := s.CreateBackground(ctx, Task{ID: "dpt_3", ToolName: "api_export", UserID: "jane",
		StatusMessage: msgWorking, CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Hour), IdempotencyKey: "k1"})
	require.NoError(t, err)
	require.True(t, created)
	found, err = s.FindByKey(ctx, "jane", "api_export", "k1")
	require.NoError(t, err)
	assert.Equal(t, second.ID, found.ID)
}
