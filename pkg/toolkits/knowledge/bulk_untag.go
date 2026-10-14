package knowledge

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// bulkUntagMaxEntities bounds how many entities one bulk_untag call rewrites in
// its single transaction. A tag on more is cleared over several calls, and the
// response says so.
const bulkUntagMaxEntities = 500

// bulkUntagURNSample bounds how many affected references are echoed in a
// response, so a tag on hundreds of entities cannot push the tool response past
// the output token limit. The changeset records every catalog change.
const bulkUntagURNSample = 50

// fieldTagURN is the response/changeset field name carrying the target tag.
const fieldTagURN = "tag_urn"

// handleBulkUntag removes a tag from every dataset and column in the platform
// catalog carrying it, in one transaction, and records one changeset that rolls
// the removal back (#726, #1920). It requires explicit confirmation because it
// changes many entities.
func (t *Toolkit) handleBulkUntag(ctx context.Context, input applyKnowledgeInput) (*mcp.CallToolResult, any, error) {
	if input.TagURN == "" {
		return toolkit.ErrorResult("tag_urn is required for bulk_untag"), nil, nil
	}
	if t.curator.store == nil {
		return toolkit.ErrorResult(errNoCatalog), nil, nil
	}
	tag, err := tagName(input.TagURN)
	if err != nil {
		return toolkit.ErrorResult("bulk_untag: " + err.Error()), nil, nil
	}
	tagRef := catalog.KeyRef(catalog.KindTag, tag).String()
	refs, err := t.curator.store.TaggedEntities(ctx, tag, bulkUntagMaxEntities+1)
	if err != nil {
		return toolkit.ErrorResult("bulk_untag: listing the entities carrying the tag failed: " + err.Error()), nil, nil
	}
	if len(refs) == 0 {
		return toolkit.JSONResultTyped(map[string]any{
			fieldTagURN:         tagRef,
			"entities_untagged": 0,
			fieldMessage:        "No dataset or column in the catalog carries this tag.",
		})
	}
	if withheld := t.unreachableCount(ctx, refs); withheld > 0 {
		return toolkit.ErrorResult(fmt.Sprintf(
			"bulk_untag: %s is also on %d datasets or columns on connections your persona does not reach, "+
				"and bulk_untag removes it everywhere; remove it from the ones you reach with remove_tag",
			tagRef, withheld)), nil, nil
	}
	unfinished := len(refs) > bulkUntagMaxEntities
	if unfinished {
		refs = refs[:bulkUntagMaxEntities]
	}
	if t.requireConfirmation && !input.Confirm {
		return bulkUntagConfirmation(tagRef, refs, unfinished)
	}

	affected, written, err := t.curator.untag(ctx, tag, bulkUntagMaxEntities, authorFromContext(ctx))
	if err != nil {
		return toolkit.ErrorResult("bulk_untag: " + err.Error()), nil, nil
	}
	csID, err := t.recordBulkUntagChangeset(ctx, tagRef, authorFromContext(ctx), affected, written)
	if err != nil {
		slog.ErrorContext(ctx, "knowledge: bulk_untag failed to record changeset",
			"tag", logsan.SanitizeForLog(tagRef), "error", logsan.SanitizeForLog(err.Error()))
		return toolkit.ErrorResult(fmt.Sprintf(
			"bulk_untag removed %s from %d entities but FAILED to record the changeset: %v; "+
				"the removal has no rollback handle. Restore the tag with add_tag where needed.",
			tagRef, len(affected), err)), nil, nil
	}
	return bulkUntagSuccess(csID, tagRef, affected, unfinished)
}

// recordBulkUntagChangeset records the untag as one changeset holding every
// catalog change it wrote, so a rollback restores the tag everywhere at once.
func (t *Toolkit) recordBulkUntagChangeset(
	ctx context.Context, tagRef, appliedBy string, affected []string, written curationResult,
) (string, error) {
	csID, err := generateID()
	if err != nil {
		return "", err
	}
	newValue := changesToMap([]ApplyChange{{ChangeType: string(actionBulkUntag), Target: tagRef}})
	newValue[fieldTagURN] = tagRef
	newValue["affected_urns"] = affected
	newValue[fieldCatalogChanges] = written.changeIDs()
	cs := Changeset{
		ID:         csID,
		TargetURN:  tagRef,
		ChangeType: actionBulkUntag,
		NewValue:   newValue,
		AppliedBy:  appliedBy,
	}
	if err := t.changesetStore.InsertChangeset(ctx, cs); err != nil {
		return "", fmt.Errorf("inserting changeset: %w", err)
	}
	return csID, nil
}

// bulkUntagConfirmation builds the confirmation-required preview (nothing has
// been removed yet), showing the count, a bounded sample, and, when more
// entities carry the tag than one run rewrites, that a follow-up run is needed.
func bulkUntagConfirmation(tagRef string, refs []string, unfinished bool) (*mcp.CallToolResult, any, error) {
	msg := fmt.Sprintf("bulk_untag will remove %s from %d entities. Set confirm: true to proceed.", tagRef, len(refs))
	if unfinished {
		msg += fmt.Sprintf(" More than %d entities carry the tag, so a follow-up run will be needed after this one.",
			bulkUntagMaxEntities)
	}
	resp := map[string]any{
		"confirmation_required": true,
		fieldTagURN:             tagRef,
		"entities_found":        len(refs),
		"affected_urns_sample":  sampleURNs(refs),
		fieldMessage:            msg,
	}
	if unfinished {
		resp["truncated"] = true
	}
	return toolkit.JSONResultTyped(resp)
}

// bulkUntagSuccess builds the success response: how many entities were
// untagged, the changeset that restores them, and whether more remain.
func bulkUntagSuccess(csID, tagRef string, affected []string, unfinished bool) (*mcp.CallToolResult, any, error) {
	msg := fmt.Sprintf("Removed %s from %d entities. Roll back with action=rollback changeset_id=%s.",
		tagRef, len(affected), csID)
	resp := map[string]any{
		"changeset_id":         csID,
		fieldTagURN:            tagRef,
		"entities_untagged":    len(affected),
		"affected_urns_sample": sampleURNs(affected),
		"revertible":           true,
	}
	if unfinished {
		resp["truncated"] = true
		msg += fmt.Sprintf(" More than %d entities carried the tag; re-run bulk_untag to clear the rest.", bulkUntagMaxEntities)
	}
	resp[fieldMessage] = msg
	return toolkit.JSONResultTyped(resp)
}

// sampleURNs returns at most bulkUntagURNSample references so a response never
// echoes an unbounded list.
func sampleURNs(refs []string) []string {
	if len(refs) <= bulkUntagURNSample {
		return refs
	}
	return refs[:bulkUntagURNSample]
}
