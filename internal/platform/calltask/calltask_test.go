package calltask

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// memStore is the call_tasks contract in memory: a task ends once, a cancel
// is recorded only on a working task, and a heartbeat reports the working
// tasks a cancel was asked for.
type memStore struct {
	mu        sync.Mutex
	tasks     map[string]*Task
	createErr error
	getErr    error
	beatErr   error
	finished  chan string
}

func newMemStore() *memStore {
	return &memStore{tasks: map[string]*Task{}, finished: make(chan string, 16)}
}

func (m *memStore) Create(_ context.Context, t Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	m.tasks[t.ID] = &t
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	t, ok := m.tasks[id]
	if !ok {
		return nil, errTaskNotFound
	}
	cp := *t
	return &cp, nil
}

func (m *memStore) Finish(_ context.Context, id string, end Ending) error {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if ok && t.Status == mcptask.StatusWorking {
		t.Status, t.StatusMessage, t.Result, t.Error = end.Status, end.Message, end.Result, end.Error
	}
	m.mu.Unlock()
	m.finished <- id
	return nil
}

func (m *memStore) RequestCancel(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok && t.Status == mcptask.StatusWorking {
		t.CancelRequested = true
	}
	return nil
}

func (m *memStore) Heartbeat(_ context.Context, ids []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.beatErr != nil {
		return nil, m.beatErr
	}
	cancel := []string{}
	for _, id := range ids {
		if t, ok := m.tasks[id]; ok && t.Status == mcptask.StatusWorking && t.CancelRequested {
			cancel = append(cancel, id)
		}
	}
	return cancel, nil
}

func (m *memStore) FailStale(_ context.Context, id string, before time.Time, message string, rpcErr json.RawMessage) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok || t.Status != mcptask.StatusWorking || !t.HeartbeatAt.Before(before) {
		return false, nil
	}
	t.Status, t.StatusMessage, t.Error = mcptask.StatusFailed, message, rpcErr
	return true, nil
}

func (m *memStore) task(id string) Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.tasks[id]
}

// caller is a request context carrying jane's PlatformContext.
func caller(user string, admin bool) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserID: user, IsAdmin: admin})
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// A call becomes a working task the moment it is started, runs apart from the
// request on a context of its own, and completes with the tool's result,
// which tasks/get then reads back.
func TestStartRunsTheCallApartAndRecordsItsResult(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store, Retention: time.Hour, PollInterval: 3 * time.Second})

	reqCtx, cancelReq := context.WithCancel(caller("jane", false))
	reqCtx = mcpcontext.WithProgressToken(reqCtx, "tok")
	var completed mcp.Result
	completedPC := make(chan *middleware.PlatformContext, 1)
	reqCtx = mcptask.WithCompletion(reqCtx, func(ctx context.Context, res mcp.Result, _ error) {
		completed = res
		completedPC <- middleware.GetPlatformContext(ctx)
	})
	release := make(chan struct{})
	seen := make(chan context.Context, 1)
	snap, err := r.Start(reqCtx, "trino_export", func(ctx context.Context) (mcp.Result, error) {
		seen <- ctx
		<-release
		middleware.GetPlatformContext(ctx).EnrichmentApplied = true
		return textResult("exported 10 rows"), nil
	})
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusWorking, snap.Status)
	assert.Regexp(t, `^dpt_[0-9a-f]{32}$`, snap.ID)
	assert.Equal(t, time.Hour, snap.TTL)
	assert.Equal(t, 3*time.Second, snap.PollInterval)
	assert.Equal(t, "jane", store.task(snap.ID).UserID)
	assert.Equal(t, "trino_export", store.task(snap.ID).ToolName)

	runCtx := <-seen
	cancelReq()
	assert.NoError(t, runCtx.Err(), "the request ending does not stop the call")
	assert.Nil(t, mcpcontext.GetProgressToken(runCtx), "a task reports no progress to a closed stream")
	assert.Nil(t, mcpcontext.GetServerSession(runCtx))
	close(release)
	<-store.finished

	pc := <-completedPC
	assert.True(t, pc.EnrichmentApplied, "the completion reads the call's own PlatformContext")
	assert.False(t, middleware.GetPlatformContext(reqCtx).EnrichmentApplied, "the request's PlatformContext is not written by the call")
	assert.NotNil(t, completed)

	got, err := r.Get(caller("jane", false), snap.ID)
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusCompleted, got.Status)
	assert.Contains(t, string(got.Result), "exported 10 rows")
}

// How a call ends decides the task's terminal state: a tool error result is a
// completed task carrying the contract's envelope, a JSON-RPC error is a
// failed one with that error, any other error is a failed internal error, a
// panic is the contract's internal error result, and an answer that is not a
// tool result is a failure.
func TestOutcomes(t *testing.T) {
	cases := map[string]struct {
		run        func(context.Context) (mcp.Result, error)
		status     string
		resultHas  string
		errorCode  int64
		errorHas   string
		messageHas string
	}{
		"tool error": {
			run: func(context.Context) (mcp.Result, error) {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "bad sql"}}}, nil
			},
			status: mcptask.StatusCompleted, resultHas: `"category"`,
		},
		"rpc error": {
			run: func(context.Context) (mcp.Result, error) {
				return nil, &jsonrpc.Error{Code: -32602, Message: "unknown tool"}
			},
			status: mcptask.StatusFailed, errorCode: -32602, errorHas: "unknown tool", messageHas: "unknown tool",
		},
		"go error": {
			run:    func(context.Context) (mcp.Result, error) { return nil, errors.New("conn reset") },
			status: mcptask.StatusFailed, errorCode: jsonrpc.CodeInternalError, errorHas: "conn reset",
		},
		"panic": {
			run:    func(context.Context) (mcp.Result, error) { panic("nil map") },
			status: mcptask.StatusCompleted, resultHas: "failed unexpectedly",
		},
		"not a tool result": {
			run:    func(context.Context) (mcp.Result, error) { return &mcp.ListToolsResult{}, nil },
			status: mcptask.StatusFailed, errorCode: jsonrpc.CodeInternalError, errorHas: msgNoToolResult,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newMemStore()
			r := New(Options{Store: store})
			snap, err := r.Start(caller("jane", false), "api_export", tc.run)
			require.NoError(t, err)
			<-store.finished
			got, err := r.Get(caller("jane", false), snap.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.status, got.Status)
			if tc.resultHas != "" {
				assert.Contains(t, string(got.Result), tc.resultHas)
			}
			if tc.errorCode != 0 {
				require.NotNil(t, got.Error)
				assert.Equal(t, tc.errorCode, got.Error.Code)
				assert.Contains(t, got.Error.Message, tc.errorHas)
			}
			if tc.messageHas != "" {
				assert.Contains(t, got.StatusMessage, tc.messageHas)
			}
		})
	}
}

// A task is shown to whoever started it and to an administrator; to anybody
// else, and to a caller with no identity, it does not exist.
func TestOnlyTheCallerAndAnAdministratorFollowATask(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store})
	snap, err := r.Start(caller("jane", false), "trino_export", func(context.Context) (mcp.Result, error) { return textResult("ok"), nil })
	require.NoError(t, err)
	<-store.finished

	_, err = r.Get(caller("jane", false), snap.ID)
	require.NoError(t, err)
	_, err = r.Get(caller("root", true), snap.ID)
	require.NoError(t, err)
	for _, ctx := range []context.Context{caller("bob", false), caller("", false), context.Background()} {
		_, err = r.Get(ctx, snap.ID)
		require.ErrorIs(t, err, mcptask.ErrNotFound)
		require.ErrorIs(t, r.Cancel(ctx, snap.ID), mcptask.ErrNotFound)
	}
	_, err = r.Get(caller("jane", false), "dpt_missing")
	require.ErrorIs(t, err, mcptask.ErrNotFound)
	store.getErr = errors.New("db down")
	_, err = r.Get(caller("jane", false), snap.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, mcptask.ErrNotFound)
}

// A call with no identified caller, which nobody could follow, is declined,
// and so is one the store cannot record.
func TestStartDeclines(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store})
	run := func(context.Context) (mcp.Result, error) { return textResult("ok"), nil }
	_, err := r.Start(context.Background(), "trino_export", run)
	require.ErrorIs(t, err, mcptask.ErrNotDetached)
	_, err = r.Start(caller("", false), "trino_export", run)
	require.ErrorIs(t, err, mcptask.ErrNotDetached)

	store.createErr = errors.New("db down")
	_, err = r.Start(caller("jane", false), "trino_export", run)
	require.Error(t, err)
	assert.NotErrorIs(t, err, mcptask.ErrNotDetached)
}

// tasks/cancel stops a call running on this replica at once; the call ends
// canceled whatever it returned on its way out.
func TestCancelStopsALocalCall(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store})
	started := make(chan struct{})
	snap, err := r.Start(caller("jane", false), "graphql_export", func(ctx context.Context) (mcp.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.NoError(t, err)
	<-started
	require.NoError(t, r.Cancel(caller("jane", false), snap.ID))
	<-store.finished
	got, err := r.Get(caller("jane", false), snap.ID)
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusCancelled, got.Status)
	assert.Equal(t, msgCancelled, got.StatusMessage)
	require.NoError(t, r.Cancel(caller("jane", false), snap.ID), "canceling an ended task acknowledges and changes nothing")
	assert.Equal(t, mcptask.StatusCancelled, store.task(snap.ID).Status)
}

// A cancel taken by another replica reaches the call at this replica's next
// heartbeat, which reads it in the statement that stamps the call alive.
func TestHeartbeatCarriesACancelFromAnotherReplica(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store})
	started := make(chan struct{})
	snap, err := r.Start(caller("jane", false), "trino_export", func(ctx context.Context) (mcp.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.NoError(t, err)
	<-started
	require.NoError(t, r.beat(context.Background()), "no cancel asked for: the call keeps running")
	require.NoError(t, store.RequestCancel(context.Background(), snap.ID), "another replica took the cancel")
	require.NoError(t, r.beat(context.Background()))
	<-store.finished
	assert.Equal(t, mcptask.StatusCancelled, store.task(snap.ID).Status)

	require.NoError(t, r.beat(context.Background()), "nothing running, nothing to stamp")
	store.beatErr = errors.New("db down")
	blocked := make(chan struct{})
	_, err = r.Start(caller("jane", false), "trino_export", func(context.Context) (mcp.Result, error) {
		<-blocked
		return textResult("ok"), nil
	})
	require.NoError(t, err)
	require.Error(t, r.beat(context.Background()))
	close(blocked)
	<-store.finished
}

// A working task whose replica stopped reporting is read as failed, with an
// error that says so; one still reporting is left working.
func TestAStaleTaskReadsAsFailed(t *testing.T) {
	store := newMemStore()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	r := New(Options{Store: store, Heartbeat: time.Second, Now: func() time.Time { return now }})
	store.tasks["dpt_old"] = &Task{
		ID: "dpt_old", UserID: "jane", Status: mcptask.StatusWorking, CreatedAt: now.Add(-time.Hour),
		HeartbeatAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	store.tasks["dpt_live"] = &Task{
		ID: "dpt_live", UserID: "jane", Status: mcptask.StatusWorking, CreatedAt: now.Add(-time.Hour),
		HeartbeatAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Hour),
	}

	got, err := r.Get(caller("jane", false), "dpt_old")
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusFailed, got.Status)
	assert.Equal(t, msgReplicaGone, got.StatusMessage)
	require.NotNil(t, got.Error)
	assert.Equal(t, int64(jsonrpc.CodeInternalError), got.Error.Code)

	got, err = r.Get(caller("jane", false), "dpt_live")
	require.NoError(t, err)
	assert.Equal(t, mcptask.StatusWorking, got.Status)
}

// A drain waits for the calls running here and takes no new one; a drain
// past its deadline says so.
func TestDrain(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store})
	release := make(chan struct{})
	_, err := r.Start(caller("jane", false), "trino_export", func(context.Context) (mcp.Result, error) {
		<-release
		return textResult("ok"), nil
	})
	require.NoError(t, err)

	short, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, r.Drain(short), "the call is still running")

	_, err = r.Start(caller("jane", false), "trino_export", func(context.Context) (mcp.Result, error) { return textResult("ok"), nil })
	require.ErrorIs(t, err, mcptask.ErrNotDetached, "a draining replica takes no new call")

	close(release)
	require.NoError(t, r.Drain(context.Background()))
}

// Stopping the replica ends the calls still running as failed, saying the
// server shut down, and stops the heartbeat.
func TestStopFailsWhatIsStillRunning(t *testing.T) {
	store := newMemStore()
	r := New(Options{Store: store, Heartbeat: time.Millisecond})
	r.StartHeartbeat()
	r.StartHeartbeat()
	started := make(chan struct{})
	snap, err := r.Start(caller("jane", false), "trino_export", func(ctx context.Context) (mcp.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.NoError(t, err)
	<-started
	r.Stop(context.Background())
	task := store.task(snap.ID)
	assert.Equal(t, mcptask.StatusFailed, task.Status)
	assert.Equal(t, msgShutdown, task.StatusMessage)
	r.Stop(context.Background())

	// A call that does not return when canceled does not hold the stop past
	// its deadline.
	stuck := New(Options{Store: newMemStore()})
	release := make(chan struct{})
	_, err = stuck.Start(caller("jane", false), "trino_export", func(context.Context) (mcp.Result, error) {
		<-release
		return textResult("late"), nil
	})
	require.NoError(t, err)
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	stuck.Stop(expired)
	close(release)
}

// Prefix and the defaults a zero Options takes.
func TestDefaults(t *testing.T) {
	r := New(Options{})
	assert.Equal(t, IDPrefix, r.Prefix())
	assert.Equal(t, DefaultRetention, r.retention)
	assert.Equal(t, DefaultHeartbeat, r.heartbeat)
	assert.Equal(t, DefaultPollInterval, r.poll)
	snap := r.snapshot(&Task{ID: "dpt_1", Error: json.RawMessage(`not json`)})
	assert.Nil(t, snap.Error, "an unreadable stored error is left out")
	assert.Nil(t, nullJSON(nil))
	assert.Equal(t, []byte(`{}`), nullJSON(json.RawMessage(`{}`)))
}
