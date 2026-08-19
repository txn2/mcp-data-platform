package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// logKeyError is the slog key for error values.
const logKeyError = "error"

// handleCreate creates a script. The source is validated before the row exists,
// so a script that cannot parse never reaches the store: an unparseable script
// is not a draft, it is a typo, and keeping it out means every stored version
// is one a reviewer could meaningfully read.
func (h *Handle) handleCreate(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	scope := input.Scope
	if scope == "" {
		scope = script.ScopePersonal
	}
	sc := &script.Script{
		Name: input.Name, DisplayName: input.DisplayName, Description: input.Description,
		Category: derefOr(input.Category), Source: input.Source, Params: input.Params, Scope: scope,
		Personas: orEmpty(input.Personas), Tags: orEmpty(input.Tags),
		OwnerEmail: resolveEmail(ctx), Enabled: true, Status: script.StatusDraft,
	}
	if err := sc.Validate(); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if !h.isAdminPersona(ctx) && scope != script.ScopePersonal {
		return errorResult("only admins can create global or persona-scoped scripts"), nil, nil
	}
	if report := scriptrun.Validate(sc.Source); !report.OK {
		return jsonResult(refusedReport("the source does not parse, so it was not saved", report))
	}
	author := callerAuthor(ctx)
	if err := h.store.Create(ctx, sc, author); err != nil {
		slog.Error("failed to create script", fieldName, input.Name, logKeyError, err)
		return errorResult("failed to create script"), nil, nil
	}
	// A personal script its author owns is approved here, on the same terms an
	// edit to one is (#1367): the grant is minted from what the source reaches,
	// and a grant that cannot be read off the source leaves the script waiting
	// for a reviewer exactly as it did before.
	auto := h.auto.AutoApprove(ctx, sc, sc.Version, author)
	out := map[string]any{
		fieldStatus: "created", "id": sc.ID, fieldName: sc.Name, fieldVersion: sc.Version,
		"executable": sc.Executable(),
		"next":       createdNext(auto),
	}
	addDescriptionNotice(out, sc)
	return jsonResult(out)
}

// createdNext tells the author what happens to the script they just created:
// that it already runs, or what to do about the fact that it does not.
func createdNext(auto script.AutoOutcome) string {
	switch {
	case auto.Approved:
		return "This is your own script, so the platform approved it for you: run_script executes it now, " +
			"under the access you hold, and a schedule you set will fire it."
	case auto.Reason != "":
		return "It was not approved automatically: " + auto.Reason +
			". Call run_draft to execute it under your own identity meanwhile."
	default:
		return "Call run_draft to execute it under your own identity; nothing runs it on its own until a version is approved."
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
	if errResult := h.applyUpdates(ctx, existing, input); errResult != nil {
		return errResult, nil, nil
	}
	return h.persist(ctx, &before, existing, nil)
}

// applyUpdates mutates the script in place from the sent fields, then validates
// the resulting record as a whole.
func (h *Handle) applyUpdates(ctx context.Context, sc *script.Script, input manageScriptInput) *mcp.CallToolResult {
	if input.Source != "" {
		if report := scriptrun.Validate(input.Source); !report.OK {
			result, _, _ := jsonResult(refusedReport("the source does not parse, so the edit was not saved", report))
			return result
		}
		sc.Source = input.Source
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
	if errResult := h.applyScopeAndStatus(ctx, sc, input); errResult != nil {
		return errResult
	}
	if err := sc.Validate(); err != nil {
		return errorResult(err.Error())
	}
	return nil
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
	if input.Personas != nil {
		sc.Personas = input.Personas
	}
}

// applyScopeAndStatus applies the two changes that need an authority check or a
// lifecycle rule.
func (h *Handle) applyScopeAndStatus(ctx context.Context, sc *script.Script, input manageScriptInput) *mcp.CallToolResult {
	if input.Scope != "" && input.Scope != sc.Scope {
		if err := script.ValidateScope(input.Scope); err != nil {
			return errorResult(err.Error())
		}
		if !h.isAdminPersona(ctx) {
			return errorResult("only admins can change a script's scope")
		}
		sc.Scope = input.Scope
	}
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
	outcome, err := script.ApplyEdit(ctx, h.store, script.Edit{
		Before: before, After: after, Author: callerAuthor(ctx), Auto: h.auto,
	})
	if err != nil {
		return editError(err), nil, nil
	}
	out := map[string]any{fieldName: after.Name, fieldVersion: after.Version}
	maps.Copy(out, extra)
	if outcome.PendingVersion > 0 {
		out[fieldStatus] = "pending_approval"
		out["pending_version"] = outcome.PendingVersion
		out["message"] = "This script has an approved version, so the change was saved as a draft awaiting review. The approved version keeps running until the draft is approved."
		return jsonResult(out)
	}
	out[fieldStatus] = "updated"
	out["executable"] = after.Executable()
	addDescriptionNotice(out, after)
	if msg := updatedMessage(after, outcome.Auto); msg != "" {
		out["message"] = msg
	}
	return jsonResult(out)
}

// updatedMessage states what an applied edit means for whether anything will run
// the script, and says nothing when automatic approval had nothing to say — a
// shared script's edit is the ordinary review path and needs no sentence about
// a mechanism that does not apply to it.
func updatedMessage(sc *script.Script, auto script.AutoOutcome) string {
	switch {
	case auto.Approved:
		return "This is your own script, so the platform approved this version for you. " + runsNow(sc)
	case auto.Reason != "":
		return "It was not approved automatically: " + auto.Reason +
			". Call run_draft to execute it under your own identity meanwhile."
	default:
		return ""
	}
}

// runsNow reports what an approval actually buys this script, which is not
// always a run: the execution gate refuses a disabled or deprecated script
// whatever is approved on it.
func runsNow(sc *script.Script) string {
	switch {
	case !sc.Enabled:
		return "It is disabled, so nothing executes it until it is enabled again."
	case sc.Status == script.StatusDeprecated:
		return "It is deprecated, so nothing executes it."
	default:
		return "It executes now, under the access you hold, and on any schedule you set."
	}
}

// editError maps an edit failure to a caller-facing message. The two the caller
// can act on are named; anything else is an internal failure whose detail stays
// in the log.
func editError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, script.ErrReviewRequiredMixedEdit):
		return errorResult(script.ErrReviewRequiredMixedEdit.Error())
	case errors.Is(err, script.ErrVersionConflict):
		return errorResult(err.Error())
	default:
		slog.Error("failed to update script", logKeyError, err)
		return errorResult("failed to update script")
	}
}

// handleDelete removes a script and its version history.
func (h *Handle) handleDelete(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	existing, errResult := h.editable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	// A script with an approved version may be executing on a schedule for
	// somebody, so deleting it is refused in favor of deprecating it — except
	// where the caller IS that somebody. A personal script its owner deletes
	// takes its schedule and its history with it, and nobody else could see it,
	// run it, or notice it go; refusing would leave an owner unable to delete a
	// script the platform approved for them on save (#1367).
	if existing.Executable() && !existing.OwnedPersonally(resolveEmail(ctx)) {
		return errorResult("this script has an approved version and may be executing on a schedule; deprecate it with update status=deprecated instead of deleting it"), nil, nil
	}
	if err := h.store.Delete(ctx, existing.ID); err != nil {
		slog.Error("failed to delete script", fieldName, existing.Name, logKeyError, err)
		return errorResult("failed to delete script"), nil, nil
	}
	return jsonResult(map[string]any{fieldStatus: "deleted", fieldName: existing.Name})
}

// handleGet returns one script with its full source and parameter contract. A
// built-in example resolves here too, so an author reads a worked script with
// the same command they read their own with.
func (h *Handle) handleGet(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		// A built-in example answers only when no stored script does, so a real
		// script named after an example is never shadowed by it.
		if ex, ok := builtinExample(input.Name); ok {
			return jsonResult(ex.fields())
		}
		return errResult, nil, nil
	}
	return jsonResult(scriptFields(sc))
}

// scriptFields renders one script for a get response.
func scriptFields(sc *script.Script) map[string]any {
	return map[string]any{
		"id": sc.ID, fieldName: sc.Name, "display_name": sc.DisplayName,
		"description": sc.Description, fieldSource: sc.Source, "params": sc.Params,
		"scope": sc.Scope, "personas": sc.Personas, "owner_email": sc.OwnerEmail,
		"category": sc.Category, "tags": sc.Tags, "enabled": sc.Enabled, fieldStatus: sc.Status,
		fieldVersion: sc.Version, "executable": sc.Executable(),
		"executable_note": executableNote(sc),
		"created_at":      sc.CreatedAt, "updated_at": sc.UpdatedAt,
	}
}

// executableNote states plainly what a script's execution gate means right now,
// so an author is never left inferring why nothing is running their script.
func executableNote(sc *script.Script) string {
	if sc.Executable() {
		return "This script has an approved version and the platform may execute it."
	}
	return "This script has no approved version, so nothing executes it on its own. Use run_draft to execute it under your own identity."
}

// handleList returns the scripts the caller may see. A non-admin sees their own
// scripts and the shared ones; an admin sees everything.
func (h *Handle) handleList(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	filter := script.ListFilter{
		Scope: input.Scope, Status: input.Status, Search: input.Search, Limit: input.Limit,
		Category: derefOr(input.Category), Tags: input.Tags,
	}
	if !h.isAdminPersona(ctx) {
		// Scope the listing by the same rule the read path applies, not by
		// ownership: filtering on the owner alone would hide the shared scripts
		// the caller is entitled to see and could run.
		filter.VisibleTo, filter.VisiblePersona = resolveEmail(ctx), personaName(ctx)
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
			"scope": sc.Scope, "owner_email": sc.OwnerEmail, fieldStatus: sc.Status,
			fieldVersion: sc.Version, "executable": sc.Executable(),
			"category": sc.Category, "tags": sc.Tags,
		})
	}
	return jsonResult(map[string]any{"scripts": items, "count": len(items)})
}

// refusedReport renders a validation refusal: the reason, plus the findings an
// author needs to fix it, in one response so the fix takes one round trip.
func refusedReport(reason string, report scriptrun.Report) map[string]any {
	return map[string]any{
		fieldStatus: "invalid",
		"message":   reason,
		"findings":  report.Findings,
		"help":      fmt.Sprintf("Call %s with command=help for the dialect contract and worked examples.", ToolNameManageScript),
	}
}

// contentVerb runs a read-only content verb over a script's source: resolve the
// script, build the body fields, stamp the script's identity onto them.
func (h *Handle) contentVerb(
	ctx context.Context,
	input manageScriptInput,
	build func(body string) (map[string]any, error),
) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.readable(ctx, input)
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
