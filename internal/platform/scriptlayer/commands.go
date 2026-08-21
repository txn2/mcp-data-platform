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
	author := callerAuthor(ctx)
	if err := h.store.Create(ctx, sc, author); err != nil {
		slog.Error("failed to create script", fieldName, input.Name, logKeyError, err)
		return errorResult("failed to create script"), nil, nil
	}
	out := map[string]any{
		fieldStatus: "created", "id": sc.ID, fieldName: sc.Name, fieldVersion: sc.Version,
		"next": "Saved, and it runs: run_script executes it under the access you held when you saved it, and a schedule you set will fire it. Use run_draft to iterate on changes before saving them.",
	}
	addDescriptionNotice(out, sc)
	return jsonResult(out)
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
	if errResult := h.applyStatus(ctx, sc, input); errResult != nil {
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
	out := map[string]any{fieldName: after.Name, fieldVersion: after.Version}
	maps.Copy(out, extra)
	out[fieldStatus] = "updated"
	addDescriptionNotice(out, after)
	out["message"] = runsNow(after)
	return jsonResult(out)
}

// runsNow states what the saved script's state means for whether anything will
// run it: the run gate refuses a disabled or deprecated script whatever was
// just saved.
func runsNow(sc *script.Script) string {
	switch {
	case !sc.Enabled:
		return "Saved. It is disabled, so nothing executes it until it is enabled again."
	case sc.Status == script.StatusDeprecated:
		return "Saved. It is deprecated, so nothing executes it."
	case sc.Status == script.StatusSuperseded:
		return "Saved. It was superseded, so nothing executes it."
	default:
		return "Saved, and this version is what runs now: run_script executes it and any schedule fires it."
	}
}

// editError maps an edit failure to a caller-facing message. The one the
// caller can act on is named; anything else is an internal failure whose
// detail stays in the log.
func editError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, script.ErrVersionConflict):
		return errorResult(err.Error())
	default:
		slog.Error("failed to update script", logKeyError, err)
		return errorResult("failed to update script")
	}
}

// handleDelete removes a script and its version history. A script is one
// person's, so a delete takes its schedule and its history with it and nobody
// else loses anything; the caller has already been established as its owner or
// an administrator by editable.
func (h *Handle) handleDelete(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	existing, errResult := h.editable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
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
		"owner_email": sc.OwnerEmail,
		"category":    sc.Category, "tags": sc.Tags, "enabled": sc.Enabled, fieldStatus: sc.Status,
		fieldVersion:      sc.Version,
		"executable_note": script.ExecutionNote(sc),
		"created_at":      sc.CreatedAt, "updated_at": sc.UpdatedAt,
	}
}

// handleList returns the scripts the caller may see: their own, or every script
// on the platform for an admin.
func (h *Handle) handleList(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	filter := script.ListFilter{
		Status: input.Status, Search: input.Search, Limit: input.Limit,
		Category: derefOr(input.Category), Tags: input.Tags,
	}
	if !h.isAdminPersona(ctx) {
		filter.OwnerEmail = resolveEmail(ctx)
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
			"category":   sc.Category, "tags": sc.Tags,
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
