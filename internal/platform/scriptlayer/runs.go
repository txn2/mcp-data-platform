package scriptlayer

import (
	"context"
	"errors"
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
