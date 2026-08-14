package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// handleValidate parses a script without running it and reports what it would
// reach. The source may be sent inline (validating an edit before saving it) or
// resolved by name.
func (h *Handle) handleValidate(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	source := input.Source
	if source == "" {
		sc, errResult := h.readable(ctx, input)
		if errResult != nil {
			return errResult, nil, nil
		}
		source = sc.Source
	}
	report := scriptrun.Validate(source)
	out := map[string]any{
		"ok":                   report.OK,
		"findings":             report.Findings,
		"capabilities":         report.Capabilities,
		"connections":          report.Connections,
		"destinations":         report.Destinations,
		"dynamic_connections":  report.DynamicConnections,
		"dynamic_destinations": report.DynamicDestinations,
	}
	if report.DynamicConnections {
		out["connections_note"] = "At least one platform.query call computes its connection instead of naming one, so this connection list is incomplete."
	}
	if report.DynamicDestinations {
		out["destinations_note"] = "At least one platform.export call computes its destination instead of naming one, so this destination list is incomplete."
	}
	if !report.OK {
		out["help"] = fmt.Sprintf("Call %s with command=help for the dialect contract and worked examples.", ToolNameManageScript)
	}
	return jsonResult(out)
}

// handleRunDraft executes a script for real, under the CALLING AUTHOR's own
// identity and persona, over an in-memory MCP session against the assembled
// server.
//
// This introduces no authority. The run carries the author's identity, so every
// platform call it makes is authenticated, authorized, rate limited, and
// audited exactly as the same call typed by that author directly would be:
// there is nothing reachable through run_draft that its caller could not
// already reach by calling the tools themselves. What it adds is the loop —
// real interpreter errors, real rows, real shapes — so a script is finished
// before anyone is asked to approve it.
//
// It is deliberately NOT a way around the execution gate: it persists nothing
// (platform.export previews), it runs under tighter limits than an approved run
// will, and it never reads or sets the approved-version pointer.
func (h *Handle) handleRunDraft(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	if errResult := runnable(sc); errResult != nil {
		return errResult, nil, nil
	}
	params, err := script.BindParams(sc.Params, input.Args)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	// The run id is minted BEFORE the session, because it is the session: it is
	// threaded onto the session context so every platform call the run makes
	// records the same session id in audit. Without that, a run issuing three
	// queries would write three unrelated ids and nothing would group them, and
	// the id handed back to the author would appear in no audit row at all.
	runID, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		return errorResult("failed to mint a run id"), nil, nil
	}
	caller, cleanup, err := h.connectAuthorSession(ctx, runID)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	defer cleanup()
	result, runErr := scriptrun.Run(ctx, scriptrun.Options{
		Source: sc.Source, Name: sc.Name, RunID: runID,
		// The fire time is pinned here, once, and handed to the script as
		// run.fire_time: even a draft never reads a clock, so what an author
		// verifies in the loop is what a scheduled run will do.
		FireTime: time.Now().UTC(), Params: params, Caller: caller,
	})
	return jsonResult(draftResult(sc, runID, result, runErr))
}

// runnable refuses a draft run of a script that has been taken out of service.
// run_draft is the only execution path that exists, so without this check
// "disabled" and "superseded" would disable and supersede nothing.
func runnable(sc *script.Script) *mcp.CallToolResult {
	if !sc.Enabled {
		return errorResult("this script is disabled; enable it with update enabled=true before running a draft")
	}
	if sc.Status == script.StatusSuperseded {
		return errorResult(fmt.Sprintf("this script was superseded by %q; run that one instead", sc.SupersededBy))
	}
	return nil
}

// draftResult renders one draft run, successful or failed. A failed run still
// reports its log and metrics: the log is the whole reason to have run it.
func draftResult(sc *script.Script, runID string, result *scriptrun.Result, runErr error) map[string]any {
	out := map[string]any{
		fieldName: sc.Name, "run_id": runID, "draft": true,
		fieldStatus: "succeeded", "queries": 0, "exports": []scriptrun.ExportRecord{},
	}
	if result != nil {
		out["log"] = result.Log
		out["log_truncated"] = result.LogTruncated
		out["steps"] = result.Steps
		out["duration_ms"] = result.Duration.Milliseconds()
		out["queries"] = result.Queries
		out["exports"] = orEmptyExports(result.Exports)
	}
	if runErr != nil {
		out[fieldStatus] = "failed"
		out["error"] = runErr.Error()
		out["retryable"] = false
		out["message"] = "A script failure is deterministic: the same source on the same inputs fails the same way, so retrying it changes nothing. Fix the script and run the draft again."
	} else {
		out["message"] = "Nothing was persisted. platform.export reported the shape of each output rather than writing it."
	}
	return out
}

// orEmptyExports normalizes a nil export slice so the response carries a list
// rather than null.
func orEmptyExports(exports []scriptrun.ExportRecord) []scriptrun.ExportRecord {
	if exports == nil {
		return []scriptrun.ExportRecord{}
	}
	return exports
}

// connectAuthorSession opens an in-memory MCP session against the assembled
// server, carrying the calling author's identity and tagged as a script run.
//
// The identity is copied from the caller's own PlatformContext rather than
// synthesized, which is what makes the "no new authority" property structural
// instead of a promise: the session authenticates as the person who called
// run_draft, and the same authorization middleware then resolves the same
// persona and the same connection rules it resolved for the manage_script call
// that got here. The source tag buys exactly two things, neither of them
// authority: audit rows that say a script ran, and the per-run session identity
// that keeps the run out of the author's own discovery and gate state. runID is
// threaded onto that context as the session identity, so the whole run is one
// session in audit rather than one session per platform call.
//
// An approved run differs from this in exactly one respect — it authenticates
// as the script principal with the roles its approval bound — which is why the
// session plumbing itself lives in scriptrun and only the identity is decided
// here.
func (h *Handle) connectAuthorSession(ctx context.Context, runID string) (*scriptrun.SessionCaller, func(), error) {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || pc.UserID == "" {
		return nil, nil, errors.New("run_draft needs an authenticated caller to run as")
	}

	serverCtx := middleware.WithSource(ctx, middleware.SourceScript)
	serverCtx = pkgsession.WithAwareSessionID(serverCtx, runID)
	serverCtx = middleware.WithPreAuthenticatedUser(serverCtx, &middleware.UserInfo{
		UserID:   pc.UserID,
		Email:    pc.UserEmail,
		Claims:   pc.UserClaims,
		Roles:    pc.Roles,
		AuthType: pc.AuthType,
	})
	caller, cleanup, err := scriptrun.Connect(serverCtx, h.server, "script-draft")
	if err != nil {
		return nil, nil, fmt.Errorf("opening the draft's session: %w", err)
	}
	return caller, cleanup, nil
}
