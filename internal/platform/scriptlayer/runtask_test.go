package scriptlayer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// runAsTask calls run_script the way mcptask.Middleware hands it a call from
// a client that follows it as a task (#2009), and returns the tool's answer
// and the run id the call accepted.
func runAsTask(ctx context.Context, t *testing.T, h *Handle, input runScriptInput) (answer *mcp.CallToolResult, accepted string) {
	t.Helper()
	spy := &acceptSpy{}
	mw := mcptask.Middleware(mcptask.Config{
		Tools: map[string]mcptask.Mode{ToolNameRunScript: mcptask.Native}, Sources: mcptask.NewSources(spy),
	})
	handler := mw(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		res, _, err := h.handleRunScript(ctx, input)
		answer = res
		return res, err
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: ToolNameRunScript, Meta: mcp.Meta{
		"io.modelcontextprotocol/clientCapabilities": map[string]any{"extensions": map[string]any{mcptask.ExtensionID: map[string]any{}}},
	}}}
	_, err := handler(ctx, "tools/call", req)
	require.NoError(t, err)
	accepted = spy.accepted
	return answer, accepted
}

// acceptSpy is the Source the accepted run id is read back through.
type acceptSpy struct{ accepted string }

func (*acceptSpy) Prefix() string { return pkgsession.ScriptSessionPrefix }
func (a *acceptSpy) Get(_ context.Context, id string) (*mcptask.Snapshot, error) {
	a.accepted = id
	return &mcptask.Snapshot{ID: id, Status: mcptask.StatusWorking}, nil
}
func (*acceptSpy) Cancel(context.Context, string) error { return nil }

// startTask runs run_script for a client following it as a task and returns
// the run id the call accepted.
func startTask(t *testing.T, h *Handle) (id string, out map[string]any) {
	t.Helper()
	res, id := runAsTask(authorCtx(), t, h, runScriptInput{Name: "daily"})
	require.False(t, res.IsError, resultText(res))
	require.NotEmpty(t, id, "the call accepted its claim")
	out = resultFields(t, res)
	return id, out
}

// A run_script call that is to become a task is answered at once, without
// waiting for the run, and accepts its claim with the run id.
func TestRunScript_AsATaskAnswersAtOnceWithTheRun(t *testing.T) {
	h, _, runs := runnableHandle(t)
	id, out := startTask(t, h)
	assert.Equal(t, id, out["run_id"])
	assert.Equal(t, script.RunStatusPending, out[fieldStatus], "no worker ran it: the call did not wait")
	assert.Contains(t, runs.all(), id)
}

// A refused run_script call accepts nothing, so it is answered with its
// refusal rather than a task.
func TestRunScript_ARefusedCallBecomesNoTask(t *testing.T) {
	h, _, _ := runnableHandle(t)
	res, id := runAsTask(authorCtx(), t, h, runScriptInput{Name: "missing"})
	assert.True(t, res.IsError)
	assert.Empty(t, id)
}

// A run reads as a task: working while it is queued or executing, with its
// progress as the message; completed with run_script's answer when it ends,
// a failure included; canceled when it was canceled.
func TestRunTasks_ReadsTheRunAsATask(t *testing.T) {
	h, _, runs := runnableHandle(t)
	tasks := h.RunTasks(24 * time.Hour)
	require.NotNil(t, tasks)
	assert.Equal(t, pkgsession.ScriptSessionPrefix, tasks.Prefix())
	id, _ := startTask(t, h)

	snap, err := tasks.Get(authorCtx(), id)
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusWorking, snap.Status)
	assert.Equal(t, "The run is queued.", snap.StatusMessage)
	assert.Equal(t, 24*time.Hour, snap.TTL)
	assert.Equal(t, RunTaskPollInterval, snap.PollInterval)

	edit := func(fn func(*script.Run)) {
		runs.mu.Lock()
		defer runs.mu.Unlock()
		fn(runs.byID[id])
	}
	now := time.Now().UTC()
	edit(func(r *script.Run) {
		r.Status, r.StartedAt, r.HeartbeatAt = script.RunStatusRunning, &now, &now
		until := now.Add(time.Minute)
		r.LockedUntil, r.LockedBy, r.ClaimedAt = &until, "w1", &now
	})
	snap, err = tasks.Get(authorCtx(), id)
	require.NoError(t, err)
	assert.Equal(t, "The run is executing.", snap.StatusMessage)

	later := now.Add(time.Second)
	edit(func(r *script.Run) { r.Progress = &script.RunProgress{Message: "page 3 of 10", At: later} })
	snap, err = tasks.Get(authorCtx(), id)
	require.NoError(t, err)
	assert.Equal(t, "page 3 of 10", snap.StatusMessage)
	assert.Equal(t, later, snap.UpdatedAt)

	edit(func(r *script.Run) {
		end := later.Add(time.Second)
		r.Status, r.FinishedAt, r.Result = script.RunStatusSucceeded, &end, json.RawMessage(`{"rows":42}`)
	})
	snap, err = tasks.Get(authorCtx(), id)
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusCompleted, snap.Status)
	answer := taskAnswer(t, snap.Result)
	assert.Equal(t, map[string]any{"rows": float64(42)}, answer["result"], "platform.result is the task's result")
	assert.Equal(t, id, answer["run_id"])
	assert.Equal(t, script.RunStatusSucceeded, answer[fieldStatus])

	edit(func(r *script.Run) {
		r.Status, r.Error, r.Cause = script.RunStatusFailed, "boom", runstate.CauseUpstream
	})
	snap, err = tasks.Get(authorCtx(), id)
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusCompleted, snap.Status, "a script failure is a tool result, not a protocol error")
	answer = taskAnswer(t, snap.Result)
	assert.Equal(t, runstate.CauseUpstream, answer["cause"])
	assert.Equal(t, true, answer["retryable"])
	assert.Equal(t, "boom", answer["error"])

	edit(func(r *script.Run) { r.Status = script.RunStatusCanceled })
	snap, err = tasks.Get(authorCtx(), id)
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusCancelled, snap.Status)
	assert.Empty(t, snap.Result)
}

// taskAnswer reads the run_script answer a completed task inlines: a tool
// result whose one text block is the run's JSON.
func taskAnswer(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)
	var answer map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &answer))
	return answer
}

// A run's task is followed by the script's owner, an administrator and
// whoever requested the run; to anyone else it does not exist.
func TestRunTasks_FollowsTheRunReadingRule(t *testing.T) {
	h, _, _ := runnableHandle(t)
	tasks := h.RunTasks(time.Hour)
	id, _ := startTask(t, h)

	for _, ctx := range []context.Context{authorCtx(), adminCtx()} {
		_, err := tasks.Get(ctx, id)
		require.NoError(t, err)
	}
	stranger := callerCtx("dana@example.com", "analyst")
	_, err := tasks.Get(stranger, id)
	require.ErrorIs(t, err, mcptask.ErrNotFound)
	require.ErrorIs(t, tasks.Cancel(stranger, id), mcptask.ErrNotFound)
	_, err = tasks.Get(authorCtx(), "dpx_missing")
	require.ErrorIs(t, err, mcptask.ErrNotFound)
}

// Canceling a run's task is cancel_run: a queued run never starts.
func TestRunTasks_CancelIsCancelRun(t *testing.T) {
	h, _, runs := runnableHandle(t)
	tasks := h.RunTasks(time.Hour)
	id, _ := startTask(t, h)
	require.NoError(t, tasks.Cancel(authorCtx(), id))
	run, err := runs.GetRun(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, script.RunStatusCanceled, run.Status)
	assert.Equal(t, "jane@example.com", run.CancelRequestedBy)

	runs.cancelErr = errors.New("db down")
	require.Error(t, tasks.Cancel(authorCtx(), id))
}

// A store that cannot be read is an error, never a task that does not exist.
func TestRunTasks_ReadFailures(t *testing.T) {
	h, store, runs := runnableHandle(t)
	tasks := h.RunTasks(time.Hour)
	id, _ := startTask(t, h)

	runs.getErr = errors.New("db down")
	_, err := tasks.Get(authorCtx(), id)
	require.Error(t, err)
	assert.NotErrorIs(t, err, mcptask.ErrNotFound)
	runs.getErr = nil

	store.getByIDErr = errors.New("db down")
	_, err = tasks.Get(authorCtx(), id)
	require.Error(t, err)
	assert.NotErrorIs(t, err, mcptask.ErrNotFound)

	assert.Nil(t, (*Handle)(nil).RunTasks(time.Hour))
	assert.Nil(t, New(Config{Store: newMemStore()}).RunTasks(time.Hour), "no run queue, no run tasks")
}
