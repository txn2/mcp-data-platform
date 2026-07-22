package prompt

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrReviewRequiredMixedEdit rejects an edit that combines a review-gated
// substance change (content or arguments of an approved shared prompt) with
// changes a draft version cannot carry (scope, personas, status, name,
// category, owner, enabled, promotion request). The two must be submitted
// separately so the deferred draft is exactly the reviewable snapshot.
var ErrReviewRequiredMixedEdit = errors.New(
	"content changes to an approved shared prompt require review and cannot be combined with " +
		"scope, status, or other non-versioned changes; submit them as separate updates")

// EditOutcome reports how ApplyEdit landed an edit: applied to the live prompt
// row, or deferred as a pending draft version awaiting admin approval.
type EditOutcome struct {
	// Applied is true when the live row was updated (the edit is being served).
	Applied bool `json:"applied"`
	// PendingVersion is the draft version number when the edit was deferred
	// for review; zero when Applied.
	PendingVersion int `json:"pending_version,omitempty"`
}

// RequiresReview reports whether an edit must go through admin review before
// being served: a change to the prompt's substance (content or arguments) when
// the pre-edit prompt is an approved shared (global or persona) prompt.
// Personal prompts version silently (the owner is the only consumer), a
// prompt that was never approved has no approved snapshot to protect, and
// system rows are read-only config mirrors that are never versioned or
// reviewed (their content is owned by server configuration and re-ingested at
// startup).
func RequiresReview(before, after *Prompt) bool {
	if before.Status != StatusApproved || before.Source == SourceSystem {
		return false
	}
	if before.Scope != ScopeGlobal && before.Scope != ScopePersona {
		return false
	}
	return before.Content != after.Content || !slices.Equal(before.Arguments, after.Arguments)
}

// SnapshotChanged reports whether any versioned snapshot field (content,
// display name, description, arguments, tags) differs between the two states.
func SnapshotChanged(before, after *Prompt) bool {
	return before.Content != after.Content ||
		before.DisplayName != after.DisplayName ||
		before.Description != after.Description ||
		!slices.Equal(before.Arguments, after.Arguments) ||
		!slices.Equal(before.Tags, after.Tags)
}

// unversionedFieldsChanged reports whether the edit touches fields a version
// snapshot cannot carry. Used to reject mixed edits when review is required.
func unversionedFieldsChanged(before, after *Prompt) bool {
	return identityFieldsChanged(before, after) || lifecycleFieldsChanged(before, after)
}

// identityFieldsChanged covers the fields naming and placing the prompt.
func identityFieldsChanged(before, after *Prompt) bool {
	return before.Name != after.Name ||
		before.Category != after.Category ||
		before.CollectionID != after.CollectionID ||
		before.Scope != after.Scope ||
		!slices.Equal(before.Personas, after.Personas) ||
		before.OwnerEmail != after.OwnerEmail ||
		before.Source != after.Source
}

// lifecycleFieldsChanged covers the availability and lifecycle fields.
func lifecycleFieldsChanged(before, after *Prompt) bool {
	return before.Enabled != after.Enabled ||
		before.Status != after.Status ||
		before.SupersededBy != after.SupersededBy ||
		before.ReviewRequested != after.ReviewRequested ||
		before.RequestedScope != after.RequestedScope ||
		!slices.Equal(before.RequestedPersonas, after.RequestedPersonas)
}

// ApplyEdit lands a prompt edit through the one shared review gate used by
// every mutation surface (manage_prompt tool, admin REST, portal REST).
// before must be the persisted pre-edit state and after the fully mutated
// copy; author is the actor recorded on any version produced.
//
// A review-gated edit (RequiresReview) becomes a pending draft version and
// leaves the live row untouched, so other users keep being served the
// approved snapshot until an admin approves the draft. Every other edit is
// applied via UpdateWithVersion, which snapshots a new applied version when a
// versioned field changed. The versioning capability is asserted from the
// store itself; a store without it (no-DB deployments, plain test stores)
// degrades to a plain unversioned update.
func ApplyEdit(ctx context.Context, store Store, before, after *Prompt, author string) (EditOutcome, error) {
	versions, _ := store.(VersionStore)
	if versions == nil {
		if err := store.Update(ctx, after); err != nil {
			return EditOutcome{}, fmt.Errorf("updating prompt: %w", err)
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
		return EditOutcome{}, fmt.Errorf("updating prompt: %w", err)
	}
	return EditOutcome{Applied: true}, nil
}
