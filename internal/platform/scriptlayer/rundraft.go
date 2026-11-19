package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptres"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
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
	report := scriptlint.WithDestinationCheck(scriptrun.Validate(source), h.destinations)
	report = scriptlint.WithRegisterCheck(report, h.registerCheck(ctx))
	// The gate, as a save of this source would apply it (#1913): the script
	// named when it exists and the caller could save into it, otherwise a new
	// one. The findings' lines are formatted_source's, the stored form, and
	// the tests' report and the differences from the saved version are what a
	// save would be told (#1939, #1942).
	target := h.validateTarget(ctx, input)
	name := input.Name
	if target != nil {
		name = target.Name
	}
	gated := h.gate.Check(ctx, h.saveRequest(ctx, target, name, source, input))
	report = scriptlint.Merge(report, gated.Lint)
	out := map[string]any{
		"ok":                      report.OK && !gated.Refused(),
		"findings":                report.Findings,
		"formatted_source":        gated.Lint.Source,
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
		fieldLibrary:              report.Library,
		"libraries":               report.Libraries, "resources": report.Resources,
		// The managed-resource libraries the exports file in (#2080); {} is
		// the library of the person the run acts for.
		"resource_libraries": report.ResourceLibraries,
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
	addTestNotes(out, gated)
	if report.OK && gated.Refused() {
		out["save_refusal"] = gated.Refusal
	}
	if !report.OK {
		out["help"] = fmt.Sprintf("Call %s with command=help for the dialect contract and worked examples.", ToolNameManageScript)
	}
	return jsonResult(out)
}

// handleTest runs a script's tests (#1939): the saved script's, or the tests
// in the source sent. Every host call a test makes is answered from the
// recording it names, so nothing reaches an upstream and nothing is written.
func (h *Handle) handleTest(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	source, errResult := h.draftSource(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	target := h.validateTarget(ctx, input)
	caller := scriptsave.Caller{Email: resolveEmail(ctx), Admin: h.isAdminPersona(ctx)}
	report, err := scripttest.Run(ctx, scripttest.Request{
		Source: source, Name: orScriptName(input.Name), Destinations: h.destinations,
		Load: h.gate.Loader(target, caller), MaxMemoryBytes: h.runLimits.MaxMemoryBytes,
		Contracts: h.gate.Contracts, Libraries: scriptres.With(h.gate.Libraries, h.sources, nil, new(resourcewrite.CallerClaims(ctx))),
	})
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return jsonResult(map[string]any{
		"ok": report.OK(), "tests": report.Tests, "passed": report.Passed, "failed": report.Failed,
		"coverage": report.Coverage, "min_coverage_percent": scriptsave.MinCoverage, "unread": report.Unread,
	})
}

// orScriptName is the name a script is labeled with in a traceback.
func orScriptName(name string) string {
	if name == "" {
		return "script"
	}
	return name
}

// handleRecording returns one recording (#1939): what the run started from
// and every host call it made with the answer it was given. It holds upstream
// rows, so it is read under the rules run history is: the script's owner and
// administrators, and the author of a draft of a script not yet saved.
func (h *Handle) handleRecording(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if input.RunID == "" {
		return errorResult("run_id is required: the run or draft whose recording to read"), nil, nil
	}
	notFound := errorResult(fmt.Sprintf("no recording %q you can read", input.RunID))
	if h.recordings == nil {
		return notFound, nil, nil
	}
	stored, err := h.recordings.Get(ctx, input.RunID)
	if errors.Is(err, scriptrec.ErrNotFound) {
		return notFound, nil, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to read a script recording", logKeyError, err)
		return errorResult("failed to read the recording"), nil, nil
	}
	var sc *script.Script
	if stored.ScriptID != "" {
		sc, _ = h.store.GetByID(ctx, stored.ScriptID)
	}
	if !scriptsave.Readable(stored.Meta, sc, scriptsave.Caller{Email: resolveEmail(ctx), Admin: h.isAdminPersona(ctx)}) {
		return notFound, nil, nil
	}
	out := map[string]any{"recording": stored.Meta}
	if stored.Replayable() {
		rec, err := scriptrec.Decode(stored.Data)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}
		out["started_from"] = rec.Header
		out["calls"] = rec.Calls
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
// draftApplication is the application a draft names, its id and connection;
// none for "" (#2709).
func (h *Handle) draftApplication(ctx context.Context, ref string) (id, connection string, err error) {
	if ref == "" {
		return "", "", nil
	}
	if h.draftIn == nil {
		return "", "", errors.New("this deployment keeps no applications to draft in")
	}
	return h.draftIn(ctx, ref, resolveEmail(ctx), h.isAdminPersona(ctx))
}

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
	limit, background, err := h.longDrafts.Plan(input.TimeoutSeconds, h.isAdminPersona(ctx))
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	app, appConn, err := h.draftApplication(ctx, input.Application)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	exec := func(ctx context.Context, long scriptdraft.Long) (map[string]any, bool, error) {
		outcome, err := scriptdraft.New(h.server, h.destinations).WithToolkits(h.toolkits).
			WithExports(h.draftExports).WithMemoryBudget(h.runLimits.MaxMemoryBytes).
			WithStatementCeiling(h.runLimits.MaxStatementTimeout).
			WithRecordings(h.recordings).WithLibraries(h.gate.Libraries).WithResources(h.sources).
			WithTokens(h.endpoints.TokenIssuer()).Run(ctx, scriptdraft.Request{
			Source: source, Name: sc.Name, Script: sc, Params: params,
			// The live state, so the draft reads what a platform run created now
			// would read. Nothing is written back: what the draft would have saved
			// is reported beside the outputs it would have written.
			State: h.liveState(ctx, sc),
			Identity: scriptdraft.Identity{
				UserID: pc.UserID, Email: pc.UserEmail, Claims: pc.UserClaims,
				Roles: pc.Roles, AuthType: pc.AuthType, Resources: new(resourcewrite.CallerClaims(ctx)),
			},
			AllowWrites: input.AllowWrites, Long: long, Application: app, ApplicationConnection: appConn,
		})
		if err != nil {
			return nil, false, err //nolint:wrapcheck // the runner's sentence
		}
		return scriptdraft.Report(sc, outcome), outcome.Err != nil, nil
	}
	var out map[string]any
	if background {
		out, err = h.longDrafts.Begin(ctx, resolveEmail(ctx), sc.Name, limit, exec)
	} else {
		out, _, err = exec(ctx, scriptdraft.Long{Timeout: limit})
	}
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return jsonResult(out)
}

// draftScript resolves the script a draft runs as: the saved one the name
// addresses, or, when source was sent under a name the caller has no saved
// script by, the script that source would be saved as.
func (h *Handle) draftScript(ctx context.Context, input manageScriptInput) (*script.Script, *mcp.CallToolResult) {
	// A prospective script is the caller's own: one addressed under another
	// owner is only ever a saved one.
	if input.Name == "" || input.Source == "" || input.OwnerEmail != "" {
		return h.owned(ctx, input)
	}
	saved, err := h.resolveScript(ctx, input.Name, input.OwnerEmail, false)
	if err != nil {
		return nil, errorResult(err.Error())
	}
	if saved != nil {
		return h.owned(ctx, input)
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
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return "", errResult
	}
	return sc.Source, nil
}

// refuseDraftSource refuses a draft whose source every other path would refuse
// (scriptlint.DraftRefusal).
func refuseDraftSource(source string, destinations []script.Destination) *mcp.CallToolResult {
	if detail := scriptlint.DraftRefusal(source, destinations); detail != "" {
		return errorResult(detail)
	}
	return nil
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

// tableTargetChecker is the half of the asset toolkit validate asks whether a
// table could be registered on a connection (#2213); pkg/toolkits/portal
// implements it.
type tableTargetChecker interface {
	CheckTableTarget(ctx context.Context, connection string) error
}

// registerCheck is the check validate folds into its report for an export's
// register= connections, as the caller in ctx. Nil when no toolkit can
// answer, which leaves the report as the source alone decides it.
func (h *Handle) registerCheck(ctx context.Context) scriptlint.RegisterCheck {
	if h.toolkits == nil {
		return nil
	}
	for _, tk := range h.toolkits.All() {
		if checker, ok := tk.(tableTargetChecker); ok {
			return func(connection string) error { return checker.CheckTableTarget(ctx, connection) }
		}
	}
	return nil
}
