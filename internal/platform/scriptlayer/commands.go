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
		Source: input.Source, Params: input.Params, Scope: scope,
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
	if err := h.store.Create(ctx, sc); err != nil {
		slog.Error("failed to create script", fieldName, input.Name, logKeyError, err)
		return errorResult("failed to create script"), nil, nil
	}
	return jsonResult(map[string]any{
		fieldStatus: "created", "id": sc.ID, fieldName: sc.Name, fieldVersion: sc.Version,
		"next": "Call run_draft to execute it under your own identity; nothing runs it on its own until a version is approved.",
	})
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
	outcome, err := script.ApplyEdit(ctx, h.store, before, after, resolveEmail(ctx))
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
	return jsonResult(out)
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
	if existing.Executable() {
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
		"tags": sc.Tags, "enabled": sc.Enabled, fieldStatus: sc.Status,
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
			fieldVersion: sc.Version, "executable": sc.Executable(), "tags": sc.Tags,
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
