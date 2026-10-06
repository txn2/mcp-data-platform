package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/libraryuse"
	"github.com/txn2/mcp-data-platform/internal/openrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexamples"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// logKeyError is the slog key for error values.
const logKeyError = "error"

// handleCreate creates a script. The source is validated before the row exists,
// so a script that cannot parse never reaches the store: an unparseable script
// is not a draft, it is a typo, and keeping it out means every stored version
// is one a reviewer could meaningfully read.
func (h *Handle) handleCreate(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc := &script.Script{
		Name: input.Name, DisplayName: input.DisplayName, Description: input.Description,
		Category: derefOr(input.Category), Source: input.Source, Params: input.Params,
		Tags:       orEmpty(input.Tags),
		OwnerEmail: resolveEmail(ctx), Enabled: true, Status: script.StatusActive,
	}
	if err := sc.Validate(); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if report := scriptrun.Validate(sc.Source); !report.OK {
		return jsonResult(refusedReport("the source does not parse, so it was not saved", report))
	}
	sent := sc.Source
	gated := h.gate.Check(ctx, h.saveRequest(ctx, nil, sc.Name, sent, input))
	if gated.Refused() {
		return jsonResult(saveRefusal(gated))
	}
	gated.Apply(sc)
	author := callerAuthor(ctx)
	if err := h.store.Create(ctx, sc, author); err != nil {
		slog.Error("failed to create script", fieldName, input.Name, logKeyError, err)
		return errorResult("failed to create script"), nil, nil
	}
	h.keepRecordings(ctx, sc)
	out := map[string]any{
		fieldStatus: "created", "id": sc.ID, fieldName: sc.Name, fieldVersion: sc.Version,
		"next": "Saved, and it runs: run_script executes it under the access you held when you saved it, and a schedule you set will fire it. Use run_draft to iterate on changes before saving them.",
	}
	if sc.Library {
		out["library"] = true
		out["next"] = fmt.Sprintf("Saved as a library, which is never run itself. A script loads this version with "+
			"load(\"lib:%s@%d\", \"<function>\"); every later save is a new version, and a script keeps the version it names.", sc.Name, sc.Version)
	}
	addDescriptionNotice(out, sc)
	addGateNotes(out, sent, gated)
	return jsonResult(out)
}

// saveRequest is the gate's request for a save of source into existing (nil
// for a new script) by the caller, carrying the change summary they sent.
func (h *Handle) saveRequest(ctx context.Context, existing *script.Script, name, source string, input manageScriptInput) scriptsave.Request {
	return scriptsave.Request{
		Existing: existing, Name: name, Source: source,
		Caller:        scriptsave.Caller{Email: resolveEmail(ctx), Admin: h.isAdminPersona(ctx)},
		ChangeSummary: input.ChangeSummary, Agreed: input.UserAgreed,
	}
}

// keepRecordings keeps the recordings the saved source's tests name past the
// retention sweep. A failure is logged: the save landed.
func (h *Handle) keepRecordings(ctx context.Context, sc *script.Script) {
	if err := h.gate.Keep(ctx, sc, resolveEmail(ctx)); err != nil {
		slog.Warn("failed to keep the recordings a script's tests name", fieldName, sc.Name, logKeyError, err)
	}
}

// addDescriptionNotice attaches the non-blocking signal that a description has
// outgrown the script it documents. It is advisory in the response for the same
// reason it is advisory in the domain: the write already succeeded, and this is
// a suggestion about where the prose might live better, not a complaint about
// the script.
func addDescriptionNotice(out map[string]any, sc *script.Script) {
	if notice := script.DescriptionNotice(sc.Description); notice != "" {
		out["description_notice"] = notice
	}
}

// derefOr reads an optional string, yielding "" when the caller sent nothing.
func derefOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// orEmpty normalizes a nil slice to an empty one.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// handleUpdate edits a script through the domain's one gate. Which fields the
// caller sent is decided by their zero value except for enabled, which is a
// pointer precisely because false is a meaningful value.
func (h *Handle) handleUpdate(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	existing, errResult := h.editable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	before := *existing
	gated, errResult := h.applyUpdates(ctx, existing, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	var extra map[string]any
	if gated != nil {
		extra = map[string]any{}
		addGateNotes(extra, input.Source, *gated)
	}
	return h.persist(ctx, &before, existing, extra)
}

// applyUpdates mutates the script in place from the sent fields, then validates
// the resulting record as a whole. The gates' result is returned when source
// was sent, nil otherwise.
func (h *Handle) applyUpdates(ctx context.Context, sc *script.Script, input manageScriptInput) (*scriptsave.Result, *mcp.CallToolResult) {
	gated, errResult := h.gateSource(ctx, sc, input.Source, "the edit", input)
	if errResult != nil {
		return nil, errResult
	}
	if input.Params != nil {
		sc.Params = input.Params
	}
	if input.Tags != nil {
		sc.Tags = input.Tags
	}
	applyStringFields(sc, input)
	if input.Enabled != nil {
		sc.Enabled = *input.Enabled
	}
	if input.Exclusive != nil {
		sc.Exclusive = *input.Exclusive
	}
	if errResult := h.applyStatus(ctx, sc, input); errResult != nil {
		return nil, errResult
	}
	if err := sc.Validate(); err != nil {
		return nil, errorResult(err.Error())
	}
	return gated, nil
}

// gateSource puts new source for sc through the validator and the save gate
// (internal/platform/scriptsave) and, when both pass, sets it as the formatted
// source with the change it carries. Empty source means none was sent:
// nothing is checked and nil is returned. what names the change in a refusal
// ("the edit", "the patch").
func (h *Handle) gateSource(ctx context.Context, sc *script.Script, source, what string, input manageScriptInput) (*scriptsave.Result, *mcp.CallToolResult) {
	if source == "" {
		return nil, nil
	}
	if report := scriptrun.Validate(source); !report.OK {
		result, _, _ := jsonResult(refusedReport("the source does not parse, so "+what+" was not saved", report))
		return nil, result
	}
	gated := h.gate.Check(ctx, h.saveRequest(ctx, sc, sc.Name, source, input))
	if gated.Refused() {
		result, _, _ := jsonResult(saveRefusal(gated))
		return nil, result
	}
	gated.Apply(sc)
	return &gated, nil
}

// applyStringFields copies the plain descriptive fields a caller sent.
func applyStringFields(sc *script.Script, input manageScriptInput) {
	if input.DisplayName != "" {
		sc.DisplayName = input.DisplayName
	}
	if input.Description != "" {
		sc.Description = input.Description
	}
	if input.Category != nil {
		sc.Category = *input.Category
	}
}

// applyStatus applies the lifecycle change, the one edit that needs an
// authority check and a transition rule. Ownership is not editable here: moving
// a script to another person is an administrator's action with its own route
// and its own record (script.Script.Transfer).
func (h *Handle) applyStatus(ctx context.Context, sc *script.Script, input manageScriptInput) *mcp.CallToolResult {
	if input.Status != "" && input.Status != sc.Status {
		if !h.isAdminPersona(ctx) {
			return errorResult("only admins can change a script's lifecycle status")
		}
		if err := sc.ApplyStatusTransition(input.Status, input.SupersededBy, time.Now().UTC()); err != nil {
			return errorResult(err.Error())
		}
	}
	return nil
}

// persist lands an edit through script.ApplyEdit and reports the outcome. extra
// carries command-specific fields (a patch report) into the response.
func (h *Handle) persist(ctx context.Context, before, after *script.Script, extra map[string]any) (*mcp.CallToolResult, any, error) {
	err := script.ApplyEdit(ctx, h.store, script.Edit{
		Before: before, After: after, Author: callerAuthor(ctx),
	})
	if err != nil {
		return editError(err), nil, nil
	}
	if after.Source != before.Source {
		h.keepRecordings(ctx, after)
	}
	out := map[string]any{fieldName: after.Name, fieldVersion: after.Version}
	maps.Copy(out, extra)
	out[fieldStatus] = "updated"
	addDescriptionNotice(out, after)
	out[fieldMessage] = script.SavedMessage(after)
	return jsonResult(out)
}

// editError maps an edit failure to a caller-facing message. The one the
// caller can act on is named; anything else is an internal failure whose
// detail stays in the log.
func editError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, script.ErrVersionConflict):
		return errorResult(err.Error())
	case errors.Is(err, openrun.ErrOpen):
		return errorResult(openrun.BlockedMessage)
	default:
		slog.Error("failed to update script", logKeyError, err)
		return errorResult("failed to update script")
	}
}

// handleDelete removes a script and its version history. A script is one
// person's, so a delete takes its schedule and its history with it and nobody
// else loses anything; the caller has already been established as its owner or
// an administrator by editable.
//
// It answers with the same account of the cascade the portal route answers
// with, composed by script.DeleteMessage: an agent deleting a script on
// somebody's behalf has to be able to tell them what went with it and what
// stayed, and a bare status left it inventing the answer (#1593).
func (h *Handle) handleDelete(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	existing, errResult := h.editable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	removed, err := h.store.Delete(ctx, existing.ID)
	var inUse *libraryuse.InUseError
	if errors.As(err, &inUse) {
		return errorResult(inUse.Error()), nil, nil
	}
	if err != nil {
		slog.Error("failed to delete script", fieldName, existing.Name, logKeyError, err)
		return errorResult("failed to delete script"), nil, nil
	}
	return jsonResult(map[string]any{
		fieldStatus: "deleted", fieldName: existing.Name,
		fieldMessage: script.DeleteMessage(existing.Name, removed),
	})
}

// handleGet returns one script with its full source and parameter contract. A
// built-in example resolves here too, so an author reads a worked script with
// the same command they read their own with.
func (h *Handle) handleGet(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.viewable(ctx, input)
	if errResult != nil {
		// A built-in example answers only when no stored script does, so a real
		// script named after an example is never shadowed by it.
		if ex, ok := scriptexamples.Lookup(input.Name); ok {
			return jsonResult(exampleFields(ex))
		}
		return errResult, nil, nil
	}
	fields := scriptFields(sc)
	// The runs are the owner's: what a run returned and logged was produced
	// with its author's roles (#1866).
	if h.ownsOrAdmin(ctx, sc) {
		fields["live_runs"] = h.liveRuns(ctx, sc)
	}
	return jsonResult(fields)
}

// liveRunsLimit caps the runs a script lists as not yet ended.
const liveRunsLimit = 20

// liveRuns lists the script's runs that have not ended, with who holds each
// and whether that worker is still reporting (#1860), so an orphaned or
// looping run is seen by anyone looking at the script, not only by someone who
// already knows its run id. A read that fails lists none rather than failing
// the script's own read.
func (h *Handle) liveRuns(ctx context.Context, sc *script.Script) []map[string]any {
	out := []map[string]any{}
	if h.runs == nil {
		return out
	}
	runs, err := h.runs.ListRuns(ctx, script.RunFilter{ScriptID: sc.ID, Live: true, Limit: liveRunsLimit})
	if err != nil {
		slog.Warn("failed to list a script's live runs", fieldName, sc.Name, logKeyError, err)
		return out
	}
	for i := range runs {
		summary := runSummary(sc, &runs[i])
		if msg := livenessMessage(&runs[i], time.Now()); msg != "" {
			summary[fieldMessage] = msg
		}
		out = append(out, summary)
	}
	return out
}

// scriptFields renders one script for a get response.
func scriptFields(sc *script.Script) map[string]any {
	return map[string]any{
		"id": sc.ID, fieldName: sc.Name, "display_name": sc.DisplayName,
		"description": sc.Description, fieldSource: sc.Source, "params": sc.Params,
		"owner_email": sc.OwnerEmail,
		"category":    sc.Category, "tags": sc.Tags, "enabled": sc.Enabled, fieldStatus: sc.Status,
		"exclusive":       sc.Exclusive,
		fieldVersion:      sc.Version,
		"executable_note": script.ExecutionNote(sc),
		"created_at":      sc.CreatedAt, "updated_at": sc.UpdatedAt,
	}
}

// handleList returns the scripts the caller may see, which is every script on
// the platform (#1795).
//
// It used to be the caller's own unless they held an admin persona. Widening
// it is the point: an agent asked to write a weekly report should be able to
// see that one already exists rather than writing a second copy of it. Each
// row carries name, display name, description, owner, status, version,
// category and tags, and no source: that is a payload choice, since a listing
// runs to hundreds of rows. The source is everyone signed in's to read, with
// command=get_content (#2027).
func (h *Handle) handleList(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	filter := script.ListFilter{
		Status: input.Status, Search: input.Search, Limit: input.Limit,
		Category: derefOr(input.Category), Tags: input.Tags,
	}
	scripts, err := h.store.List(ctx, filter)
	if err != nil {
		slog.Error("failed to list scripts", logKeyError, err)
		return errorResult("failed to list scripts"), nil, nil
	}
	items := make([]map[string]any, 0, len(scripts))
	for i := range scripts {
		sc := &scripts[i]
		items = append(items, map[string]any{
			fieldName: sc.Name, "display_name": sc.DisplayName, "description": sc.Description,
			"owner_email": sc.OwnerEmail, fieldStatus: sc.Status,
			fieldVersion: sc.Version,
			"category":   sc.Category, "tags": sc.Tags, "library": sc.Library,
		})
	}
	return jsonResult(map[string]any{"scripts": items, "count": len(items)})
}

// refusedReport renders a validation refusal: the reason, plus the findings an
// author needs to fix it, in one response so the fix takes one round trip.
func refusedReport(reason string, report scriptrun.Report) map[string]any {
	return map[string]any{
		fieldStatus:  "invalid",
		fieldMessage: reason,
		"findings":   report.Findings,
		"help":       fmt.Sprintf("Call %s with command=help for the dialect contract and worked examples.", ToolNameManageScript),
	}
}

// contentVerb runs a read-only content verb over a script's source: resolve the
// script, build the body fields, stamp the script's identity onto them.
func (h *Handle) contentVerb(
	ctx context.Context,
	input manageScriptInput,
	build func(body string) (map[string]any, error),
) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.viewable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	fields, err := build(sc.Source)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	maps.Copy(fields, identity(sc))
	return jsonResult(fields)
}

// validateTarget is the script a validate call's source would be saved into:
// the one named, when the caller could save into it, or nil for a new script.
func (h *Handle) validateTarget(ctx context.Context, input manageScriptInput) *script.Script {
	if input.Name == "" {
		return nil
	}
	sc, errResult := h.owned(ctx, input)
	if errResult != nil {
		return nil
	}
	return sc
}

// saveRefusal renders a save the gate refused: the reason, every finding on
// the formatted source (the refused ones as errors), the formatted source the
// findings' lines refer to, and the tests' report and behavior comparison when
// the gate got that far.
func saveRefusal(res scriptsave.Result) map[string]any {
	out := map[string]any{
		fieldStatus:        "invalid",
		fieldMessage:       res.Refusal,
		"findings":         res.Lint.Findings,
		"formatted_source": res.Lint.Source,
		"help": fmt.Sprintf("Fix what the message names and save again. Line numbers refer to formatted_source, "+
			"which is how the script is stored. Call %s with command=help for the rules.", ToolNameManageScript),
	}
	addTestNotes(out, res)
	return out
}

// addTestNotes carries the tests' report and the behavior comparison into a
// response.
func addTestNotes(out map[string]any, res scriptsave.Result) {
	if res.Tests != nil {
		out["tests"] = res.Tests
	}
	if len(res.Differences) > 0 || len(res.Replayed) > 0 {
		out["differences"] = res.Differences
		out["replayed"] = res.Replayed
	}
	if res.ChangeNeeded {
		out["change_needed"] = true
	}
}

// addGateNotes tells the author of a saved version what the gates did: that
// the stored source is the formatted one.
func addGateNotes(out map[string]any, sent string, gated scriptsave.Result) {
	res := gated.Lint
	if len(res.Warnings) > 0 {
		// Saved, with what the lint warns about: a warning never refuses a save.
		out["warnings"] = res.Warnings
	}
	if res.Source != sent {
		out["source_formatted"] = true
		out["formatted_note"] = "The source was stored in the canonical format; read it back with get before patching it."
	}
	addTestNotes(out, gated)
}
