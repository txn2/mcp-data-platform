package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// ErrChangesetAlreadyRolledBack is returned when a rollback targets a changeset
// that has already been rolled back.
var ErrChangesetAlreadyRolledBack = errors.New("changeset already rolled back")

// UnrevertibleError is returned when a changeset contains change types whose
// pre-change state is not captured in the changeset's before-image, so they
// cannot be reverted automatically. The recovery path for these is a manual
// re-apply of the desired state.
type UnrevertibleError struct {
	ChangeTypes []string
}

// Error implements the error interface.
func (e *UnrevertibleError) Error() string {
	return fmt.Sprintf("changeset cannot be rolled back automatically: change types %s "+
		"do not record enough prior state to revert; restore the desired state with a new apply",
		strings.Join(e.ChangeTypes, ", "))
}

// RollbackConflictError is returned when a newer, not-yet-rolled-back changeset
// has since mutated the same metadata aspect. Reverting would silently clobber
// that newer change, so the rollback is refused.
type RollbackConflictError struct {
	ConflictingIDs []string
	Aspects        []string
}

// Error implements the error interface.
func (e *RollbackConflictError) Error() string {
	return fmt.Sprintf("rollback blocked: newer changeset(s) %s modified the same aspect(s) %s; "+
		"roll those back first or restore the desired state with a new apply",
		strings.Join(e.ConflictingIDs, ", "), strings.Join(e.Aspects, ", "))
}

// RollbackResult summarizes the outcome of a successful changeset rollback.
type RollbackResult struct {
	ChangesetID        string   `json:"changeset_id"`
	TargetURN          string   `json:"target_urn"`
	RevertedChanges    []string `json:"reverted_changes"`
	SkippedChanges     []string `json:"skipped_changes,omitempty"`
	InsightsRolledBack []string `json:"insights_rolled_back"`
	RolledBackBy       string   `json:"rolled_back_by,omitempty"`
}

// recordedChange is a single change reconstructed from a changeset's new_value.
type recordedChange struct {
	ChangeType string
	Target     string
	Detail     string
}

// priorState holds the subset of an entity's before-image that rollback can use.
type priorState struct {
	Description   string
	Tags          map[string]bool
	GlossaryTerms map[string]bool
}

// RollbackDeps bundles the stores and writer a rollback operates on. It is the
// shared dependency set for both the apply_knowledge MCP tool and the admin REST
// endpoint.
type RollbackDeps struct {
	Writer     DataHubWriter
	Changesets ChangesetStore
	Insights   InsightStore
	// Pages reverts knowledge-page promotions (target "kp:<slug>"). Optional; a
	// nil Pages makes a page-changeset rollback return a clear "not configured"
	// error rather than mis-routing through the DataHub inverse-op path.
	// PageReverter and PageEditedError live in page_sink.go with the rest of the
	// page-sink machinery.
	Pages PageReverter
}

// RevertChangeset reverts the DataHub aspects mutated by a changeset back to
// their pre-change state, transitions the source insights to rolled_back, and
// marks the changeset as rolled back. It is the single rollback implementation
// shared by the apply_knowledge MCP tool and the admin REST endpoint.
//
// It refuses (rather than silently no-ops) when the changeset is already rolled
// back, when it contains change types whose prior state was not captured, or
// when a newer changeset has since touched the same aspect.
func RevertChangeset(ctx context.Context, deps RollbackDeps, cs *Changeset, rolledBackBy string) (*RollbackResult, error) {
	if cs.RolledBack {
		return nil, ErrChangesetAlreadyRolledBack
	}

	// Knowledge-page promotions (target "kp:<slug>") revert via the page sink,
	// not the DataHub inverse-op path. Shared here so both the apply_knowledge
	// tool and the admin REST endpoint route page changesets correctly.
	if strings.HasPrefix(cs.TargetURN, pageTargetPrefix) {
		return revertPageChangeset(ctx, deps, cs, rolledBackBy)
	}

	changes := parseRecordedChanges(cs.NewValue)
	if unsupported := unrevertibleChangeTypes(changes); len(unsupported) > 0 {
		return nil, &UnrevertibleError{ChangeTypes: unsupported}
	}
	if err := checkRollbackConflicts(ctx, deps.Changesets, cs, changes); err != nil {
		return nil, err
	}

	prior := parsePriorState(cs.PreviousValue)
	reverted, skipped, err := applyInverseChanges(ctx, deps.Writer, cs.TargetURN, changes, prior)
	if err != nil {
		return nil, fmt.Errorf("rollback aborted after reverting %d change(s): %w", len(reverted), err)
	}

	rolledBackInsights := rollbackInsights(ctx, deps.Insights, cs.SourceInsightIDs, rolledBackBy)

	if err := deps.Changesets.RollbackChangeset(ctx, cs.ID, rolledBackBy); err != nil {
		return nil, fmt.Errorf("reverted DataHub but recording the rollback failed: %w", err)
	}

	return &RollbackResult{
		ChangesetID:        cs.ID,
		TargetURN:          cs.TargetURN,
		RevertedChanges:    reverted,
		SkippedChanges:     skipped,
		InsightsRolledBack: rolledBackInsights,
		RolledBackBy:       rolledBackBy,
	}, nil
}

// revertibleChangeTypes is the set of change types whose inverse operation can be
// derived from the changeset's recorded before-image. Change types absent here
// (column descriptions, structured properties, incidents, curated queries,
// context documents, prompts) do not capture enough prior state to revert.
var revertibleChangeTypes = map[string]bool{
	string(actionUpdateDescription): true,
	string(actionAddTag):            true,
	string(actionRemoveTag):         true,
	string(actionFlagQualityIssue):  true,
	string(actionAddGlossaryTerm):   true,
	string(actionAddDocumentation):  true,
}

// unrevertibleChangeTypes returns the distinct change types in the changeset that
// cannot be reverted from the recorded before-image. Column-level description
// changes are unrevertible even though update_description itself is revertible,
// because the before-image only captures the entity-level description.
func unrevertibleChangeTypes(changes []recordedChange) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range changes {
		if !isRevertible(c) && !seen[c.ChangeType] {
			seen[c.ChangeType] = true
			out = append(out, c.ChangeType)
		}
	}
	return out
}

// isRevertible reports whether a single recorded change has a derivable inverse.
func isRevertible(c recordedChange) bool {
	if c.ChangeType == string(actionUpdateDescription) {
		_, isColumn := parseColumnTarget(c.Target)
		return !isColumn
	}
	return revertibleChangeTypes[c.ChangeType]
}

// applyInverseChanges performs the inverse of each recorded change against
// DataHub. It returns human-readable descriptions of the reverted and skipped
// (pre-existing, so left untouched) changes. On the first write error it stops
// and returns what was reverted so far alongside the error.
func applyInverseChanges(
	ctx context.Context,
	writer DataHubWriter,
	urn string,
	changes []recordedChange,
	prior priorState,
) (reverted, skipped []string, err error) {
	for _, c := range changes {
		desc, didRevert, applyErr := applyInverse(ctx, writer, urn, c, prior)
		if applyErr != nil {
			return reverted, skipped, applyErr
		}
		if didRevert {
			reverted = append(reverted, desc)
		} else {
			skipped = append(skipped, desc)
		}
	}
	return reverted, skipped, nil
}

// applyInverse reverts a single change. didRevert is false when the change was a
// no-op at apply time (the value already existed in the before-image), so the
// rollback intentionally leaves the pre-existing value in place.
func applyInverse(ctx context.Context, writer DataHubWriter, urn string, c recordedChange, prior priorState) (desc string, didRevert bool, err error) {
	switch c.ChangeType {
	case string(actionUpdateDescription):
		return revertDescription(ctx, writer, urn, prior)
	case string(actionAddTag):
		return revertAddedTag(ctx, writer, urn, normalizeTagURN(c.Detail), prior)
	case string(actionFlagQualityIssue):
		return revertAddedTag(ctx, writer, urn, qualityIssueTagURN, prior)
	case string(actionRemoveTag):
		return revertRemovedTag(ctx, writer, urn, normalizeTagURN(c.Detail), prior)
	case string(actionAddGlossaryTerm):
		return revertAddedTerm(ctx, writer, urn, normalizeGlossaryTermURN(c.Detail), prior)
	case string(actionAddDocumentation):
		return revertAddedDocumentation(ctx, writer, urn, c.Target)
	default:
		return "", false, fmt.Errorf("no inverse defined for change type %q", c.ChangeType)
	}
}

func revertDescription(ctx context.Context, writer DataHubWriter, urn string, prior priorState) (desc string, reverted bool, err error) {
	if err := writer.UpdateDescription(ctx, urn, prior.Description); err != nil {
		return "", false, fmt.Errorf("restoring description: %w", err)
	}
	return "restored description", true, nil
}

func revertAddedTag(ctx context.Context, writer DataHubWriter, urn, tagURN string, prior priorState) (desc string, reverted bool, err error) {
	if prior.Tags[tagURN] {
		return fmt.Sprintf("kept pre-existing tag %s", tagURN), false, nil
	}
	if err := writer.RemoveTag(ctx, urn, tagURN); err != nil {
		return "", false, fmt.Errorf("removing tag %s: %w", tagURN, err)
	}
	return fmt.Sprintf("removed tag %s", tagURN), true, nil
}

func revertRemovedTag(ctx context.Context, writer DataHubWriter, urn, tagURN string, prior priorState) (desc string, reverted bool, err error) {
	if !prior.Tags[tagURN] {
		return fmt.Sprintf("tag %s was not present before; nothing to restore", tagURN), false, nil
	}
	if err := writer.AddTag(ctx, urn, tagURN); err != nil {
		return "", false, fmt.Errorf("restoring tag %s: %w", tagURN, err)
	}
	return fmt.Sprintf("restored tag %s", tagURN), true, nil
}

func revertAddedTerm(ctx context.Context, writer DataHubWriter, urn, termURN string, prior priorState) (desc string, reverted bool, err error) {
	if prior.GlossaryTerms[termURN] {
		return fmt.Sprintf("kept pre-existing glossary term %s", termURN), false, nil
	}
	if err := writer.RemoveGlossaryTerm(ctx, urn, termURN); err != nil {
		return "", false, fmt.Errorf("removing glossary term %s: %w", termURN, err)
	}
	return fmt.Sprintf("removed glossary term %s", termURN), true, nil
}

func revertAddedDocumentation(ctx context.Context, writer DataHubWriter, urn, linkURL string) (desc string, reverted bool, err error) {
	if err := writer.RemoveDocumentationLink(ctx, urn, linkURL); err != nil {
		return "", false, fmt.Errorf("removing documentation link %s: %w", linkURL, err)
	}
	return fmt.Sprintf("removed documentation link %s", linkURL), true, nil
}

// rollbackInsights transitions each source insight to rolled_back. A failure on
// one insight is logged via the returned slice (the insight is simply omitted)
// rather than aborting the rollback, which has already mutated DataHub.
func rollbackInsights(ctx context.Context, store InsightStore, insightIDs []string, rolledBackBy string) []string {
	var done []string
	for _, id := range insightIDs {
		if err := store.MarkRolledBack(ctx, id, rolledBackBy); err == nil {
			done = append(done, id)
		}
	}
	return done
}

// parseRecordedChanges reconstructs the ordered changes from a changeset's
// new_value map, which uses change_0, change_1, ... keys (see changesToMap).
func parseRecordedChanges(newValue map[string]any) []recordedChange {
	var changes []recordedChange
	for i := 0; ; i++ {
		entry, ok := newValue[fmt.Sprintf("change_%d", i)].(map[string]any)
		if !ok {
			break
		}
		changes = append(changes, recordedChange{
			ChangeType: stringField(entry, "change_type"),
			Target:     stringField(entry, fieldTarget),
			Detail:     stringField(entry, fieldDetail),
		})
	}
	return changes
}

// parsePriorState extracts the revert-relevant subset of a changeset's
// previous_value before-image (see metadataToMap).
func parsePriorState(prev map[string]any) priorState {
	return priorState{
		Description:   stringField(prev, fieldDescription),
		Tags:          stringSetField(prev, "tags"),
		GlossaryTerms: stringSetField(prev, "glossary_terms"),
	}
}

// stringField reads a string value from a decoded JSON map, tolerating absence.
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// stringSetField reads a JSON array of strings into a set, tolerating absence
// and the []any shape produced by encoding/json.
func stringSetField(m map[string]any, key string) map[string]bool {
	out := map[string]bool{}
	switch v := m[key].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out[s] = true
			}
		}
	case []string:
		for _, s := range v {
			out[s] = true
		}
	}
	return out
}

// revertPageChangeset reverts a knowledge-page promotion: a create_page is
// soft-deleted, an update_page is restored to its before-image (a new version).
// It refuses (PageEditedError) if the page was edited after the promotion, so a
// rollback never clobbers a later human edit. Pure: returns a RollbackResult and
// typed errors so the apply_knowledge tool and the admin REST endpoint present
// failures uniformly (see writeRollbackError / rollbackErrorResult).
func revertPageChangeset(ctx context.Context, deps RollbackDeps, cs *Changeset, rolledBackBy string) (*RollbackResult, error) {
	if deps.Pages == nil {
		return nil, fmt.Errorf("knowledge-page rollback is not configured on this deployment")
	}
	slug := strings.TrimPrefix(cs.TargetURN, pageTargetPrefix)
	page, err := deps.Pages.GetBySlug(ctx, slug)
	if errors.Is(err, knowledgepage.ErrNotFound) {
		return nil, fmt.Errorf("knowledge page no longer exists: %s", slug)
	}
	if err != nil {
		return nil, fmt.Errorf("looking up knowledge page: %w", err)
	}
	if produced := intFromMap(cs.NewValue, pageFieldVersion); page.CurrentVersion != produced {
		return nil, &PageEditedError{Slug: slug, CurrentVersion: page.CurrentVersion, ChangesetVersion: produced}
	}

	reverted, err := applyPageRevert(ctx, deps.Pages, cs, page, rolledBackBy)
	if err != nil {
		return nil, err
	}

	rolledBackInsights := rollbackInsights(ctx, deps.Insights, cs.SourceInsightIDs, rolledBackBy)
	if err := deps.Changesets.RollbackChangeset(ctx, cs.ID, rolledBackBy); err != nil {
		return nil, fmt.Errorf("reverted the page but recording the rollback failed: %w", err)
	}
	return &RollbackResult{
		ChangesetID:        cs.ID,
		TargetURN:          cs.TargetURN,
		RevertedChanges:    []string{reverted},
		InsightsRolledBack: rolledBackInsights,
		RolledBackBy:       rolledBackBy,
	}, nil
}

// applyPageRevert performs the inverse page operation for a promotion changeset:
// soft-delete a created page or restore an updated page's before-image. Returns a
// human-readable summary of what it reverted.
func applyPageRevert(ctx context.Context, pages PageReverter, cs *Changeset, page *knowledgepage.Page, rolledBackBy string) (string, error) {
	switch cs.ChangeType {
	case changeCreatePage:
		if err := pages.SoftDelete(ctx, page.ID); err != nil {
			return "", fmt.Errorf("deleting knowledge page: %w", err)
		}
		return "deleted page " + page.Slug, nil
	case changeUpdatePage:
		title := stringField(cs.PreviousValue, pageFieldTitle)
		summary := stringField(cs.PreviousValue, pageFieldSummary)
		body := stringField(cs.PreviousValue, pageFieldBody)
		tags := strsFromMap(cs.PreviousValue, pageFieldTags)
		if err := pages.Update(ctx, page.ID, knowledgepage.Update{
			Title: &title, Summary: &summary, Body: &body, Tags: &tags,
			UpdatedBy: rolledBackBy, ChangeSummary: "rollback of changeset " + cs.ID,
		}); err != nil {
			return "", fmt.Errorf("restoring knowledge page: %w", err)
		}
		// Restore the page's promoted references to the prior set (#664), scoped to
		// source=promoted so manual and inline references survive the rollback. The
		// previous-value URNs are serialized references of any type.
		if err := pages.ReplaceEntityRefsBySource(ctx, page.ID, knowledgepage.RefSourcePromoted,
			promotedRefsFromURNs(strsFromMap(cs.PreviousValue, pageFieldEntityURNs))); err != nil {
			return "", fmt.Errorf("restoring knowledge page references: %w", err)
		}
		// Re-derive the inline references from the restored body (#678) so they stay
		// consistent with the body after a rollback; manual refs are untouched.
		if err := pages.ReplaceEntityRefsBySource(ctx, page.ID, knowledgepage.RefSourceInline,
			knowledgepage.ScanBodyRefs(body)); err != nil {
			return "", fmt.Errorf("restoring inline knowledge page references: %w", err)
		}
		return "restored page " + page.Slug, nil
	default:
		return "", fmt.Errorf("unknown page change type: %s", cs.ChangeType)
	}
}
