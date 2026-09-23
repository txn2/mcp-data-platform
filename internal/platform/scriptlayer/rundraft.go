package scriptlayer

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// handleValidate parses a script without running it and reports what it would
// reach. The source may be sent inline (validating an edit before saving it) or
// resolved by name.
func (h *Handle) handleValidate(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	source, errResult := h.draftSource(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	report := scriptrun.WithDestinationCheck(scriptrun.Validate(source), h.destinations)
	out := map[string]any{
		"ok":                      report.OK,
		"findings":                report.Findings,
		"capabilities":            report.Capabilities,
		"tools":                   report.Tools,
		"connections":             report.Connections,
		"destinations":            report.Destinations,
		"refresh_targets":         report.RefreshTargets,
		"dynamic_connections":     report.DynamicConnections,
		"dynamic_destinations":    report.DynamicDestinations,
		"dynamic_refresh_targets": report.DynamicRefreshTargets,
		"dynamic_tools":           report.DynamicTools,
		"reads_state":             report.Reads,
		"saves_state":             report.Saves,
	}
	if report.DynamicConnections {
		out["connections_note"] = "At least one call computes its connection instead of naming one, or passes platform.call an argument set that cannot be read from the source, so this connection list is incomplete."
	}
	if report.DynamicDestinations {
		out["destinations_note"] = "At least one platform.export call computes its destination instead of naming one, so this destination list is incomplete."
	}
	if report.DynamicRefreshTargets {
		out["refresh_targets_note"] = "At least one platform.publish_data call computes the output name it refreshes instead of naming one, so this refresh-target list is incomplete."
	}
	if report.DynamicTools {
		out["tools_note"] = "At least one platform.call computes the tool it invokes instead of naming one, so this tool list is incomplete."
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
// before it is saved as the version that runs.
//
// It is deliberately NOT a platform run: it runs under tighter limits, and it
// executes the source as sent rather than the saved version. Sending no source
// runs the saved version, which is how an author dry-runs a script they have
// not edited.
//
// By default it persists nothing. platform.export previews, platform.save_state
// reports, and the engine's write barrier refuses every write-class call the
// source makes through platform.call (#1664). allow_writes lifts the barrier
// for one run and lets platform.export write for real (#1822), and the response
// then lists what the run wrote.
//
// A script need not be saved to be drafted (#1822). Source sent under a name no
// saved script of the caller's holds runs as that prospective script: its
// params bind against the params sent with it, and it reads the empty state a
// script that has never run reads.
func (h *Handle) handleRunDraft(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if errResult := refuseReentrantRun(ctx, ToolNameManageScript+" "+cmdRunDraft); errResult != nil {
		return errResult, nil, nil
	}
	sc, errResult := h.draftScript(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	if errResult := runnable(sc); errResult != nil {
		return errResult, nil, nil
	}
	source := script.DraftSource(input.Source, sc)
	if errResult := refuseDraftSource(source, h.destinations); errResult != nil {
		return errResult, nil, nil
	}
	// Values bind against the LIVE record's contract, which is the contract the
	// source being run was written against.
	params, err := script.BindParams(sc.Params, input.Args)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return errorResult(scriptdraft.ErrNoIdentity.Error()), nil, nil
	}
	outcome, err := scriptdraft.New(h.server, h.destinations).WithToolkits(h.toolkits).
		WithExports(h.draftExports).Run(ctx, scriptdraft.Request{
		Source: source, Name: sc.Name, Script: sc, Params: params,
		// The live state, so the draft reads what a platform run created now
		// would read. Nothing is written back: what the draft would have saved
		// is reported beside the outputs it would have written.
		State: h.liveState(ctx, sc),
		Identity: scriptdraft.Identity{
			UserID: pc.UserID, Email: pc.UserEmail, Claims: pc.UserClaims,
			Roles: pc.Roles, AuthType: pc.AuthType,
		},
		AllowWrites: input.AllowWrites,
	})
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return jsonResult(draftResult(sc, outcome))
}

// draftScript resolves the script a draft runs as: the saved one the name
// addresses, or, when source was sent under a name the caller has no saved
// script by, the script that source would be saved as.
func (h *Handle) draftScript(ctx context.Context, input manageScriptInput) (*script.Script, *mcp.CallToolResult) {
	// A prospective script is the caller's own: one addressed under another
	// owner is only ever a saved one.
	if input.Name == "" || input.Source == "" || input.OwnerEmail != "" {
		return h.readable(ctx, input)
	}
	saved, err := h.resolveScript(ctx, input.Name, input.OwnerEmail)
	if err != nil {
		return nil, errorResult(err.Error())
	}
	if saved != nil {
		return h.readable(ctx, input)
	}
	prospective := &script.Script{
		Name: input.Name, OwnerEmail: resolveEmail(ctx), Params: input.Params, Enabled: true,
	}
	if err := script.ValidateName(prospective.Name); err != nil {
		return nil, errorResult(err.Error())
	}
	if err := script.ValidateParams(prospective.Params); err != nil {
		return nil, errorResult(err.Error())
	}
	return prospective, nil
}

// draftSource resolves the source a validate acts on: the edit when one was
// sent, and the stored code otherwise. A name is only read when no source came
// with the call, so validating an inline edit needs no stored script.
func (h *Handle) draftSource(ctx context.Context, input manageScriptInput) (string, *mcp.CallToolResult) {
	if input.Source != "" {
		return input.Source, nil
	}
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return "", errResult
	}
	return sc.Source, nil
}

// refuseDraftSource applies the static read before a draft executes, so a draft
// refuses what the rest of the surface refuses: a source that cannot parse, one
// carrying an inline credential, and one naming a destination this deployment
// does not declare. Without it a draft run would be the one way to execute
// source every other path rejects, and the destination refusal would arrive
// only after the script's queries had run (#1415).
func refuseDraftSource(source string, destinations []script.Destination) *mcp.CallToolResult {
	report := scriptrun.WithDestinationCheck(scriptrun.Validate(source), destinations)
	if report.OK {
		return nil
	}
	detail := "the source does not pass validation, so it was not run"
	if len(report.Findings) > 0 {
		detail += ": " + report.Findings[0].Message
	}
	return errorResult(detail)
}

// runnable refuses a draft run of a script that has been taken out of service,
// asking the domain (script.RefuseDraftRun) so this surface and the portal
// editor refuse the same states in the same words.
func runnable(sc *script.Script) *mcp.CallToolResult {
	if err := script.RefuseDraftRun(sc); err != nil {
		return errorResult(err.Error())
	}
	return nil
}

// draftResult renders one draft run, successful or failed. A failed run still
// reports its log and metrics: the log is the whole reason to have run it.
func draftResult(sc *script.Script, outcome *scriptdraft.Outcome) map[string]any {
	result, runErr := outcome.Result, outcome.Err
	out := map[string]any{
		fieldName: sc.Name, "run_id": outcome.RunID, "draft": true,
		fieldStatus: "succeeded", "queries": 0, "exports": []scriptrun.ExportRecord{},
		"writes": []scriptrun.WriteRecord{},
	}
	if result != nil {
		out["log"] = result.Log
		out["log_truncated"] = result.LogTruncated
		out["steps"] = result.Steps
		out["duration_ms"] = result.Duration.Milliseconds()
		out["queries"] = result.Queries
		out["exports"] = orEmptyExports(result.Exports)
		out["writes"] = orEmptyWrites(result.Writes)
		// A draft reports what a platform run would hand back and its last
		// progress report, the same fields get_run carries (#1845, #1847).
		if result.Return != nil {
			out["result"] = result.Return
		}
		if result.Progress != nil {
			out["progress"] = result.Progress
		}
		if result.State != nil {
			out["state"] = orEmptyParams(result.State.Value)
		}
	}
	if runErr != nil {
		out[fieldStatus] = "failed"
		out["error"] = runErr.Error()
		out["retryable"] = false
		out["message"] = draftFailureMessage(result)
		if refused := refusedWriteOf(result); refused != nil {
			out["refused_write"] = refused
		}
	} else {
		out["message"] = outcome.Persisted("draft")
	}
	// A draft of a script that is not saved says so, because nothing names it
	// yet: no schedule fires it and no run_script reaches it until it is.
	if sc.ID == "" {
		out["saved"] = false
	}
	return out
}

// draftFailureMessage states why the draft ended, separating the two failures
// an author acts on differently: a script that is wrong, and a script that is
// right but wanted to write.
func draftFailureMessage(result *scriptrun.Result) string {
	if refused := refusedWriteOf(result); refused != nil {
		return "The draft stopped at a call that persists (refused_write), because a draft does not write. " +
			"Run it again with allow_writes to let it write for real, and it will report every write it made."
	}
	return "A script failure is deterministic: the same source on the same inputs fails the same way, so retrying it changes nothing. Fix the script and run the draft again."
}

// refusedWriteOf reads the barred call off a result, tolerating the nil result
// a run that never started leaves.
func refusedWriteOf(result *scriptrun.Result) *scriptrun.WriteRecord {
	if result == nil {
		return nil
	}
	return result.RefusedWrite
}

// writesOf reads the persisting calls off a result, tolerating the nil result a
// run that never started leaves.
func writesOf(result *scriptrun.Result) []scriptrun.WriteRecord {
	if result == nil {
		return nil
	}
	return result.Writes
}

// orEmptyWrites normalizes a nil write slice so the response carries a list
// rather than null: a reader checking what a draft persisted must be able to
// read the empty answer as an empty list.
func orEmptyWrites(writes []scriptrun.WriteRecord) []scriptrun.WriteRecord {
	if writes == nil {
		return []scriptrun.WriteRecord{}
	}
	return writes
}

// orEmptyExports normalizes a nil export slice so the response carries a list
// rather than null.
func orEmptyExports(exports []scriptrun.ExportRecord) []scriptrun.ExportRecord {
	if exports == nil {
		return []scriptrun.ExportRecord{}
	}
	return exports
}
