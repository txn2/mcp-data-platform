package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// defaultRunsLimit caps a run listing that names no limit.
const defaultRunsLimit = 20

// handleRuns lists a script's run history, newest first.
func (h *Handle) handleRuns(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.runs == nil {
		return errorResult("this deployment keeps no script runs"), nil, nil
	}
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultRunsLimit
	}
	runs, err := h.runs.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID, Status: input.RunStatus, Limit: limit})
	if err != nil {
		slog.Error("failed to list script runs", fieldName, sc.Name, logKeyError, err)
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
	run, err := h.runs.GetRun(ctx, input.RunID)
	if errors.Is(err, script.ErrRunNotFound) {
		return errorResult("run not found"), nil, nil
	}
	if err != nil {
		slog.Error("failed to read a script run", "run_id", input.RunID, logKeyError, err)
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
		slog.Error("failed to read the script a run belongs to", "run_id", run.ID, logKeyError, err)
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
	sc, errResult := h.readable(ctx, input)
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
		slog.Error("failed to read script state", fieldName, sc.Name, logKeyError, err)
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
		slog.Error("failed to write script state", fieldName, sc.Name, logKeyError, err)
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
	if h.states == nil {
		return nil
	}
	st, err := h.states.GetState(ctx, sc.ID)
	if err != nil {
		slog.Warn("failed to read script state for a draft; the draft reads {}", fieldName, sc.Name, logKeyError, err)
		return nil
	}
	return st.Value
}
