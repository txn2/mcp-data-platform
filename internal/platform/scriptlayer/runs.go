package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/internal/platform/runcontrol"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// defaultRunsLimit caps a run listing that names no limit.
const defaultRunsLimit = 20

// handleRuns lists a script's run history, newest first.
func (h *Handle) handleRuns(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.runs == nil {
		return errorResult("this deployment keeps no script runs"), nil, nil
	}
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultRunsLimit
	}
	runs, err := h.runs.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID, Status: input.RunStatus, Limit: limit})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list script runs", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to list runs"), nil, nil
	}
	summaries := make([]map[string]any, 0, len(runs))
	for i := range runs {
		summaries = append(summaries, runSummary(sc, &runs[i]))
	}
	return jsonResult(map[string]any{
		fieldName: sc.Name, "runs": summaries, "count": len(summaries),
	})
}

// handleGetRun returns one run in full, including its captured log.
//
// The log is the run's own account of itself — what the script printed as it
// worked — and is the first thing an author reads when a scheduled run produced
// the wrong number. It is bounded at capture time, so returning it whole is
// bounded too.
func (h *Handle) handleGetRun(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.runs == nil {
		return errorResult("this deployment keeps no script runs"), nil, nil
	}
	if input.RunID == "" {
		return errorResult("run_id is required"), nil, nil
	}
	if out, ok, err := h.longDrafts.Answer(ctx, input.RunID, resolveEmail(ctx), h.isAdminPersona(ctx)); ok && err != nil {
		return errorResult(err.Error()), nil, nil
	} else if ok {
		return jsonResult(out)
	}
	run, err := h.runs.GetRun(ctx, input.RunID)
	if errors.Is(err, script.ErrRunNotFound) {
		return errorResult("run not found"), nil, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to read a script run", logKeyRunID, input.RunID, logKeyError, err)
		return errorResult("failed to read the run"), nil, nil
	}
	sc, errResult := h.readableRunScript(ctx, run)
	if errResult != nil {
		return errResult, nil, nil
	}
	out := runResult(sc, run)
	out["requested_by"] = run.RequestedBy
	out["params"] = run.Params
	out["scheduled_for"] = run.ScheduledFor.UTC()
	out["steps"] = run.Metrics.Steps
	return jsonResult(out)
}

// handleCancelRun stops a run (#1847): a queued one never starts, a running
// one ends canceled within seconds keeping the outputs it already wrote, and
// a finished one is left alone, which the answer says. Whoever may read a run
// may cancel it -- the script's owner, an administrator, and the person who
// requested that run -- and the same answer covers "no such run" and "not
// yours".
func (h *Handle) handleCancelRun(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.runs == nil {
		return errorResult("this deployment keeps no script runs"), nil, nil
	}
	if input.RunID == "" {
		return errorResult("run_id is required"), nil, nil
	}
	run, err := h.runs.GetRun(ctx, input.RunID)
	if errors.Is(err, script.ErrRunNotFound) {
		return errorResult("run not found"), nil, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to read a script run", logKeyRunID, input.RunID, logKeyError, err)
		return errorResult("failed to read the run"), nil, nil
	}
	sc, errResult := h.readableRunScript(ctx, run)
	if errResult != nil {
		return errResult, nil, nil
	}
	prior, now, err := h.runs.CancelRun(ctx, run.ID, resolveEmail(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "failed to cancel a script run", logKeyRunID, run.ID, logKeyError, err)
		return errorResult("failed to cancel the run"), nil, nil
	}
	return jsonResult(map[string]any{
		fieldName: sc.Name, "run_id": run.ID, "status": now,
		"outcome": string(runcontrol.OutcomeOf(prior, now)), "message": runcontrol.CancelMessage(prior, now),
	})
}

// readableRunScript resolves the script a run belongs to and applies the run
// reading rule to it.
//
// A run id is unguessable, but unguessable is not an authorization rule: the
// run carries the log, the parameters, and the output ids of a script the
// caller may have no business reading. The same message covers "no such run" and
// "not yours", so a caller cannot use the difference to learn that a run exists.
func (h *Handle) readableRunScript(ctx context.Context, run *script.Run) (*script.Script, *mcp.CallToolResult) {
	sc, err := h.store.GetByID(ctx, run.ScriptID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read the script a run belongs to", logKeyRunID, run.ID, logKeyError, err)
		return nil, errorResult("failed to read the run")
	}
	// Whoever asked for a run may read it back, whether or not they own the
	// script: the result was already handed to them when they requested it, and
	// a run they cannot re-read is a run id they cannot follow.
	if sc == nil || (!h.ownsScript(ctx, sc) && !requestedBy(run, resolveEmail(ctx))) {
		return nil, errorResult("run not found")
	}
	return sc, nil
}

// requestedBy reports whether caller is the identified requester of run.
func requestedBy(run *script.Run, caller string) bool {
	return run.RequestedBy != "" && run.RequestedBy == caller
}

// ownsScript reports whether the caller owns a script or is an administrator.
// It is the same rule the read path applies, stated separately because one
// caller admits somebody else as well: whoever requested a run may read that
// run back.
func (h *Handle) ownsScript(ctx context.Context, sc *script.Script) bool {
	return h.isAdminPersona(ctx) || sc.OwnedBy(resolveEmail(ctx))
}

// State actions for manage_script command=state (#1537), the one object the
// runs above carry between them.
const (
	stateActionGet   = "get"
	stateActionSet   = "set"
	stateActionClear = "clear"
)

// handleState reads, replaces or clears a script's state (#1537): the one JSON
// object a run reads as run.state and writes with platform.save_state.
//
// Reading is the owner's and an administrator's, the same rule every other
// read of the script applies. Replacing and clearing are the reset: a wrong
// watermark is otherwise stuck, and "clear it and let the next run start over"
// is the recovery. A reset moves the revision, so a run in flight that read the
// old revision fails at its write, which is correct: the reset was after its
// premise. Neither write is admitted from inside a run, whose one way to write
// state is platform.save_state, applied when the run succeeds under the
// compare-and-set; a run that could reset state through this command would
// step around that guarantee.
func (h *Handle) handleState(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.states == nil {
		return errorResult("this deployment keeps no script state"), nil, nil
	}
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	action := input.StateAction
	if action == "" {
		action = stateActionGet
	}
	switch action {
	case stateActionGet:
		return h.stateGet(ctx, sc)
	case stateActionSet, stateActionClear:
		if errResult := refuseScriptAuthoring(ctx, ToolNameManageScript+" state "+action); errResult != nil {
			return errResult, nil, nil
		}
		return h.stateWrite(ctx, sc, action, input.State)
	default:
		return errorResult(fmt.Sprintf("unknown state_action %q: use get, set or clear", action)), nil, nil
	}
}

// stateGet reports the state as the store holds it.
func (h *Handle) stateGet(ctx context.Context, sc *script.Script) (*mcp.CallToolResult, any, error) {
	st, err := h.states.GetState(ctx, sc.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read script state", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to read the script's state"), nil, nil
	}
	return jsonResult(stateFields(sc, st, ""))
}

// stateWrite replaces the state with the object sent, or with {} for a clear,
// as the person calling.
func (h *Handle) stateWrite(ctx context.Context, sc *script.Script, action string, value map[string]any) (*mcp.CallToolResult, any, error) {
	if action == stateActionSet && value == nil {
		return errorResult("state is required for set: send the whole object the next run should read, or use clear"), nil, nil
	}
	if action == stateActionClear {
		value = map[string]any{}
	}
	if err := script.ValidateState(value); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	st, err := h.states.SetState(ctx, sc.ID, value, resolveEmail(ctx))
	if err != nil {
		slog.ErrorContext(ctx, "failed to write script state", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to write the script's state"), nil, nil
	}
	return jsonResult(stateFields(sc, st, script.StateResetMessage(action == stateActionClear)))
}

// stateFields renders one script's state for a response.
func stateFields(sc *script.Script, st *script.State, message string) map[string]any {
	out := map[string]any{
		fieldName:  sc.Name,
		"state":    orEmptyParams(st.Value),
		"revision": st.Revision,
	}
	if st.Revision > 0 {
		out["updated_at"] = st.UpdatedAt.UTC()
		out["written_by"] = st.WrittenBy()
	}
	if message != "" {
		out["message"] = message
	}
	return out
}

// orEmptyParams normalizes a nil object so the response carries {} rather than
// null: a script with no state has an empty object, not no object.
func orEmptyParams(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

// liveState reads a script's state for a draft, or nil where this deployment
// keeps none. Nil reads as {} inside the run, which is the state a script that
// has never saved any would read on the platform too.
func (h *Handle) liveState(ctx context.Context, sc *script.Script) map[string]any {
	// A script that is not saved has never run, so it reads {} as one that has
	// never saved any does.
	if h.states == nil || sc.ID == "" {
		return nil
	}
	st, err := h.states.GetState(ctx, sc.ID)
	if err != nil {
		slog.WarnContext(ctx, "failed to read script state for a draft; the draft reads {}", fieldName, sc.Name, logKeyError, err)
		return nil
	}
	return st.Value
}

// RunTaskPollInterval is how often a client following a run as a task is asked
// to read it again: often enough that a short run reads as finished soon after
// it does, and no more often than the worker reports (#2009).
const RunTaskPollInterval = 2 * time.Second

// RunTasks is a managed-script run as an MCP task (#2009). The task is the
// run: its id is the run id, its status is read from the run's row, and
// canceling it is cancel_run. Nothing is stored beside the run, so a task
// lives exactly as long as its run is kept (scripts.run_retention_days) and is
// answered by any replica.
type RunTasks struct {
	h   *Handle
	ttl time.Duration
}

// RunTasks returns the task Source over this layer's runs, or nil when the
// deployment keeps no runs. ttl is how long a finished run is kept.
func (h *Handle) RunTasks(ttl time.Duration) *RunTasks {
	if h == nil || h.runs == nil {
		return nil
	}
	return &RunTasks{h: h, ttl: ttl}
}

// Prefix is the run id prefix.
func (*RunTasks) Prefix() string { return pkgsession.ScriptSessionPrefix }

// Get reports a run as a task, under the run reading rule: the script's owner,
// an administrator, and whoever requested the run.
func (t *RunTasks) Get(ctx context.Context, id string) (*mcptask.Snapshot, error) {
	sc, run, err := t.readable(ctx, id)
	if err != nil {
		return nil, err
	}
	return t.snapshot(sc, run)
}

// Cancel asks the run to stop, as cancel_run does.
func (t *RunTasks) Cancel(ctx context.Context, id string) error {
	if _, _, err := t.readable(ctx, id); err != nil {
		return err
	}
	if _, _, err := t.h.runs.CancelRun(ctx, id, resolveEmail(ctx)); err != nil {
		return fmt.Errorf("canceling run %s: %w", id, err)
	}
	return nil
}

// readable reads a run and the script it belongs to, answering
// mcptask.ErrNotFound for a run the caller may not read.
func (t *RunTasks) readable(ctx context.Context, id string) (*script.Script, *script.Run, error) {
	run, err := t.h.runs.GetRun(ctx, id)
	if errors.Is(err, script.ErrRunNotFound) {
		return nil, nil, mcptask.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading run %s: %w", id, err)
	}
	sc, err := t.h.store.GetByID(ctx, run.ScriptID)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the script of run %s: %w", id, err)
	}
	if sc == nil || (!t.h.ownsScript(ctx, sc) && !requestedBy(run, resolveEmail(ctx))) {
		return nil, nil, mcptask.ErrNotFound
	}
	return sc, run, nil
}

// snapshot renders a run as a task. A run that ended is completed with the
// answer run_script would have given, its failure included: a script that
// failed is a tool result that says so, not a protocol error. A canceled run
// is the canceled task.
func (t *RunTasks) snapshot(sc *script.Script, run *script.Run) (*mcptask.Snapshot, error) {
	snap := &mcptask.Snapshot{
		ID:           run.ID,
		CreatedAt:    run.FireTime,
		UpdatedAt:    runUpdatedAt(run),
		TTL:          t.ttl,
		PollInterval: RunTaskPollInterval,
	}
	switch run.Status {
	case script.RunStatusPending, script.RunStatusRunning:
		snap.Status = mcptask.StatusWorking
		snap.StatusMessage = workingMessage(run)
	case script.RunStatusCanceled:
		snap.Status = mcptask.StatusCancelled
		snap.StatusMessage = "The run was canceled. The outputs it wrote before it stopped are listed and stand."
	default:
		res, _, _ := jsonResult(runResult(sc, run))
		raw, err := mcptask.EncodeResult(res)
		if err != nil {
			return nil, fmt.Errorf("rendering run %s as a task: %w", run.ID, err)
		}
		snap.Status = mcptask.StatusCompleted
		snap.StatusMessage = "The run " + run.Status + "."
		snap.Result = raw
	}
	return snap, nil
}

// workingMessage is what a run still going has to say: its latest
// platform.progress report, or where it is in the queue.
func workingMessage(run *script.Run) string {
	if msg := livenessMessage(run, time.Now()); msg != "" {
		return msg
	}
	if run.Progress != nil && run.Progress.Message != "" {
		return run.Progress.Message
	}
	if run.Status == script.RunStatusPending {
		return "The run is queued."
	}
	return "The run is executing."
}

// runUpdatedAt is the last time anything about the run changed.
func runUpdatedAt(run *script.Run) time.Time {
	latest := run.FireTime
	for _, t := range []*time.Time{run.StartedAt, run.HeartbeatAt, run.FinishedAt} {
		if t != nil && t.After(latest) {
			latest = *t
		}
	}
	if run.Progress != nil && run.Progress.At.After(latest) {
		latest = run.Progress.At
	}
	return latest
}

// taskResult is run_script's answer to a call that became a task: the run as
// queued, which is what the call did. The task carries the rest.
func taskResult(ctx context.Context, sc *script.Script, run *script.Run) (*mcp.CallToolResult, any, error) {
	if claim := mcptask.Requested(ctx); claim != nil {
		claim.Accept(run.ID)
	}
	return jsonResult(pendingResult(sc, run, 0))
}
