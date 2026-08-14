package script

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrReviewRequiredMixedEdit rejects an edit that combines a review-gated
// substance change (the source or parameter contract of a script with an
// approved version) with changes a draft version cannot carry (scope, personas,
// status, name, owner, enabled). The two must be submitted separately so the
// deferred draft is exactly the reviewable snapshot — a reviewer approving a
// code change must not also be silently approving a scope widening.
var ErrReviewRequiredMixedEdit = errors.New(
	"source or parameter changes to an approved script require review and cannot be combined with " +
		"scope, status, or other non-versioned changes; submit them as separate updates")

// EditOutcome reports how ApplyEdit landed an edit: applied to the live script
// row, or deferred as a pending draft version awaiting approval.
type EditOutcome struct {
	// Applied is true when the live row was updated.
	Applied bool `json:"applied"`
	// PendingVersion is the draft version number when the edit was deferred for
	// review; zero when Applied.
	PendingVersion int `json:"pending_version,omitempty"`
}

// RequiresReview reports whether an edit must go through review before it can
// be served: a change to the script's substance (source or parameter contract)
// when the pre-edit script has an approved version.
//
// This is where the script domain deliberately parts company with prompts.
// prompt.RequiresReview gates only APPROVED SHARED prompts, because a personal
// prompt has exactly one consumer — its owner — and versioning it silently
// costs nobody anything. A script with an approved version is executed by the
// platform, on a schedule, under a governed identity; letting the owner swap
// the code out from under that approval would make the approval meaningless at
// any scope. So the gate here keys on the execution pointer, not on visibility.
//
// A script with no approved version is pure authoring: nothing executes it but
// its author, through run_draft, under their own authority. Those edits apply
// directly and snapshot an applied version, which is every edit today.
func RequiresReview(before, after *Script) bool {
	if !before.Executable() {
		return false
	}
	return before.Source != after.Source || !ParamsEqual(before.Params, after.Params)
}

// SnapshotChanged reports whether any versioned snapshot field (source, params,
// display name, description, tags) differs between the two states.
func SnapshotChanged(before, after *Script) bool {
	return before.Source != after.Source ||
		before.DisplayName != after.DisplayName ||
		before.Description != after.Description ||
		!ParamsEqual(before.Params, after.Params) ||
		!slices.Equal(before.Tags, after.Tags)
}

// unversionedFieldsChanged reports whether the edit touches fields a version
// snapshot cannot carry. Used to reject mixed edits when review is required.
func unversionedFieldsChanged(before, after *Script) bool {
	return before.Name != after.Name ||
		before.Scope != after.Scope ||
		!slices.Equal(before.Personas, after.Personas) ||
		before.OwnerEmail != after.OwnerEmail ||
		before.Enabled != after.Enabled ||
		before.Status != after.Status ||
		before.SupersededBy != after.SupersededBy ||
		before.ApprovedVersionID != after.ApprovedVersionID
}

// ApplyEdit lands a script edit through the one shared gate every mutation
// surface crosses (the manage_script tool today; admin REST and the portal
// later). before must be the persisted pre-edit state and after the fully
// mutated copy; author is the actor recorded on any version produced.
//
// A review-gated edit (RequiresReview) becomes a pending draft version and
// leaves the live row untouched, so the approved version keeps executing until
// the draft is approved. Every other edit is applied via UpdateWithVersion,
// which snapshots a new applied version when a versioned field changed. The
// versioning capability is asserted from the store itself; a store without it
// degrades to a plain unversioned update.
func ApplyEdit(ctx context.Context, store Store, before, after *Script, author string) (EditOutcome, error) {
	versions, _ := store.(VersionStore)
	if versions == nil {
		if err := store.Update(ctx, after); err != nil {
			return EditOutcome{}, fmt.Errorf("updating script: %w", err)
		}
		return EditOutcome{Applied: true}, nil
	}
	if RequiresReview(before, after) {
		if unversionedFieldsChanged(before, after) {
			return EditOutcome{}, ErrReviewRequiredMixedEdit
		}
		n, err := versions.CreateDraftVersion(ctx, before.ID, after, author)
		if err != nil {
			return EditOutcome{}, fmt.Errorf("creating draft version: %w", err)
		}
		return EditOutcome{PendingVersion: n}, nil
	}
	if err := versions.UpdateWithVersion(ctx, after, author); err != nil {
		return EditOutcome{}, fmt.Errorf("updating script: %w", err)
	}
	return EditOutcome{Applied: true}, nil
}
