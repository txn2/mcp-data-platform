package scriptlayer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/runcontrol"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptgrant"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ToolNameRunScript is the MCP tool name of the platform-execution tool,
// exported for composition roots that bind UI apps to it.
const ToolNameRunScript = "run_script"

// Waiting policy for run_script.
const (
	// DefaultWaitSeconds is how long run_script waits for a run to finish when
	// the caller names no window.
	DefaultWaitSeconds = 120

	// MaxWaitSeconds caps the wait, the same cap the portal's run route
	// applies (runcontrol.MaxWaitSeconds). Past it the tool answers with the
	// run id and a pending status rather than holding a request open for the
	// length of a run.
	MaxWaitSeconds = runcontrol.MaxWaitSeconds
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
		// A run executes a saved script, which reaches whatever tool the
		// script calls, so it claims the widest reach any of them has.
		Annotations: toolkit.WriteAnnotations(true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input runScriptInput) (*mcp.CallToolResult, any, error) {
		return h.handleRunScript(ctx, input)
	})
}

// handleRunScript queues one execution of a script's latest saved version and
// waits for it, within a bound.
//
// The tool never executes anything itself. It validates the request against
// the script's parameter contract, puts a row on the queue, and watches it —
// so a run is executed by a worker, under the script principal, whether it was
// asked for here or fired by a schedule, and there is exactly one path into
// execution to govern.
func (h *Handle) handleRunScript(ctx context.Context, input runScriptInput) (*mcp.CallToolResult, any, error) {
	if errResult := refuseReentrantRun(ctx, ToolNameRunScript); errResult != nil {
		return errResult, nil, nil
	}
	sc, errResult := h.owned(ctx, manageScriptInput{Name: input.Name, OwnerEmail: input.OwnerEmail})
	if errResult != nil {
		return errResult, nil, nil
	}
	version, errResult := h.currentVersion(ctx, sc)
	if errResult != nil {
		return errResult, nil, nil
	}
	params, err := scriptgrant.BindCaller(version.Params, input.Args, callerClaims(ctx))
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

// callerClaims are the calling session's claims, which a parameter bound to
// caller.<claim> reads (#1846); nil outside an authenticated call.
func callerClaims(ctx context.Context) map[string]any {
	if pc := middleware.GetPlatformContext(ctx); pc != nil {
		return pc.UserClaims
	}
	return nil
}

// currentVersion loads the script's latest saved version — the one a run
// executes — refusing the states that must not execute.
func (h *Handle) currentVersion(ctx context.Context, sc *script.Script) (*script.Version, *mcp.CallToolResult) {
	if err := script.RefuseRun(sc); err != nil {
		return nil, errorResult(err.Error() + draftHint(sc))
	}
	if h.versions == nil {
		return nil, errorResult("this deployment cannot read script versions, so platform runs are unavailable")
	}
	version, err := h.versions.GetVersion(ctx, sc.ID, sc.Version)
	if err != nil {
		slog.Error("failed to read a script version", fieldName, sc.Name, logKeyError, err)
		return nil, errorResult("failed to read the script's current version")
	}
	if version == nil {
		return nil, errorResult("the script's current version is missing from its history; save it again to restore it")
	}
	return version, nil
}

// draftHint offers run_draft only where a draft would actually be admitted.
// The two gates refuse different sets — a deprecated script may still be
// draft-run by the person fixing it, while a disabled or superseded one is
// refused by both — so naming run_draft unconditionally would send somebody to
// a second refusal.
func draftHint(sc *script.Script) string {
	if script.RefuseDraftRun(sc) != nil {
		return ""
	}
	return "; use manage_script run_draft to execute it as yourself while you iterate"
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
	current, finished, err := runcontrol.AwaitRun(ctx, h.runs, run, budget, runcontrol.PollEvery)
	if err != nil && !errors.Is(err, script.ErrRunNotFound) {
		// A read failing mid-wait says nothing about the run, which a worker
		// is executing elsewhere; it is reported as pending with its id.
		slog.Warn("failed to read a script run while waiting", "run_id", run.ID, logKeyError, err)
	}
	if finished {
		return runResult(sc, current)
	}
	return pendingResult(sc, current, budget)
}

// runResult renders a finished run.
func runResult(sc *script.Script, run *script.Run) map[string]any {
	out := runSummary(sc, run)
	out["log"] = run.Log
	out["log_truncated"] = run.LogTruncated
	// The state read is an input of the run exactly as params are, and what
	// it saved is part of what it did (#1537): a run explains itself from its
	// own row, including the revision it read and the one it wrote.
	out["state_revision"] = run.StateRevision
	out["state_read"] = orEmptyParams(run.StateRead)
	if run.StateWritten != nil {
		out["state_written"] = run.StateWritten
		out["state_revision_written"] = run.StateRevisionWritten
	}
	// The value the run handed back with platform.result (#1845), which is
	// what a caller running a script for an answer came for.
	if run.Result != nil {
		out["result"] = run.Result
	}
	out["metrics"] = run.Metrics
	switch run.Status {
	case script.RunStatusFailed:
		// Why it failed decides whether running it again helps (#1859).
		cause := cmp.Or(run.Cause, runstate.CauseScript)
		out["error"] = run.Error
		out["cause"] = cause
		out["retryable"] = runstate.CauseRetryable(cause)
		out["message"] = failureMessages[cause]
	case script.RunStatusRunning:
		if msg := livenessMessage(run, time.Now()); msg != "" {
			out["message"] = msg
		}
	case script.RunStatusCanceled:
		out["error"] = run.Error
		out["message"] = "The run was canceled. The outputs it wrote before it stopped are listed and stand."
	}
	return out
}

// failureMessages says, for each cause a run fails with, what it means for
// whoever reads the run: whether there is something in the script to fix, and
// whether running it again is expected to succeed (#1859, #1860, #1861).
var failureMessages = map[string]string{
	runstate.CauseScript: "A script failure is deterministic: the same version on the same inputs fails the same way, " +
		"so the platform does not retry it. Fix the script with run_draft and save the fix.",
	runstate.CauseUpstream: "The run failed because a service it called was temporarily unavailable: it timed out, " +
		"dropped the connection, or kept refusing after the platform waited and retried. This is usually temporary " +
		"and there is nothing in the script to fix; run it again later, and a schedule tries again at its next fire.",
	runstate.CauseMemory: "The run held more memory than it is allowed (scripts.worker.max_run_memory), or more than " +
		"its replica had, and fails the same way until it holds less. Page the work, export each page with " +
		"platform.export(..., append=True), and keep only what the next page needs; metrics.peak_memory_bytes is " +
		"what it reached.",
	runstate.CauseWorkerLost: "The worker executing this run stopped without reporting a result each time it was " +
		"tried, most often because the run used more memory than its replica has, so it is not run again. Page the " +
		"work and export each page with platform.export(..., append=True).",
	runstate.CausePlatform: "The platform could not execute the run: its session or its script could not be read. " +
		"There is nothing in the script to fix; run it again.",
	runstate.CauseStateConflict: "Another run of this script saved its state first, so this run's platform.save_state " +
		"was refused; the outputs it wrote stand. Run it again and it reads the state the other run saved.",
}

// livenessMessage says what is happening to a running run whose worker is not
// reporting, so it never reads as a run being executed (#1860). Empty for a
// run whose worker is.
func livenessMessage(run *script.Run, now time.Time) string {
	switch run.Liveness(now) {
	case runstate.LivenessUnresponsive:
		return fmt.Sprintf("The worker executing this run (%s) stopped reporting at %s and is most likely gone, "+
			"a replica that was killed or restarted. The run is taken over when its lease ends at %s, or failed if "+
			"its reclaims are spent; cancel_run ends it now.",
			run.LockedBy, timeText(run.HeartbeatAt), timeText(run.LockedUntil))
	case runstate.LivenessLeaseExpired:
		return fmt.Sprintf("The worker that held this run (%s) stopped reporting and its lease ended at %s. The next "+
			"worker to claim it takes it over, or fails it if its reclaims are spent; cancel_run ends it now.",
			run.LockedBy, timeText(run.LockedUntil))
	default:
		return ""
	}
}

// timeText renders a time a message names, or "an unknown time".
func timeText(t *time.Time) string {
	if t == nil {
		return "an unknown time"
	}
	return t.UTC().Format(time.RFC3339)
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
	// The latest platform.progress report (#1847): on a running run it is
	// how far the run has got, on a finished one the last thing it said.
	if run.Progress != nil {
		out["progress"] = run.Progress
	}
	if run.CancelRequestedAt != nil && !run.Terminal() {
		out["cancel_requested_by"] = run.CancelRequestedBy
	}
	holderFields(out, run)
	return out
}

// holderFields adds who holds a run and how each earlier attempt ended
// (#1860), so an orphaned or looping run is visible without the run table.
func holderFields(out map[string]any, run *script.Run) {
	out["reclaims"] = run.Reclaims
	if len(run.Attempts) > 0 {
		out["attempts"] = run.Attempts
	}
	if run.Status != script.RunStatusRunning {
		return
	}
	out["liveness"] = run.Liveness(time.Now())
	out["locked_by"] = run.LockedBy
	out["locked_until"] = run.LockedUntil
	out["heartbeat_at"] = run.HeartbeatAt
	out["claimed_at"] = run.ClaimedAt
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
const runScriptDescription = `Execute a managed script's latest saved version and return what it produced.

The platform runs the script itself, as the script's own principal, presenting
the roles its author held when that version was saved — not as you, and not with your
access. Use manage_script run_draft to execute unsaved changes as yourself
while you are still writing them.

Parameters are checked against the script's contract before anything is
queued. The call waits up to two minutes for the run to finish; a longer run
returns its run_id, keeps going, and is read with manage_script get_run, which
shows its latest progress and the log so far while it runs. A run that set a
value with platform.result returns it as "result". manage_script cancel_run
stops a run: a queued one never starts, a running one ends canceled.

A failed run is not retried. A script failure is deterministic — the same
version on the same inputs fails the same way — so the fix is to correct the
script and save the correction.`

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
				keyDescription: "Owner of the script; admins use it to address another person's script.",
			},
			"args": map[string]any{
				keyType:        valObject,
				keyDescription: "Parameter values, checked against the script's declared parameters.",
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
