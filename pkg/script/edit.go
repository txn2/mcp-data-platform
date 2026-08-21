package script

import (
	"context"
	"fmt"
	"slices"
)

// Edit is one edit crossing the funnel: the persisted pre-edit state, the fully
// mutated copy, and who wrote it with the authority they held.
type Edit struct {
	// Before is the persisted pre-edit state and After the fully mutated copy.
	Before *Script
	After  *Script
	// Author is recorded on any version produced, together with the authority
	// they held, which is what a run of that version presents (see Author).
	Author Author
}

// SnapshotChanged reports whether any versioned snapshot field (source, params,
// display name, description, category, tags) differs between the two states.
func SnapshotChanged(before, after *Script) bool {
	return before.Source != after.Source ||
		before.DisplayName != after.DisplayName ||
		before.Description != after.Description ||
		before.Category != after.Category ||
		!ParamsEqual(before.Params, after.Params) ||
		!slices.Equal(before.Tags, after.Tags)
}

// ApplyEdit lands a script edit through the one shared gate every mutation
// surface crosses: the manage_script tool and the portal editor.
//
// Every edit is applied to the live row. A store with versioning records a new
// applied version when a versioned field changed, so the saved script and the
// script a run executes are always the same code; a store without it degrades
// to a plain unversioned update.
func ApplyEdit(ctx context.Context, store Store, e Edit) error {
	if versions, ok := store.(VersionStore); ok {
		if err := versions.UpdateWithVersion(ctx, e.After, e.Author); err != nil {
			return fmt.Errorf("updating script: %w", err)
		}
		return nil
	}
	if err := store.Update(ctx, e.After); err != nil {
		return fmt.Errorf("updating script: %w", err)
	}
	return nil
}
