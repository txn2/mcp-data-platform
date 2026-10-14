package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
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

// RollbackConflictError is returned when an aspect the changeset wrote has been
// written again since. Reverting would silently discard that newer write, so
// the rollback is refused and nothing is reverted.
type RollbackConflictError struct {
	Conflict *catalog.ConflictError
}

// Error implements the error interface.
func (e *RollbackConflictError) Error() string {
	return "rollback blocked: " + e.Conflict.Error() +
		"; roll that change back first or restore the desired state with a new apply"
}

// Unwrap exposes the catalog's conflict.
func (e *RollbackConflictError) Unwrap() error { return e.Conflict }

// RollbackResult summarizes the outcome of a successful changeset rollback.
type RollbackResult struct {
	ChangesetID     string   `json:"changeset_id"`
	TargetURN       string   `json:"target_urn"`
	RevertedChanges []string `json:"reverted_changes"`
	SkippedChanges  []string `json:"skipped_changes,omitempty"`
	// InsightsReturnedToReview lists the source insights the rollback sent back to
	// the review queue as pending (#1257). Only insights that actually moved are
	// listed: one already returned by an earlier rollback, or since decided some
	// other way, is left as it is.
	InsightsReturnedToReview []string `json:"insights_returned_to_review"`
	RolledBackBy             string   `json:"rolled_back_by,omitempty"`
}

// recordedChange is a single change reconstructed from a changeset's new_value.
type recordedChange struct {
	ChangeType string
	Target     string
	Detail     string
}

// RollbackDeps bundles the stores a rollback operates on. It is the shared
// dependency set for both the apply_knowledge MCP tool and the admin REST
// endpoint.
type RollbackDeps struct {
	// Catalog is the platform catalog a catalog changeset is rolled back in.
	Catalog    catalog.Store
	Changesets ChangesetStore
	Insights   InsightStore
	// Pages reverts knowledge-page promotions (target "kp:<slug>"). Optional; a
	// nil Pages makes a page-changeset rollback return a clear "not configured"
	// error rather than mis-routing through the catalog path.
	// PageReverter and PageEditedError live in page_sink.go with the rest of the
	// page-sink machinery.
	Pages PageReverter
	// Instructions reverts promotions into the deployment's customized agent
	// instructions (target "ai:<section>"). Optional; a nil Instructions makes an
	// instructions-changeset rollback return a clear "not configured" error rather
	// than mis-routing through the catalog path. InstructionsStore and
	// InstructionsEditedError live in instructions_sink.go with the rest of that
	// sink's machinery.
	Instructions InstructionsStore
}

// RevertChangeset rolls back a changeset, returns its source insights to the
// review queue, and marks it rolled back. It is the single rollback
// implementation shared by the apply_knowledge MCP tool and the admin REST
// endpoint.
//
// A catalog changeset is undone through the catalog's own history, every
// aspect it wrote restored in one transaction. It refuses (rather than
// silently no-ops) when the changeset is already rolled back, when it created
// a prompt, or when an aspect it wrote has been written again since.
func RevertChangeset(ctx context.Context, deps RollbackDeps, cs *Changeset, rolledBackBy string) (*RollbackResult, error) {
	if cs.RolledBack {
		return nil, ErrChangesetAlreadyRolledBack
	}

	// Knowledge-page promotions (target "kp:<slug>") revert via the page sink.
	// Shared here so both the apply_knowledge tool and the admin REST endpoint
	// route page changesets correctly.
	if strings.HasPrefix(cs.TargetURN, pageTargetPrefix) {
		return revertPageChangeset(ctx, deps, cs, rolledBackBy)
	}
	// Agent-instruction promotions (target "ai:<section>") revert through the
	// instructions sink, restoring the customized layer's prior text and, for a
	// diverted rule, the page the section indexed.
	if strings.HasPrefix(cs.TargetURN, instructionsTargetPrefix) {
		return revertInstructionsChangeset(ctx, deps, cs, rolledBackBy)
	}

	reverted, err := catalogRollback(ctx, deps.Catalog, cs, rolledBackBy)
	if err != nil {
		return nil, err
	}
	var skipped []string

	returnedInsights := returnInsightsToReview(ctx, deps.Insights, cs.SourceInsightIDs, rolledBackBy, cs.ID)

	if err := deps.Changesets.RollbackChangeset(ctx, cs.ID, rolledBackBy); err != nil {
		return nil, fmt.Errorf("reverted the catalog changes but recording the rollback failed: %w", err)
	}

	return &RollbackResult{
		ChangesetID:              cs.ID,
		TargetURN:                cs.TargetURN,
		RevertedChanges:          reverted,
		SkippedChanges:           skipped,
		InsightsReturnedToReview: returnedInsights,
		RolledBackBy:             rolledBackBy,
	}, nil
}

// RollbackReviewNote is the review note left on an insight a rollback returned to
// the queue (#1257). It names the changeset whose revert sent it back, so the
// reviewer reading the queue entry knows why an insight carrying applied_by and
// applied_at is pending again.
func RollbackReviewNote(changesetID string) string {
	if changesetID == "" {
		return "Returned to review: the changeset that applied this insight was rolled back."
	}
	return fmt.Sprintf("Returned to review: changeset %s was rolled back.", changesetID)
}

// returnInsightsToReview sends every source insight of a rolled-back changeset
// back to the review queue, and returns the ids that actually moved. An insight
// that was not in the applied state (already returned by an earlier rollback, or
// since decided some other way) is left alone and omitted, as is one whose store
// write fails: the rollback has already mutated the sink, so it must not abort
// here. Every id is attempted, so a changeset carrying several insights returns
// all of them, not just the first.
func returnInsightsToReview(ctx context.Context, store InsightStore, insightIDs []string, rolledBackBy, changesetID string) []string {
	var done []string
	for _, id := range insightIDs {
		if returned, err := store.ReturnToReview(ctx, id, rolledBackBy, changesetID); err == nil && returned {
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

// stringField reads a string value from a decoded JSON map, tolerating absence.
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// revertPageChangeset reverts a knowledge-page promotion: a create_page is
// soft-deleted, an update_page is restored to its before-image (a new version).
// It refuses (PageEditedError) if the page was edited after the promotion, so a
// rollback never clobbers a later human edit. Pure: returns a RollbackResult and
// typed errors so the apply_knowledge tool and the admin REST endpoint present
// failures uniformly (see writeRollbackError / rollbackErrorResult).
func revertPageChangeset(ctx context.Context, deps RollbackDeps, cs *Changeset, rolledBackBy string) (*RollbackResult, error) {
	if deps.Pages == nil {
		return nil, errors.New("knowledge-page rollback is not configured on this deployment")
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

	reverted, err := applyPageRevert(ctx, pageRevert{
		pages: deps.Pages, cs: cs, op: cs.ChangeType, page: page, by: rolledBackBy,
	})
	if err != nil {
		return nil, err
	}

	returnedInsights := returnInsightsToReview(ctx, deps.Insights, cs.SourceInsightIDs, rolledBackBy, cs.ID)
	if err := deps.Changesets.RollbackChangeset(ctx, cs.ID, rolledBackBy); err != nil {
		return nil, fmt.Errorf("reverted the page but recording the rollback failed: %w", err)
	}
	return &RollbackResult{
		ChangesetID:              cs.ID,
		TargetURN:                cs.TargetURN,
		RevertedChanges:          []string{reverted},
		InsightsReturnedToReview: returnedInsights,
		RolledBackBy:             rolledBackBy,
	}, nil
}

// pageRevert carries the inputs of one inverse page operation. op is the page
// change type to invert, held separately from cs.ChangeType because an
// agent-instructions promotion that diverted its body onto a page carries the
// page's operation under its own key, the changeset's own change type describing
// the instruction write (#1607).
type pageRevert struct {
	pages PageReverter
	cs    *Changeset
	op    string
	page  *knowledgepage.Page
	by    string
}

// applyPageRevert performs the inverse page operation for a promotion changeset:
// soft-delete a created page or restore an updated page's before-image. Returns a
// human-readable summary of what it reverted.
func applyPageRevert(ctx context.Context, r pageRevert) (string, error) {
	pages, cs, page, rolledBackBy := r.pages, r.cs, r.page, r.by
	switch r.op {
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
		return "", fmt.Errorf("unknown page change type: %s", r.op)
	}
}
