package knowledge

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// bulkUntagMaxEntities bounds how many entities one bulk_untag call processes, so
// a very common tag does not fan out unbounded. When a search fills this cap the
// response flags the result as truncated so the caller knows to re-run.
const bulkUntagMaxEntities = 500

// bulkUntagURNSample bounds how many affected URNs are echoed in a response, so a
// tag on hundreds of entities cannot push the tool response past the output token
// limit. The full list is still recorded in the changeset for audit.
const bulkUntagURNSample = 50

// bulkUntagEntityTypes is the set of entity types bulk_untag enumerates when
// searching for entities carrying a tag. Tags most commonly sit on datasets, but
// they can also be applied to these other indexed types.
var bulkUntagEntityTypes = []string{
	"DATASET", "DASHBOARD", "CHART", "DATA_FLOW", "DATA_JOB", "CONTAINER", "GLOSSARY_TERM",
}

// handleBulkUntag removes a tag from every entity a catalog search finds carrying
// it, recording one changeset that lists the affected entities (#726). The
// changeset is for audit and is not auto-revertible (re-add the tag with add_tag
// if needed). Requires a semantic search provider to enumerate the entities, and
// requires explicit confirmation because it is destructive across many entities.
func (t *Toolkit) handleBulkUntag(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.TagURN == "" {
		return errorResult("tag_urn is required for bulk_untag"), nil, nil
	}
	tagURN := normalizeTagURN(input.TagURN)
	if t.semanticProvider == nil {
		return errorResult("bulk_untag requires a semantic search provider to enumerate entities; none is configured"), nil, nil
	}

	results, err := t.semanticProvider.SearchTables(ctx, semantic.SearchFilter{
		Query:       "*", // match-all; the tag filter does the selection (mirrors browseCatalog)
		Tags:        []string{tagURN},
		EntityTypes: bulkUntagEntityTypes,
		Limit:       bulkUntagMaxEntities,
	})
	if err != nil {
		return errorResult("bulk_untag: enumerating entities for tag failed: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if len(results) == 0 {
		return jsonResult(map[string]any{
			"tag_urn":           tagURN,
			"entities_untagged": 0,
			fieldMessage:        "No entities carry this tag within the searchable catalog.",
		})
	}

	urns := make([]string, 0, len(results))
	for _, r := range results {
		urns = append(urns, r.URN)
	}
	// A full page means the catalog likely holds more entities than one call can
	// process; the caller must re-run bulk_untag to clear the remainder.
	truncated := len(urns) >= bulkUntagMaxEntities

	// Destructive across many entities: require explicit confirmation, showing the
	// count and a bounded sample first.
	if t.requireConfirmation && !input.Confirm {
		return jsonResult(withTruncation(map[string]any{
			"confirmation_required": true,
			"tag_urn":               tagURN,
			"entities_found":        len(urns),
			"affected_urns_sample":  sampleURNs(urns),
			fieldMessage: fmt.Sprintf("bulk_untag will remove %s from %d entities. Set confirm: true to proceed.",
				tagURN, len(urns)),
		}, truncated, len(urns)))
	}

	affected := t.removeTagFromEntities(ctx, urns, tagURN)
	if len(affected) == 0 {
		return errorResult("bulk_untag: found entities but failed to remove the tag from any of them"), nil, nil
	}

	csID, err := t.recordBulkUntagChangeset(ctx, tagURN, authorFromContext(ctx), affected)
	if err != nil {
		// The untag already happened but the audit record did not persist; surface
		// this as an error so the operator investigates rather than trusting a
		// changeset_id that does not resolve.
		slog.Error("knowledge: bulk_untag failed to record changeset", "tag_urn", tagURN, "error", err)
		return errorResult(fmt.Sprintf(
			"bulk_untag removed %s from %d entities but FAILED to record the audit changeset: %v; "+
				"the operation is not in the audit log and has no rollback handle.",
			tagURN, len(affected), err)), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(withTruncation(map[string]any{
		"changeset_id":         csID,
		"tag_urn":              tagURN,
		"entities_untagged":    len(affected),
		"affected_urns_sample": sampleURNs(affected),
		fieldMessage: fmt.Sprintf("Removed %s from %d entities. Recorded for audit but not auto-revertible; "+
			"re-apply add_tag to restore. Coverage is the searchable catalog (datasets and other indexed types).",
			tagURN, len(affected)),
	}, truncated, len(affected)))
}

// removeTagFromEntities removes tagURN from each entity, skipping (with a warning)
// any that fail, and returns the URNs successfully untagged.
func (t *Toolkit) removeTagFromEntities(ctx context.Context, urns []string, tagURN string) []string {
	var affected []string
	for _, u := range urns {
		if err := t.datahubWriter.ApplyTagChanges(ctx, u, nil, []string{tagURN}); err != nil {
			slog.Warn("knowledge: bulk_untag failed to remove tag from entity",
				"entity_urn", u, "tag_urn", tagURN, "error", err)
			continue
		}
		affected = append(affected, u)
	}
	return affected
}

// recordBulkUntagChangeset records the untag as one changeset. The untag is stored
// as a change_0 entry (change_type bulk_untag) so the rollback path recognizes it
// and refuses to revert it (bulk_untag is not in revertibleChangeTypes); the full
// affected list is kept alongside for audit.
func (t *Toolkit) recordBulkUntagChangeset(ctx context.Context, tagURN, appliedBy string, affected []string) (string, error) {
	csID, err := generateID()
	if err != nil {
		return "", err
	}
	newValue := changesToMap([]ApplyChange{{ChangeType: string(actionBulkUntag), Target: tagURN}})
	newValue["tag_urn"] = tagURN
	newValue["affected_urns"] = affected
	cs := Changeset{
		ID:         csID,
		TargetURN:  tagURN,
		ChangeType: actionBulkUntag,
		NewValue:   newValue,
		AppliedBy:  appliedBy,
	}
	if err := t.changesetStore.InsertChangeset(ctx, cs); err != nil {
		return "", fmt.Errorf("inserting changeset: %w", err)
	}
	return csID, nil
}

// sampleURNs returns at most bulkUntagURNSample URNs so a response never echoes an
// unbounded list.
func sampleURNs(urns []string) []string {
	if len(urns) <= bulkUntagURNSample {
		return urns
	}
	return urns[:bulkUntagURNSample]
}

// withTruncation annotates a bulk_untag response when the entity set was capped at
// bulkUntagMaxEntities, so the caller knows the coverage was partial and to re-run.
func withTruncation(resp map[string]any, truncated bool, count int) map[string]any {
	if truncated {
		resp["truncated"] = true
		resp[fieldMessage] = fmt.Sprintf("%v Only the first %d entities were processed (the catalog holds more); "+
			"re-run bulk_untag to clear the remainder.", resp[fieldMessage], count)
	}
	return resp
}
