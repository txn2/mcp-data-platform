package knowledge

import (
	"context"
	"fmt"
	"sort"
)

// aspectFamily maps a change type to the DataHub aspect it mutates. Two
// changesets conflict for rollback purposes when they touch the same family.
func aspectFamily(c recordedChange) string {
	switch c.ChangeType {
	case string(actionUpdateDescription):
		if field, isColumn := parseColumnTarget(c.Target); isColumn {
			return "column_description:" + field
		}
		return "description"
	case string(actionAddTag), string(actionRemoveTag), string(actionFlagQualityIssue):
		return "tags"
	case string(actionAddGlossaryTerm):
		return "glossary_terms"
	case string(actionAddDocumentation):
		return "documentation"
	default:
		return c.ChangeType
	}
}

// aspectFamilies returns the distinct aspect families a set of changes touches.
func aspectFamilies(changes []recordedChange) map[string]bool {
	out := map[string]bool{}
	for _, c := range changes {
		out[aspectFamily(c)] = true
	}
	return out
}

// checkRollbackConflicts refuses the rollback when a newer, not-yet-rolled-back
// changeset on the same entity has mutated an aspect this changeset also touched.
// Reverting in that case would silently clobber the newer write, so the caller
// must roll the newer changeset back first (or re-apply the desired state).
func checkRollbackConflicts(ctx context.Context, csStore ChangesetStore, cs *Changeset, changes []recordedChange) error {
	families := aspectFamilies(changes)

	notRolledBack := false
	since := cs.CreatedAt
	later, _, err := csStore.ListChangesets(ctx, ChangesetFilter{
		EntityURN:  cs.TargetURN,
		Since:      &since,
		RolledBack: &notRolledBack,
		Limit:      MaxLimit,
	})
	if err != nil {
		return fmt.Errorf("checking for conflicting changesets: %w", err)
	}

	conflictIDs := map[string]bool{}
	conflictAspects := map[string]bool{}
	for i := range later {
		other := &later[i]
		// Defensive: do not rely solely on the store's RolledBack filter, and never
		// treat the changeset itself or an older/equal one as a conflict.
		if other.ID == cs.ID || other.RolledBack || !other.CreatedAt.After(cs.CreatedAt) {
			continue
		}
		for fam := range aspectFamilies(parseRecordedChanges(other.NewValue)) {
			if families[fam] {
				conflictIDs[other.ID] = true
				conflictAspects[fam] = true
			}
		}
	}

	if len(conflictIDs) == 0 {
		return nil
	}
	return &RollbackConflictError{
		ConflictingIDs: sortedKeys(conflictIDs),
		Aspects:        sortedKeys(conflictAspects),
	}
}

// sortedKeys returns the keys of a set in deterministic order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
