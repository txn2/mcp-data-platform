package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// changesetSummary is the discovery-surface view of a changeset, omitting the
// verbose before/after value maps. Returned by the list_changesets action.
type changesetSummary struct {
	ChangesetID      string     `json:"changeset_id"`
	CreatedAt        time.Time  `json:"created_at"`
	TargetURN        string     `json:"target_urn"`
	ChangeType       string     `json:"change_type"`
	AppliedBy        string     `json:"applied_by,omitempty"`
	SourceInsightIDs []string   `json:"source_insight_ids,omitempty"`
	RolledBack       bool       `json:"rolled_back"`
	RolledBackBy     string     `json:"rolled_back_by,omitempty"`
	RolledBackAt     *time.Time `json:"rolled_back_at,omitempty"`
	// Revertible reports whether this changeset can be rolled back automatically,
	// computed with rollback's own all-or-nothing gate (#922). It folds in the
	// already-rolled-back state so a caller filtering on this field alone never picks a
	// changeset the rollback would refuse. It does NOT model the newer-changeset
	// conflict, a dynamic condition surfaced only at rollback time; when unrevertible
	// for a structural reason, UnrevertibleChangeTypes names the blocking change types.
	Revertible              bool     `json:"revertible"`
	UnrevertibleChangeTypes []string `json:"unrevertible_change_types,omitempty"`
}

// handleRollback reverts a previously applied changeset, restoring the mutated
// aspects to their pre-change state.
func (t *Toolkit) handleRollback(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.ChangesetID == "" {
		return toolkit.ErrorResult("changeset_id is required for rollback action"), nil, nil
	}
	if t.requireConfirmation && !input.Confirm {
		return toolkit.JSONResultTyped(map[string]any{
			"confirmation_required": true,
			"changeset_id":          input.ChangesetID,
			fieldMessage:            "Set confirm: true to roll back this changeset.",
		})
	}

	cs, err := t.changesetStore.GetChangeset(ctx, input.ChangesetID)
	if err != nil {
		return toolkit.ErrorResult("changeset not found: " + input.ChangesetID), nil, nil
	}
	if input.EntityURN != "" && cs.TargetURN != input.EntityURN {
		return toolkit.ErrorResult("changeset " + input.ChangesetID + " does not belong to entity " + input.EntityURN), nil, nil
	}

	deps := RollbackDeps{
		Writer: t.datahubWriter, Changesets: t.changesetStore, Insights: t.store,
		Pages: t.pageWriter, Instructions: t.instructions,
	}
	result, err := RevertChangeset(ctx, deps, cs, authorFromContext(ctx))
	if err != nil {
		return rollbackErrorResult(err), nil, nil
	}
	return toolkit.JSONResultTyped(result)
}

// rollbackErrorResult maps rollback failures to user-facing tool errors with the
// most actionable message for each failure mode.
func rollbackErrorResult(err error) *mcp.CallToolResult {
	var unrevertible *UnrevertibleError
	var conflict *RollbackConflictError
	var pageEdited *PageEditedError
	var instructionsEdited *InstructionsEditedError
	switch {
	case errors.Is(err, ErrChangesetAlreadyRolledBack):
		return toolkit.ErrorResult("changeset has already been rolled back")
	case errors.As(err, &unrevertible):
		return toolkit.ErrorResult(unrevertible.Error())
	case errors.As(err, &conflict):
		return toolkit.ErrorResult(conflict.Error())
	case errors.As(err, &pageEdited):
		return toolkit.ErrorResult(pageEdited.Error())
	case errors.As(err, &instructionsEdited):
		return toolkit.ErrorResult(instructionsEdited.Error())
	default:
		return toolkit.ErrorResult("rollback failed: " + err.Error())
	}
}

// handleListChangesets returns the changesets for an entity so an agent can
// discover rollback targets without already holding their ids.
func (t *Toolkit) handleListChangesets(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.EntityURN == "" {
		return toolkit.ErrorResult("entity_urn is required for list_changesets action"), nil, nil
	}

	changesets, total, err := t.changesetStore.ListChangesets(ctx, ChangesetFilter{
		EntityURN: input.EntityURN,
		Limit:     MaxLimit,
	})
	if err != nil {
		return toolkit.ErrorResult("failed to list changesets: " + err.Error()), nil, nil
	}

	summaries := make([]changesetSummary, 0, len(changesets))
	for i := range changesets {
		summaries = append(summaries, toChangesetSummary(&changesets[i]))
	}

	return toolkit.JSONResultTyped(map[string]any{
		fieldEntityURN: input.EntityURN,
		"total":        total,
		"changesets":   summaries,
	})
}

// toChangesetSummary projects a changeset onto the discovery view.
func toChangesetSummary(cs *Changeset) changesetSummary {
	revertible, blockingTypes := changesetRevertibility(cs.TargetURN, parseRecordedChanges(cs.NewValue))
	// An already-rolled-back changeset cannot be rolled back again, so report it as not
	// revertible regardless of its structural revertibility (#922 review).
	if cs.RolledBack {
		revertible = false
	}
	return changesetSummary{
		ChangesetID:             cs.ID,
		CreatedAt:               cs.CreatedAt,
		TargetURN:               cs.TargetURN,
		ChangeType:              cs.ChangeType,
		AppliedBy:               cs.AppliedBy,
		SourceInsightIDs:        cs.SourceInsightIDs,
		RolledBack:              cs.RolledBack,
		RolledBackBy:            cs.RolledBackBy,
		RolledBackAt:            cs.RolledBackAt,
		Revertible:              revertible,
		UnrevertibleChangeTypes: blockingTypes,
	}
}
