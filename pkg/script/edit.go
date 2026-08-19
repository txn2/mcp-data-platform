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
	// Auto is what automatic approval did with an applied version (#1367). It is
	// the zero value on a deferred edit, which is a version waiting for a
	// reviewer by definition.
	Auto AutoOutcome `json:"-"`
}

// Edit is one edit crossing the funnel: the persisted pre-edit state, the fully
// mutated copy, who wrote it and the authority they held, and the automatic
// approval an owner-authored personal version may carry.
type Edit struct {
	// Before is the persisted pre-edit state and After the fully mutated copy.
	Before *Script
	After  *Script
	// Author is recorded on any version produced, together with the authority
	// they held, which becomes the ceiling on what approving that version can
	// grant (see Author).
	Author Author
	// Auto mints the approval a personal script's own version carries (#1367).
	// Nil is a deployment with no automatic approval, where every version waits
	// for a reviewer.
	Auto AutoApprover
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
// display name, description, category, tags) differs between the two states.
//
// The four documentation fields are versioned together and none of them is
// gated by RequiresReview, which is what lets one form edit all of them at once
// (#1369): an edit that only documents a script applies to the live row
// immediately, is captured as a version like every other edit, and does not
// send the version that is executing back to a reviewer.
func SnapshotChanged(before, after *Script) bool {
	return before.Source != after.Source ||
		before.DisplayName != after.DisplayName ||
		before.Description != after.Description ||
		before.Category != after.Category ||
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
// surface crosses: the manage_script tool and the portal editor.
//
// A review-gated edit (RequiresReview) becomes a pending draft version and
// leaves the live row untouched, so the approved version keeps executing until
// the draft is approved. Every other edit is applied via UpdateWithVersion,
// which snapshots a new applied version when a versioned field changed. The
// versioning capability is asserted from the store itself; a store without it
// degrades to a plain unversioned update.
//
// An applied version is then offered to automatic approval (#1367), which is
// the one place that happens: a personal script whose owner wrote the edit
// becomes executable here, and every other script leaves this function exactly
// as it did before, waiting for a reviewer.
func ApplyEdit(ctx context.Context, store Store, e Edit) (EditOutcome, error) {
	versions, _ := store.(VersionStore)
	if versions == nil {
		if err := store.Update(ctx, e.After); err != nil {
			return EditOutcome{}, fmt.Errorf("updating script: %w", err)
		}
		return EditOutcome{Applied: true}, nil
	}
	// The approval decision is taken BEFORE anything is written, because it is
	// what decides where the edit goes. An edit automatic approval covers is
	// applied to the live row and approved onto it; one it does not cover
	// becomes a draft and waits for a reviewer, exactly as every edit did before.
	decision := e.consider(ctx)
	if RequiresReview(e.Before, e.After) && !decision.Approvable {
		return deferForReview(ctx, versions, e, decision)
	}
	produced := e.Before.Version
	if err := versions.UpdateWithVersion(ctx, e.After, e.Author, decision.Approvable); err != nil {
		return EditOutcome{}, fmt.Errorf("updating script: %w", err)
	}
	return EditOutcome{Applied: true, Auto: e.approve(ctx, decision, produced)}, nil
}

// deferForReview snapshots the edit as a pending draft, leaving the live row and
// the version it is executing untouched.
func deferForReview(
	ctx context.Context, versions VersionStore, e Edit, decision AutoDecision,
) (EditOutcome, error) {
	if unversionedFieldsChanged(e.Before, e.After) {
		return EditOutcome{}, ErrReviewRequiredMixedEdit
	}
	n, err := versions.CreateDraftVersion(ctx, e.Before.ID, e.After, e.Author)
	if err != nil {
		return EditOutcome{}, fmt.Errorf("creating draft version: %w", err)
	}
	return EditOutcome{PendingVersion: n, Auto: AutoOutcome{Reason: decision.Reason}}, nil
}

// consider asks what automatic approval would do with this edit. A deployment
// with no approver declines everything, with no reason: "this deployment does
// not do that" is not something to put in front of the person who pressed save.
func (e Edit) consider(ctx context.Context) AutoDecision {
	if e.Auto == nil {
		return AutoDecision{}
	}
	return e.Auto.Consider(ctx, e.After, e.Author)
}

// approve binds a decision to the version this edit PRODUCED, and to no other.
//
// An edit that changed nothing a version snapshot carries — enabled, personas,
// a name — produces no version, and the one the live row already carries was
// written by somebody at some other time. Approving that would bind the roles of
// whoever wrote it, which for an edit by the owner of a script somebody else
// last edited is exactly the authority the whole model exists to cap. So when
// nothing was produced, nothing is approved: the script keeps whatever approval
// state it already had.
func (e Edit) approve(ctx context.Context, decision AutoDecision, before int) AutoOutcome {
	if e.Auto == nil || e.After.Version == before {
		return AutoOutcome{}
	}
	return e.Auto.Approve(ctx, e.After, e.After.Version, decision)
}
