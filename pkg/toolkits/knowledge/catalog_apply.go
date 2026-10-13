package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// errNoCatalog is the refusal when apply_knowledge has no platform catalog to
// write to: a deployment with no database, or one that turned the semantic
// layer off.
const errNoCatalog = "no platform catalog is available on this deployment (it needs a database and a semantic " +
	`provider other than noop), so catalog changes cannot be written; re-send with sink: "knowledge_page" ` +
	"to record this knowledge as a knowledge page instead"

// handleApply routes an apply to its sink: catalog curation (the default), a
// knowledge page (#633), or the deployment's agent instructions (#1607).
func (t *Toolkit) handleApply(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	switch input.Sink {
	case "", sinkCatalog:
		return t.applyToCatalog(ctx, input)
	case sinkKnowledgePage:
		return t.promoteToPage(ctx, input)
	case sinkAgentInstructions:
		return t.promoteToInstructions(ctx, input)
	default:
		return toolkit.ErrorResult("unknown sink: " + input.Sink +
			" (valid: catalog, knowledge_page, agent_instructions)"), nil, nil
	}
}

// applyToCatalog writes an apply's changes to the platform catalog in one
// transaction and records the changeset that rolls them back (#1920).
func (t *Toolkit) applyToCatalog(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if msg := t.catalogApplyRefusal(input); msg != "" {
		return toolkit.ErrorResult(msg), nil, nil
	}
	if t.requireConfirmation && !input.Confirm {
		return toolkit.JSONResultTyped(map[string]any{
			"confirmation_required": true,
			fieldEntityURN:          input.EntityURN,
			"changes_count":         len(input.Changes),
			fieldMessage:            "Set confirm: true to apply these changes.",
		})
	}
	if !needsCatalog(input.Changes) {
		return t.applyPromptsOnly(ctx, input)
	}
	ref, err := t.curator.resolveTarget(ctx, input.EntityURN)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	appliedBy := authorFromContext(ctx)
	prev, err := t.curator.metadata(ctx, ref)
	if err != nil && !tagNotDefined(ref, err) {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	written, err := t.curator.apply(ctx, ref, input.Changes, appliedBy)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	prompts, err := t.applyPrompts(ctx, input.Changes)
	if err != nil {
		return toolkit.ErrorResult(err.Error() + " (the catalog changes were written; roll them back with the changeset below if needed)"), nil, nil
	}
	written.created = append(written.created, prompts...)
	return t.recordCatalogChangeset(ctx, input, appliedCuration{ref: ref, prev: prev, by: appliedBy, written: written})
}

// catalogApplyRefusal is why an apply cannot be written, or "".
func (t *Toolkit) catalogApplyRefusal(input applyKnowledgeInput) string {
	if input.EntityURN == "" {
		return "entity_urn is required for apply action"
	}
	if err := ValidateApplyChanges(input.Changes); err != nil {
		return err.Error()
	}
	if t.curator.store == nil && needsCatalog(input.Changes) {
		return errNoCatalog
	}
	return ""
}

// appliedCuration is what an apply wrote: the entity, its curation before,
// who wrote it and the catalog changes.
type appliedCuration struct {
	ref     catalog.Ref
	prev    *EntityMetadata
	by      string
	written curationResult
}

// tagNotDefined reports a tag the catalog holds no definition for. A tag
// exists wherever an entity carries its name, so delete_tag acts on one with
// or without a definition.
func tagNotDefined(ref catalog.Ref, err error) bool {
	return ref.Kind == catalog.KindTag && errors.Is(err, ErrTargetNotCataloged)
}

// needsCatalog reports whether any change writes catalog curation, which all
// but add_prompt do.
func needsCatalog(changes []ApplyChange) bool {
	for _, c := range changes {
		if c.ChangeType != string(actionAddPrompt) {
			return true
		}
	}
	return false
}

// applyPrompts creates the prompts an apply's add_prompt changes carry and
// returns their ids.
func (t *Toolkit) applyPrompts(ctx context.Context, changes []ApplyChange) ([]string, error) {
	var ids []string
	for i, c := range changes {
		if c.ChangeType != string(actionAddPrompt) {
			continue
		}
		id, err := t.dispatchAddPrompt(ctx, c)
		if err != nil {
			return ids, fmt.Errorf("changes[%d]: %w", i, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// applyPromptsOnly applies an apply made only of add_prompt changes, which
// touch no catalog entity, and records it for audit.
func (t *Toolkit) applyPromptsOnly(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	ids, err := t.applyPrompts(ctx, input.Changes)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	return t.recordCatalogChangeset(ctx, input, appliedCuration{by: authorFromContext(ctx), written: curationResult{created: ids}})
}

// recordCatalogChangeset records an apply's changeset, marks its source
// insights applied, and reports what the catalog now holds.
func (t *Toolkit) recordCatalogChangeset(ctx context.Context, input applyKnowledgeInput, applied appliedCuration) (*mcp.CallToolResult, any, error) {
	ref, prev, appliedBy, written := applied.ref, applied.prev, applied.by, applied.written
	csID, err := generateID()
	if err != nil {
		return toolkit.ErrorResult("internal error generating changeset ID"), nil, nil
	}
	insightIDs := input.InsightIDs
	if insightIDs == nil {
		insightIDs = []string{}
	}
	newValue := changesToMap(input.Changes)
	newValue[fieldCatalogChanges] = written.changeIDs()
	if len(written.created) > 0 {
		newValue[fieldCreatedURNs] = written.created
	}
	target := input.EntityURN
	if ref.Kind != "" {
		target = ref.String()
	}
	cs := Changeset{
		ID:               csID,
		TargetURN:        target,
		ChangeType:       summarizeChangeTypes(input.Changes),
		PreviousValue:    metadataToMap(prev),
		NewValue:         newValue,
		SourceInsightIDs: insightIDs,
		AppliedBy:        appliedBy,
	}
	if err := t.changesetStore.InsertChangeset(ctx, cs); err != nil {
		return toolkit.ErrorResult("failed to record changeset: " + err.Error()), nil, nil
	}
	for _, insID := range input.InsightIDs {
		if err := t.store.MarkApplied(ctx, insID, appliedBy, csID); err != nil {
			slog.WarnContext(ctx, "knowledge: failed to mark insight applied",
				"insight_id", insID, "changeset_id", csID, "error", err)
		}
	}
	return toolkit.JSONResultTyped(t.applyResult(ctx, cs, ref, input, written))
}

// applyResult is the apply response: the changeset, what changed and the
// target's curation as the catalog now holds it.
func (t *Toolkit) applyResult(ctx context.Context, cs Changeset, ref catalog.Ref, input applyKnowledgeInput, written curationResult) map[string]any {
	revertible := !hasPromptChange(input.Changes)
	result := map[string]any{
		"changeset_id":            cs.ID,
		fieldEntityURN:            cs.TargetURN,
		"changes_applied":         len(input.Changes),
		"catalog_changes":         len(written.changes),
		"insights_marked_applied": len(input.InsightIDs),
		"revertible":              revertible,
		fieldMessage:              applyResultMessage(cs.ID, revertible),
	}
	if !revertible {
		result["unrevertible_change_types"] = []string{string(actionAddPrompt)}
	}
	if ref.Kind != "" {
		if meta, err := t.curator.metadata(ctx, ref); err == nil {
			result["resulting_state"] = metadataToMap(meta)
		}
	}
	if len(written.created) > 0 {
		result["created_ids"] = written.created
	}
	return result
}

// hasPromptChange reports whether an apply created a prompt, which a rollback
// does not delete.
func hasPromptChange(changes []ApplyChange) bool {
	for _, c := range changes {
		if c.ChangeType == string(actionAddPrompt) {
			return true
		}
	}
	return false
}

// applyResultMessage says where the changes went and how to undo them.
func applyResultMessage(csID string, revertible bool) string {
	if revertible {
		return fmt.Sprintf("Changes written to the platform catalog. Roll back with action=rollback changeset_id=%s.", csID)
	}
	return fmt.Sprintf("Changes written. Recorded as changeset %s. It cannot be rolled back automatically: "+
		"a prompt it created is not deleted by a rollback; delete the prompt with manage_prompt if needed.", csID)
}

// catalogRollback reverts the catalog changes a changeset recorded, in one
// transaction. A changeset that recorded none was applied to DataHub before
// the catalog replaced it, and cannot be undone here.
func catalogRollback(ctx context.Context, store catalog.Store, cs *Changeset, by string) ([]string, error) {
	if _, recorded := cs.NewValue[fieldCatalogChanges]; !recorded {
		return nil, ErrDataHubChangeset
	}
	if hasRecordedPrompt(cs) {
		return nil, &UnrevertibleError{ChangeTypes: []string{string(actionAddPrompt)}}
	}
	changeIDs := strsFromMap(cs.NewValue, fieldCatalogChanges)
	if len(changeIDs) == 0 {
		return []string{}, nil
	}
	if store == nil {
		return nil, errors.New(errNoCatalog)
	}
	reverted, err := store.RevertChanges(ctx, reverse(changeIDs), by)
	if err != nil {
		return nil, rollbackError(err)
	}
	out := make([]string, len(reverted))
	for i, c := range reverted {
		out[i] = fmt.Sprintf("restored %s of %s", c.Aspect, c.Reference)
	}
	return out, nil
}

// ErrDataHubChangeset refuses the rollback of a changeset written to DataHub
// before the platform catalog replaced it: its writes are not in the catalog's
// history.
var ErrDataHubChangeset = errors.New("this changeset was applied to DataHub before the platform catalog replaced it, " +
	"so it has no catalog history to roll back; restore the desired state with a new apply")

// hasRecordedPrompt reports whether a recorded changeset created a prompt.
func hasRecordedPrompt(cs *Changeset) bool {
	for _, c := range parseRecordedChanges(cs.NewValue) {
		if c.ChangeType == string(actionAddPrompt) {
			return true
		}
	}
	return false
}

// rollbackError reads a store refusal into the rollback's own terms.
func rollbackError(err error) error {
	var conflict *catalog.ConflictError
	if errors.As(err, &conflict) {
		return &RollbackConflictError{Conflict: conflict}
	}
	return fmt.Errorf("rolling back catalog changes: %w", err)
}

// changesetRevertibility reports whether a changeset can be rolled back and,
// when it cannot, the change types that block it. A page or instructions
// promotion keeps the prior state it restores; a catalog changeset is
// revertible unless it created a prompt; one applied to DataHub before the
// catalog is not. Whether an aspect was written since is decided at rollback
// time, not here.
func changesetRevertibility(cs *Changeset) (revertible bool, blockingTypes []string) {
	if strings.HasPrefix(cs.TargetURN, pageTargetPrefix) || strings.HasPrefix(cs.TargetURN, instructionsTargetPrefix) {
		return true, nil
	}
	if hasRecordedPrompt(cs) {
		return false, []string{string(actionAddPrompt)}
	}
	if _, recorded := cs.NewValue[fieldCatalogChanges]; !recorded {
		return false, uniqueChangeTypes(cs)
	}
	return true, nil
}

// uniqueChangeTypes lists a changeset's change types once each, in order.
func uniqueChangeTypes(cs *Changeset) []string {
	var out []string
	for _, c := range parseRecordedChanges(cs.NewValue) {
		if !slices.Contains(out, c.ChangeType) {
			out = append(out, c.ChangeType)
		}
	}
	return out
}

// reverse returns s in reverse order, so the last change written is undone
// first.
func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
