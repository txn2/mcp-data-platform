// Package calltask executes a tool call apart from the request that asked for
// it, as an MCP task (#2009).
//
// A client that declares the Tasks extension and calls an export tool
// (trino_export, api_export, graphql_export) is answered at once with a task
// handle, and the export runs on the replica that took the call, on a context
// that outlives the request. The task is a call_tasks row: who started it,
// where it stands, and once it ends the tool's result or the JSON-RPC error it
// failed with, so tasks/get is answered by any replica.
//
// While a call runs, its replica stamps the row's heartbeat every few seconds
// and reads, in the same statement, whether a cancel was asked for: that is
// how a tasks/cancel another replica took reaches the call. A row whose
// heartbeat stopped belongs to a replica that is gone, and is read as failed.
//
// The runner sits inside the tools/call chain where mcptask.Middleware hands
// it a call, so authorization, the gates and the consent prompts have already
// answered; what runs apart is the rest of the chain and the tool.
package calltask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/exportasync"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// IDPrefix marks a call task's id.
const IDPrefix = "dpt_"

// Timing defaults.
const (
	// DefaultHeartbeat is how often a replica stamps the calls it runs.
	DefaultHeartbeat = 2 * time.Second
	// staleHeartbeats is how many missed heartbeats make a running call's
	// replica read as gone.
	staleHeartbeats = 15
	// DefaultPollInterval is how often a client is asked to read a task.
	DefaultPollInterval = 2 * time.Second
	// DefaultRetention is how long a task is kept after it is created.
	DefaultRetention = 7 * 24 * time.Hour
	// writeTimeout bounds the store write that records how a call ended,
	// which runs on a context the request no longer cancels.
	writeTimeout = 10 * time.Second
	// idRandomBytes is the entropy of a task id: a task id is presented by
	// whoever follows the task and must not be guessable.
	idRandomBytes = 16
)

// Messages a task's status carries.
const (
	msgWorking       = "The call is executing."
	msgCompleted     = "The call completed."
	msgCancelled     = "The call was canceled."
	msgReplicaGone   = "The server executing this call stopped reporting before the call finished; call the tool again."
	msgShutdown      = "The server executing this call shut down before the call finished; call the tool again."
	msgNoToolResult  = "The call ended without a tool result."
	msgCallFailedFmt = "The call failed: %s"
)

// Options configure a Runner.
type Options struct {
	Store Store
	// Retention is how long a task is kept after it is created; zero is
	// DefaultRetention.
	Retention time.Duration
	// Heartbeat is how often running calls are stamped; zero is
	// DefaultHeartbeat.
	Heartbeat time.Duration
	// PollInterval is the polling interval a client is handed; zero is
	// DefaultPollInterval.
	PollInterval time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Runner executes calls as tasks on this replica and answers for the tasks
// every replica keeps. It is both the mcptask.Detacher and the mcptask.Source
// for its tasks.
type Runner struct {
	store     Store
	retention time.Duration
	heartbeat time.Duration
	poll      time.Duration
	now       func() time.Time

	mu      sync.Mutex
	running map[string]*execution
	closed  bool
	wg      sync.WaitGroup

	stopLoop chan struct{}
	loopDone chan struct{}
}

// execution is one call this replica runs.
type execution struct {
	cancel context.CancelCauseFunc
	// cell is a background export's progress, nil for any other call; written
	// is the progress the heartbeat last recorded.
	cell    *exportasync.Cell
	written exportasync.Progress
}

// Causes a running call's context is canceled with.
var (
	errCancelRequested = errors.New("cancel requested")
	errShutdown        = errors.New("server shutting down")
)

// New builds a Runner over opts.Store.
func New(opts Options) *Runner {
	r := &Runner{
		store:     opts.Store,
		retention: opts.Retention,
		heartbeat: opts.Heartbeat,
		poll:      opts.PollInterval,
		now:       opts.Now,
		running:   make(map[string]*execution),
	}
	if r.retention <= 0 {
		r.retention = DefaultRetention
	}
	if r.heartbeat <= 0 {
		r.heartbeat = DefaultHeartbeat
	}
	if r.poll <= 0 {
		r.poll = DefaultPollInterval
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r
}

// StartHeartbeat begins the heartbeat loop. A second call does nothing.
func (r *Runner) StartHeartbeat() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopLoop != nil {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	r.stopLoop, r.loopDone = stop, done
	go func() {
		defer close(done)
		bgloop.Run(context.Background(), bgloop.Loop{
			Name: bgloop.NameCallTaskHeartbeat, Every: r.heartbeat, Stop: stop, Body: r.beat,
		})
	}()
}

// Drain waits for the calls this replica runs to finish, or for ctx to end,
// and takes no new ones. It is the shutdown drain: a call a client follows as
// a task finishes rather than being cut off.
func (r *Runner) Drain(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("draining call tasks: %w", ctx.Err())
	}
}

// Stop cancels the calls still running, each of which records that its
// server shut down, waits within ctx for them to record it, and stops the
// heartbeat. A call that does not return when canceled is left to the
// process's exit; its task then reads as failed once its heartbeat stops.
func (r *Runner) Stop(ctx context.Context) {
	r.mu.Lock()
	r.closed = true
	for _, ex := range r.running {
		ex.cancel(errShutdown)
	}
	stop, done := r.stopLoop, r.loopDone
	r.stopLoop = nil
	r.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	if err := r.Drain(ctx); err != nil {
		slog.WarnContext(ctx, "call tasks still running at stop", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// Prefix is the call task id prefix.
func (*Runner) Prefix() string { return IDPrefix }

// Start creates the call's task and runs the call apart from the request. A
// call with no identified caller, which nobody could follow, and one that
// arrives while the replica shuts down, are declined and answered directly.
func (r *Runner) Start(ctx context.Context, toolName string, run func(ctx context.Context) (mcp.Result, error)) (*mcptask.Snapshot, error) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserID == "" {
		return nil, mcptask.ErrNotDetached
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := r.now().UTC()
	task := Task{
		ID: id, ToolName: toolName, UserID: pc.UserID, Status: mcptask.StatusWorking,
		StatusMessage: msgWorking, CreatedAt: now, UpdatedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(r.retention),
	}
	dctx, ex := detach(ctx, pc)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		ex.cancel(errShutdown)
		return nil, mcptask.ErrNotDetached
	}
	// The task is created durably before it is handed out, as the extension
	// requires: a tasks/get for the returned id resolves on any replica.
	if err := r.store.Create(ctx, task); err != nil {
		r.mu.Unlock()
		ex.cancel(errShutdown)
		return nil, fmt.Errorf("creating the task: %w", err)
	}
	r.running[id] = ex
	r.wg.Add(1)
	r.mu.Unlock()
	go r.execute(dctx, id, run)
	return r.snapshot(&task), nil
}

// detach derives the context a call runs on apart from its request: it
// carries the request's values and is canceled only through the execution.
// The call gets its own copy of the PlatformContext, which the layers inside
// fill in while the request's layers read theirs, and no session or progress
// token: the request's stream is closed by the time the call reports, and the
// extension carries no progress or log notifications for a task.
func detach(ctx context.Context, pc *middleware.PlatformContext) (context.Context, *execution) {
	own := *pc
	dctx := middleware.WithPlatformContext(context.WithoutCancel(ctx), &own)
	dctx = mcpcontext.WithServerSession(dctx, nil)
	dctx = mcpcontext.WithProgressToken(dctx, nil)
	dctx, cancel := context.WithCancelCause(dctx)
	return dctx, &execution{cancel: cancel}
}

// execute runs one call and records how it ended.
func (r *Runner) execute(ctx context.Context, id string, run func(ctx context.Context) (mcp.Result, error)) {
	defer r.wg.Done()
	res, err := runGuarded(ctx, run)
	r.mu.Lock()
	ex := r.running[id]
	delete(r.running, id)
	r.mu.Unlock()
	if ex != nil {
		ex.cancel(nil)
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if ferr := r.store.Finish(wctx, id, outcome(context.Cause(ctx), res, err)); ferr != nil {
		slog.ErrorContext(ctx, "failed to record how a call task ended", "task_id", id,
			"error", logsan.SanitizeForLog(ferr.Error()))
	}
	mcptask.Complete(ctx, res, err)
}

// runGuarded runs the call through the error contract, which a call executed
// apart from its request sits inside of: its result is normalized, and a
// panic becomes the contract's internal error result, as for a call answered
// directly.
func runGuarded(ctx context.Context, run func(ctx context.Context) (mcp.Result, error)) (mcp.Result, error) {
	contract := middleware.MCPErrorContractMiddleware()(func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return run(ctx)
	})
	return contract(ctx, methodToolsCall, nil) //nolint:wrapcheck // the call's own error, recorded as the task's
}

// methodToolsCall is the request a call task is.
const methodToolsCall = "tools/call"

// outcome reads how a call ended into the task's terminal state. A call
// stopped by a cancel ends canceled whatever it returned; one stopped by the
// replica shutting down failed; a JSON-RPC error is a failed task; a tool
// result, an error result included, is a completed one.
func outcome(cause error, res mcp.Result, err error) Ending {
	switch {
	case errors.Is(cause, errCancelRequested):
		return Ending{Status: mcptask.StatusCancelled, Message: msgCancelled}
	case errors.Is(cause, errShutdown):
		return failed(msgShutdown, internalError(msgShutdown))
	case err != nil:
		var wire *jsonrpc.Error
		if !errors.As(err, &wire) {
			wire = internalError(err.Error())
		}
		return failed(fmt.Sprintf(msgCallFailedFmt, wire.Message), wire)
	}
	ctr, ok := res.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		return failed(msgNoToolResult, internalError(msgNoToolResult))
	}
	raw, encErr := mcptask.EncodeResult(ctr)
	if encErr != nil {
		return failed(encErr.Error(), internalError(encErr.Error()))
	}
	return Ending{Status: mcptask.StatusCompleted, Message: msgCompleted, Result: raw}
}

// failed is a failed Ending carrying wire.
func failed(message string, wire *jsonrpc.Error) Ending {
	return Ending{Status: mcptask.StatusFailed, Message: message, Error: encodeRPCError(wire)}
}

// beat stamps the calls this replica runs and stops the ones a cancel was
// asked for on any replica.
func (r *Runner) beat(ctx context.Context) error {
	ids := r.runningIDs()
	if len(ids) == 0 {
		return nil
	}
	cancel, err := r.store.Heartbeat(ctx, ids)
	if err != nil {
		return fmt.Errorf("call task heartbeat: %w", err)
	}
	for _, id := range cancel {
		r.cancelLocal(id)
	}
	r.writeProgress(ctx)
	return nil
}

func (r *Runner) runningIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.running))
	for id := range r.running {
		ids = append(ids, id)
	}
	return ids
}

// cancelLocal stops a call this replica runs; one running elsewhere is
// stopped by its own replica's heartbeat.
func (r *Runner) cancelLocal(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ex, ok := r.running[id]; ok {
		ex.cancel(errCancelRequested)
	}
}

// Get reports a task to whoever started it or an administrator. A working task
// whose replica stopped reporting is failed first.
func (r *Runner) Get(ctx context.Context, id string) (*mcptask.Snapshot, error) {
	task, err := r.readable(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status == mcptask.StatusWorking && r.stale(task) {
		task, err = r.failStale(ctx, task)
		if err != nil {
			return nil, err
		}
	}
	return r.snapshot(task), nil
}

// Cancel asks a task's call to stop: at once when this replica runs it, at
// the running replica's next heartbeat otherwise.
func (r *Runner) Cancel(ctx context.Context, id string) error {
	task, err := r.readable(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != mcptask.StatusWorking {
		return nil
	}
	if err := r.store.RequestCancel(ctx, id); err != nil {
		return fmt.Errorf("requesting cancel of task %s: %w", id, err)
	}
	r.cancelLocal(id)
	return nil
}

// readable reads a task the caller may follow: the person who started it, or
// an administrator. Every other caller is told it does not exist.
func (r *Runner) readable(ctx context.Context, id string) (*Task, error) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserID == "" {
		return nil, mcptask.ErrNotFound
	}
	task, err := r.store.Get(ctx, id)
	if errors.Is(err, errTaskNotFound) {
		return nil, mcptask.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading task %s: %w", id, err)
	}
	if task.UserID != pc.UserID && !pc.IsAdmin {
		return nil, mcptask.ErrNotFound
	}
	return task, nil
}

// stale reports whether a working task's replica stopped reporting.
func (r *Runner) stale(task *Task) bool {
	return r.now().Sub(task.HeartbeatAt) > staleHeartbeats*r.heartbeat
}

// failStale records that a task's replica is gone and re-reads it.
func (r *Runner) failStale(ctx context.Context, task *Task) (*Task, error) {
	before := r.now().Add(-staleHeartbeats * r.heartbeat)
	if _, err := r.store.FailStale(ctx, task.ID, before, msgReplicaGone, encodeRPCError(internalError(msgReplicaGone))); err != nil {
		return nil, fmt.Errorf("failing task %s: %w", task.ID, err)
	}
	reread, err := r.store.Get(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("re-reading task %s: %w", task.ID, err)
	}
	return reread, nil
}

// snapshot renders a task row.
func (r *Runner) snapshot(task *Task) *mcptask.Snapshot {
	snap := &mcptask.Snapshot{
		ID:            task.ID,
		Status:        task.Status,
		StatusMessage: task.StatusMessage,
		CreatedAt:     task.CreatedAt,
		UpdatedAt:     task.UpdatedAt,
		TTL:           task.ExpiresAt.Sub(task.CreatedAt),
		PollInterval:  r.poll,
		Result:        task.Result,
	}
	if len(task.Error) > 0 {
		var wire jsonrpc.Error
		if json.Unmarshal(task.Error, &wire) == nil {
			snap.Error = &wire
		}
	}
	return snap
}

func internalError(msg string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: msg}
}

func encodeRPCError(e *jsonrpc.Error) json.RawMessage {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return raw
}

// newID mints a task id: the prefix and 128 random bits.
func newID() (string, error) {
	b := make([]byte, idRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("minting a task id: %w", err)
	}
	return IDPrefix + hex.EncodeToString(b), nil
}
