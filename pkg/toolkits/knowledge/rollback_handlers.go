package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
}

// handleRollback reverts a previously applied changeset, restoring the mutated
// aspects to their pre-change state.
func (t *Toolkit) handleRollback(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.ChangesetID == "" {
		return errorResult("changeset_id is required for rollback action"), nil, nil
	}
	if t.requireConfirmation && !input.Confirm {
		return jsonResult(map[string]any{
			"confirmation_required": true,
			"changeset_id":          input.ChangesetID,
			fieldMessage:            "Set confirm: true to roll back this changeset.",
		})
	}

	cs, err := t.changesetStore.GetChangeset(ctx, input.ChangesetID)
	if err != nil {
		return errorResult("changeset not found: " + input.ChangesetID), nil, nil //nolint:nilerr // MCP protocol
	}
	if input.EntityURN != "" && cs.TargetURN != input.EntityURN {
		return errorResult("changeset " + input.ChangesetID + " does not belong to entity " + input.EntityURN), nil, nil
	}

	deps := RollbackDeps{Writer: t.datahubWriter, Changesets: t.changesetStore, Insights: t.store, Pages: t.pageWriter}
	result, err := RevertChangeset(ctx, deps, cs, authorFromContext(ctx))
	if err != nil {
		return rollbackErrorResult(err), nil, nil
	}
	return jsonResult(result)
}

// rollbackErrorResult maps rollback failures to user-facing tool errors with the
// most actionable message for each failure mode.
func rollbackErrorResult(err error) *mcp.CallToolResult {
	var unrevertible *UnrevertibleError
	var conflict *RollbackConflictError
	var pageEdited *PageEditedError
	switch {
	case errors.Is(err, ErrChangesetAlreadyRolledBack):
		return errorResult("changeset has already been rolled back")
	case errors.As(err, &unrevertible):
		return errorResult(unrevertible.Error())
	case errors.As(err, &conflict):
		return errorResult(conflict.Error())
	case errors.As(err, &pageEdited):
		return errorResult(pageEdited.Error())
	default:
		return errorResult("rollback failed: " + err.Error())
	}
}

// handleListChangesets returns the changesets for an entity so an agent can
// discover rollback targets without already holding their ids.
func (t *Toolkit) handleListChangesets(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.EntityURN == "" {
		return errorResult("entity_urn is required for list_changesets action"), nil, nil
	}

	changesets, total, err := t.changesetStore.ListChangesets(ctx, ChangesetFilter{
		EntityURN: input.EntityURN,
		Limit:     MaxLimit,
	})
	if err != nil {
		return errorResult("failed to list changesets: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	summaries := make([]changesetSummary, 0, len(changesets))
	for i := range changesets {
		summaries = append(summaries, toChangesetSummary(&changesets[i]))
	}

	return jsonResult(map[string]any{
		fieldEntityURN: input.EntityURN,
		"total":        total,
		"changesets":   summaries,
	})
}

// toChangesetSummary projects a changeset onto the discovery view.
func toChangesetSummary(cs *Changeset) changesetSummary {
	return changesetSummary{
		ChangesetID:      cs.ID,
		CreatedAt:        cs.CreatedAt,
		TargetURN:        cs.TargetURN,
		ChangeType:       cs.ChangeType,
		AppliedBy:        cs.AppliedBy,
		SourceInsightIDs: cs.SourceInsightIDs,
		RolledBack:       cs.RolledBack,
		RolledBackBy:     cs.RolledBackBy,
		RolledBackAt:     cs.RolledBackAt,
	}
}
