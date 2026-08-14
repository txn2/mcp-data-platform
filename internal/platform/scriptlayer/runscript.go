package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// ToolNameRunScript is the MCP tool name of the approved-execution tool,
// exported for composition roots that bind UI apps to it.
const ToolNameRunScript = "run_script"

// Waiting policy for run_script.
const (
	// DefaultWaitSeconds is how long run_script waits for a run to finish when
	// the caller names no window.
	DefaultWaitSeconds = 120

	// MaxWaitSeconds caps the wait. Past it the tool answers with the run id and
	// a pending status rather than holding a request open for the ten minutes an
	// approved run is allowed to take.
	MaxWaitSeconds = 300

	// pollEvery is how often the wait re-reads the run row.
	//
	// The worker is woken by NOTIFY the moment a run is enqueued, so the poll
	// interval is what a caller waits AFTER the run finishes, not before it
	// starts. Holding a dedicated LISTEN connection per waiting tool call would
	// buy at most this interval and cost one connection per concurrent caller.
	pollEvery = 300 * time.Millisecond
)

// runScriptInput is the run_script argument set.
type runScriptInput struct {
	Name       string         `json:"name"`
	OwnerEmail string         `json:"owner_email,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	// WaitSeconds bounds how long the call waits for the run to finish. Zero
	// takes the default; a negative value returns as soon as the run is queued.
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

// registerRunScript registers the run_script tool.
func (h *Handle) registerRunScript(server *mcp.Server) {
	if h.runs == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolNameRunScript,
		Title:       "Run Script",
		Description: runScriptDescription,
		InputSchema: runScriptSchema(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input runScriptInput) (*mcp.CallToolResult, any, error) {
		return h.handleRunScript(ctx, input)
	})
}

// handleRunScript queues one execution of a script's APPROVED version and waits
// for it, within a bound.
//
// The tool never executes anything itself. It validates the request against the
// approved version's parameter contract, puts a row on the queue, and watches
// it — so a run is executed by a worker, under the script principal, whether it
// was asked for here or fired by a schedule, and there is exactly one path into
// execution to govern.
func (h *Handle) handleRunScript(ctx context.Context, input runScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.readable(ctx, manageScriptInput{Name: input.Name, OwnerEmail: input.OwnerEmail})
	if errResult != nil {
		return errResult, nil, nil
	}
	version, errResult := h.approvedVersion(ctx, sc)
	if errResult != nil {
		return errResult, nil, nil
	}
	// Parameters are bound against the APPROVED version's contract, not the live
	// row's: a pending draft may have added or renamed a parameter, and the run
	// executes the approved code, which knows nothing about it.
	params, err := script.BindParams(version.Params, input.Args)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	run, err := h.enqueueRun(ctx, sc, version, params)
	if err != nil {
		slog.Error("failed to queue a script run", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to queue the run"), nil, nil
	}
	return jsonResult(h.awaitRun(ctx, sc, run, waitBudget(input.WaitSeconds)))
}

// approvedVersion loads the version the execution gate points at, refusing the
// states that must not execute.
func (h *Handle) approvedVersion(ctx context.Context, sc *script.Script) (*script.Version, *mcp.CallToolResult) {
	if errResult := runnable(sc); errResult != nil {
		return nil, errResult
	}
	if sc.Status == script.StatusDeprecated {
		return nil, errorResult("this script is deprecated and is no longer executed; run its replacement instead")
	}
	if !sc.Executable() {
		return nil, errorResult(fmt.Sprintf(
			"script %q has no approved version, so nothing may execute it. Use manage_script run_draft to run it as yourself while you iterate, and ask an administrator to review and approve it before it can run on its own.",
			sc.Name))
	}
	if h.versions == nil {
		return nil, errorResult("this deployment cannot read script versions, so approved runs are unavailable")
	}
	version, err := h.versions.GetVersionByID(ctx, sc.ApprovedVersionID)
	if err != nil {
		slog.Error("failed to read an approved script version", fieldName, sc.Name, logKeyError, err)
		return nil, errorResult("failed to read the approved version")
	}
	if version == nil {
		return nil, errorResult("the approved version of this script is missing; it must be approved again before it can run")
	}
	return version, nil
}

// enqueueRun mints the run identity and puts the run on the queue.
//
// The run id is minted before the row exists because it is also the run's
// session id: the worker threads it onto the in-memory session the run drives,
// so every audit row the run produces carries it and the id handed back here
// identifies the whole run rather than just its queue entry.
func (h *Handle) enqueueRun(ctx context.Context, sc *script.Script, version *script.Version, params map[string]any) (*script.Run, error) {
	runID, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		return nil, fmt.Errorf("minting a run id: %w", err)
	}
	run := &script.Run{
		ID: runID, ScriptID: sc.ID, VersionID: version.ID, Version: version.Version,
		Trigger: script.TriggerTool, Params: params, RequestedBy: resolveEmail(ctx),
	}
	if err := h.runs.Enqueue(ctx, run); err != nil {
		return nil, fmt.Errorf("enqueueing the run: %w", err)
	}
	return run, nil
}

// waitBudget resolves the caller's wait window.
func waitBudget(seconds int) time.Duration {
	switch {
	case seconds < 0:
		return 0
	case seconds == 0:
		return DefaultWaitSeconds * time.Second
	case seconds > MaxWaitSeconds:
		return MaxWaitSeconds * time.Second
	default:
		return time.Duration(seconds) * time.Second
	}
}

// awaitRun watches a queued run until it finishes or the budget runs out, and
// renders whichever happened. A run that outlives the wait is not canceled or
// lost: the caller gets its id and follows it with manage_script get_run.
func (h *Handle) awaitRun(ctx context.Context, sc *script.Script, run *script.Run, budget time.Duration) map[string]any {
	deadline := time.Now().Add(budget)
	current := run
	for {
		if current.Terminal() {
			return runResult(sc, current)
		}
		if time.Now().After(deadline) {
			return pendingResult(sc, current, budget)
		}
		select {
		case <-ctx.Done():
			return pendingResult(sc, current, budget)
		case <-time.After(pollEvery):
		}
		latest, err := h.runs.GetRun(ctx, run.ID)
		if err != nil {
			// A read failing mid-wait says nothing about the run, which is being
			// executed by a worker elsewhere. Report it as pending with its id so
			// the caller can follow it rather than as a failure that did not
			// happen.
			if !errors.Is(err, script.ErrRunNotFound) {
				slog.Warn("failed to read a script run while waiting", "run_id", run.ID, logKeyError, err)
			}
			return pendingResult(sc, current, budget)
		}
		current = latest
	}
}

// runResult renders a finished run.
func runResult(sc *script.Script, run *script.Run) map[string]any {
	out := runSummary(sc, run)
	out["log"] = run.Log
	out["log_truncated"] = run.LogTruncated
	if run.Status == script.RunStatusFailed {
		out["error"] = run.Error
		out["retryable"] = false
		out["message"] = "A script failure is deterministic: the same version on the same inputs fails the same way, so the platform does not retry it. Fix the script, run the draft, and have the fix approved."
	}
	return out
}

// pendingResult renders a run that outlived the caller's wait, or one the
// caller asked not to wait for at all.
func pendingResult(sc *script.Script, run *script.Run, budget time.Duration) map[string]any {
	out := runSummary(sc, run)
	if budget <= 0 {
		out["message"] = fmt.Sprintf(
			"The run is queued and executes in the background. Read it with manage_script command=get_run run_id=%s.", run.ID)
		return out
	}
	out["message"] = fmt.Sprintf(
		"The run is still going after %s and continues in the background. Read it with manage_script command=get_run run_id=%s.",
		budget, run.ID)
	return out
}

// runSummary is the shared shape of every run a tool reports, so the answer to
// "run it" and the answer to "what happened" describe a run the same way.
func runSummary(sc *script.Script, run *script.Run) map[string]any {
	out := map[string]any{
		fieldName:    sc.Name,
		"run_id":     run.ID,
		fieldStatus:  run.Status,
		fieldVersion: run.Version,
		"trigger":    run.Trigger,
		"attempt":    run.Attempt,
		"queries":    run.Metrics.Queries,
		"outputs":    orEmptyOutputs(run.Outputs),
	}
	if run.StartedAt != nil {
		out["started_at"] = run.StartedAt.UTC()
	}
	if run.FinishedAt != nil {
		out["finished_at"] = run.FinishedAt.UTC()
		out["duration_ms"] = run.Metrics.DurationMS
	}
	return out
}

// orEmptyOutputs normalizes a nil output slice so a response carries a list
// rather than null.
func orEmptyOutputs(outputs []script.RunOutput) []script.RunOutput {
	if outputs == nil {
		return []script.RunOutput{}
	}
	return outputs
}

// runScriptDescription is the tool description an agent reads.
const runScriptDescription = `Execute a managed script's APPROVED version and return what it produced.

The platform runs the script itself, as the script's own principal, with the
capabilities its approval granted — not as you, and not with your access. A
script with no approved version cannot be run here: use manage_script
run_draft to execute a draft as yourself while you are still writing it.

Parameters are checked against the approved version's contract before anything
is queued. The call waits up to two minutes for the run to finish; a longer run
returns its run_id, keeps going, and is read with manage_script get_run.

A failed run is not retried. A script failure is deterministic — the same
version on the same inputs fails the same way — so the fix is to correct the
script and have the correction approved.`

// runScriptSchema is the closed input schema for run_script.
func runScriptSchema() any {
	return map[string]any{
		keyType: valObject,
		"properties": map[string]any{
			fieldName: map[string]any{
				keyType:        valString,
				keyDescription: "Name of the script to run.",
			},
			"owner_email": map[string]any{
				keyType:        valString,
				keyDescription: "Owner of the script; admins use it to address another owner's personal script.",
			},
			"args": map[string]any{
				keyType:        valObject,
				keyDescription: "Parameter values, checked against the approved version's declared parameters.",
			},
			"wait_seconds": map[string]any{
				keyType: valInteger,
				keyDescription: fmt.Sprintf(
					"How long to wait for the run to finish, in seconds (default %d, maximum %d). A negative value queues the run and returns immediately.",
					DefaultWaitSeconds, MaxWaitSeconds),
			},
		},
		"required":             []string{fieldName},
		"additionalProperties": false,
	}
}
