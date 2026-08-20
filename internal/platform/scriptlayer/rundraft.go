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
		"ok":                      report.OK,
		"findings":                report.Findings,
		"capabilities":            report.Capabilities,
		"connections":             report.Connections,
		"destinations":            report.Destinations,
		"refresh_targets":         report.RefreshTargets,
		"dynamic_connections":     report.DynamicConnections,
		"dynamic_destinations":    report.DynamicDestinations,
		"dynamic_refresh_targets": report.DynamicRefreshTargets,
	}
	if report.DynamicConnections {
		out["connections_note"] = "At least one platform.query call computes its connection instead of naming one, so this connection list is incomplete."
	}
	if report.DynamicDestinations {
		out["destinations_note"] = "At least one platform.export call computes its destination instead of naming one, so this destination list is incomplete."
	}
	if report.DynamicRefreshTargets {
		out["refresh_targets_note"] = "At least one platform.publish_data call computes the output name it refreshes instead of naming one, so this refresh-target list is incomplete."
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
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return errorResult(scriptdraft.ErrNoIdentity.Error()), nil, nil
	}
	outcome, err := scriptdraft.New(h.server).Run(ctx, scriptdraft.Request{
		Source: sc.Source, Name: sc.Name, Params: params,
		Identity: scriptdraft.Identity{
			UserID: pc.UserID, Email: pc.UserEmail, Claims: pc.UserClaims,
			Roles: pc.Roles, AuthType: pc.AuthType,
		},
	})
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return jsonResult(draftResult(sc, outcome))
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
