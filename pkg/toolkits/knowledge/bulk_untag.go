package knowledge

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// bulkUntagMaxEntities bounds how many entities one bulk_untag call processes, so
// a very common tag does not fan out unbounded. It is the size requested; the
// catalog may return fewer under its own page limit, and either way the response
// flags the result as truncated unless the catalog's match count proves the run
// reached every entity carrying the tag, so the caller re-runs.
const bulkUntagMaxEntities = 500

// bulkUntagConcurrency bounds how many tag removals run in parallel: the per-entity
// writes are independent, so they fan out concurrently rather than serially.
const bulkUntagConcurrency = 8

// bulkUntagURNSample bounds how many affected URNs are echoed in a response, so a
// tag on hundreds of entities cannot push the tool response past the output token
// limit. The full list is still recorded in the changeset for audit.
const bulkUntagURNSample = 50

// fieldTagURN is the response/changeset field name carrying the target tag URN.
const fieldTagURN = "tag_urn"

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
		return toolkit.ErrorResult("tag_urn is required for bulk_untag"), nil, nil
	}
	tagURN := normalizeTagURN(input.TagURN)
	if t.semanticProvider == nil {
		return toolkit.ErrorResult("bulk_untag requires a semantic search provider to enumerate entities; none is configured"), nil, nil
	}

	urns, unfinished, err := t.enumerateTaggedEntities(ctx, tagURN)
	if err != nil {
		return toolkit.ErrorResult("bulk_untag: enumerating entities for tag failed: " + err.Error()), nil, nil
	}
	if len(urns) == 0 {
		return toolkit.JSONResultTyped(map[string]any{
			fieldTagURN:         tagURN,
			"entities_untagged": 0,
			fieldMessage:        "No entities carry this tag within the searchable catalog.",
		})
	}

	// Destructive across many entities: require explicit confirmation, showing the
	// count and a bounded sample first.
	if t.requireConfirmation && !input.Confirm {
		return bulkUntagConfirmation(tagURN, urns, unfinished)
	}

	affected, failed := t.removeTagFromEntities(ctx, urns, tagURN)
	if len(affected) == 0 {
		return toolkit.ErrorResult("bulk_untag: found entities but failed to remove the tag from any of them"), nil, nil
	}

	csID, err := t.recordBulkUntagChangeset(ctx, tagURN, authorFromContext(ctx), affected)
	if err != nil {
		// The untag already happened but the audit record did not persist; surface
		// this as an error so the operator investigates rather than trusting a
		// changeset_id that does not resolve.
		slog.Error("knowledge: bulk_untag failed to record changeset", "tag_urn", tagURN, "error", err)
		return toolkit.ErrorResult(fmt.Sprintf(
			"bulk_untag removed %s from %d entities but FAILED to record the audit changeset: %v; "+
				"the operation is not in the audit log and has no rollback handle.",
			tagURN, len(affected), err)), nil, nil
	}

	return bulkUntagSuccess(bulkUntagOutcome{
		csID:       csID,
		tagURN:     tagURN,
		affected:   affected,
		failed:     failed,
		attempted:  len(urns),
		unfinished: unfinished,
	})
}

// enumerateTaggedEntities searches for entities carrying tagURN and reports
// whether this run is known to reach every one of them.
//
// The signal is the catalog's own match count, not the size of the page it
// returned. Asking for one row over the cap and treating a full page as "more
// exist" does not work here: the catalog client clamps every search to its own
// maximum (100, well under the cap), so the extra row never arrives and every
// oversubscribed tag reported itself as fully cleared (#1238).
//
// A catalog that cannot count leaves the run unverified, and unverified counts as
// unfinished: this removes a tag from entities, and sending the caller back for a
// second run that finds nothing costs one search, while calling an unverified
// sweep complete leaves entities tagged and no one looking for them.
func (t *Toolkit) enumerateTaggedEntities(ctx context.Context, tagURN string) (urns []string, unfinished bool, err error) {
	results, total, err := semantic.SearchTablesCounted(ctx, t.semanticProvider, semantic.SearchFilter{
		Query:       "*", // match-all; the tag filter does the selection (mirrors browseCatalog)
		Tags:        []string{tagURN},
		EntityTypes: bulkUntagEntityTypes,
		Limit:       bulkUntagMaxEntities,
	})
	if err != nil {
		return nil, false, err //nolint:wrapcheck // caller prefixes the message
	}
	urns = make([]string, 0, len(results))
	for _, r := range results {
		urns = append(urns, r.URN)
	}
	// The cap bounds a fan-out of writes, so it is enforced here rather than
	// trusted to the requested limit: this whole path exists because a catalog
	// does not have to return the page size it was asked for.
	if len(urns) > bulkUntagMaxEntities {
		urns = urns[:bulkUntagMaxEntities]
	}
	return urns, total == semantic.TotalUnknown || total > len(urns), nil
}

// removeTagFromEntities removes tagURN from each entity concurrently (bounded), and
// returns the URNs successfully untagged (in input order) and the count that failed.
// A per-entity failure is logged and counted, not fatal, so the batch clears as many
// entities as it can.
func (t *Toolkit) removeTagFromEntities(ctx context.Context, urns []string, tagURN string) (affected []string, failed int) {
	ok := make([]bool, len(urns))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(bulkUntagConcurrency)
	for i, u := range urns {
		g.Go(func() error {
			if err := t.datahubWriter.ApplyTagChanges(gctx, u, nil, []string{tagURN}); err != nil {
				slog.Warn("knowledge: bulk_untag failed to remove tag from entity",
					"entity_urn", u, "tag_urn", tagURN, "error", err)
				return nil // do not cancel the batch on one entity's failure
			}
			ok[i] = true
			return nil
		})
	}
	_ = g.Wait() // goroutines never return an error, so Wait cannot fail
	for i, done := range ok {
		if done {
			affected = append(affected, urns[i])
			continue
		}
		failed++
	}
	return affected, failed
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
	newValue[fieldTagURN] = tagURN
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

// bulkUntagConfirmation builds the confirmation-required preview (nothing has been
// removed yet), showing the count, a bounded sample, and, when the run is not
// known to reach every carrier, a note that a follow-up run will be needed.
func bulkUntagConfirmation(tagURN string, urns []string, unfinished bool) (*mcp.CallToolResult, any, error) {
	msg := fmt.Sprintf("bulk_untag will remove %s from %d entities. Set confirm: true to proceed.", tagURN, len(urns))
	if unfinished {
		msg += fmt.Sprintf(" The %d entities this run would process are not known to be every entity carrying the tag, so a follow-up run will be needed after this one.",
			len(urns))
	}
	resp := map[string]any{
		"confirmation_required": true,
		fieldTagURN:             tagURN,
		"entities_found":        len(urns),
		"affected_urns_sample":  sampleURNs(urns),
		fieldMessage:            msg,
	}
	if unfinished {
		resp["truncated"] = true
	}
	return toolkit.JSONResultTyped(resp)
}

// bulkUntagOutcome carries the result of a bulk_untag run into the success response.
type bulkUntagOutcome struct {
	csID      string
	tagURN    string
	affected  []string
	failed    int
	attempted int
	// unfinished reports that the run is not known to have reached every entity
	// carrying the tag; it becomes the response's "truncated" field.
	unfinished bool
}

// bulkUntagSuccess builds the success response, reporting how many entities were
// untagged, how many failed (and still carry the tag), and whether the run is
// known to have reached every carrier (so the caller re-runs for the remainder).
func bulkUntagSuccess(o bulkUntagOutcome) (*mcp.CallToolResult, any, error) {
	msg := fmt.Sprintf("Removed %s from %d entities. Recorded for audit but not auto-revertible; "+
		"re-apply add_tag to restore. Coverage is the searchable catalog (datasets and other indexed types).",
		o.tagURN, len(o.affected))
	resp := map[string]any{
		"changeset_id":         o.csID,
		fieldTagURN:            o.tagURN,
		"entities_untagged":    len(o.affected),
		"affected_urns_sample": sampleURNs(o.affected),
		// bulk_untag records a changeset for audit but is not auto-revertible (the
		// untagged set has no captured before-image); carry the same structured field
		// the other apply responses expose so the contract is uniform (#922).
		"revertible":                false,
		"unrevertible_change_types": []string{string(actionBulkUntag)},
	}
	if o.failed > 0 {
		resp["failed"] = o.failed
		msg += fmt.Sprintf(" %d entities could not be updated and still carry the tag; re-run bulk_untag to retry them.", o.failed)
	}
	if o.unfinished {
		resp["truncated"] = true
		msg += fmt.Sprintf(" Only the %d entities this run processed are accounted for; re-run bulk_untag to clear any remainder.", o.attempted)
	}
	resp[fieldMessage] = msg
	return toolkit.JSONResultTyped(resp)
}

// sampleURNs returns at most bulkUntagURNSample URNs so a response never echoes an
// unbounded list.
func sampleURNs(urns []string) []string {
	if len(urns) <= bulkUntagURNSample {
		return urns
	}
	return urns[:bulkUntagURNSample]
}
