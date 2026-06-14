package portal

import (
	"context"
	"fmt"
	"log/slog"
)

// Shared target gathering for cross-artifact feedback views. The REST worklist
// and activity feed (#617) and the manage_feedback MCP tool (#618) all need the
// set of asset/collection ids a caller owns or holds a share on. Centralized
// here so the policy lives in one place rather than forked per surface.

// targetGatherCap bounds how many owned/shared artifacts a single gather pulls,
// so a caller with an enormous library cannot issue an unbounded query.
const targetGatherCap = 1000

// ShareKeep decides whether a share grant counts toward a gathered target set.
// KeepEditorShares is the "I can act on it" scope (worklist, MCP agent); KeepAnyShare
// is the "I can view it" scope (activity feed).
type ShareKeep func(SharePermission) bool

// KeepEditorShares keeps only editor-permission shares.
func KeepEditorShares(p SharePermission) bool { return p == PermissionEditor }

// KeepAnyShare keeps shares at any permission.
func KeepAnyShare(SharePermission) bool { return true }

// TargetGatherer gathers the asset/collection ids a single user can reach,
// bundling the stores and identity so callers (REST worklist/activity feed, the
// manage_feedback MCP tool) build it once and ask for asset or collection ids
// with the desired share scope.
type TargetGatherer struct {
	Assets      AssetStore
	Collections CollectionStore
	Shares      ShareStore
	UserID      string
	Email       string
}

// AssetIDs returns the ids of assets the user owns plus shared assets whose
// permission satisfies keep.
func (g TargetGatherer) AssetIDs(ctx context.Context, keep ShareKeep) ([]string, error) {
	var ids []string
	if g.Assets != nil {
		owned, total, err := g.Assets.List(ctx, AssetFilter{OwnerID: g.UserID, Limit: targetGatherCap})
		if err != nil {
			return nil, fmt.Errorf("listing owned assets: %w", err)
		}
		warnIfTargetsTruncated(total, "owned assets", g.UserID)
		for _, a := range owned {
			ids = append(ids, a.ID)
		}
	}
	if g.Shares != nil {
		shared, total, err := g.Shares.ListSharedWithUser(ctx, g.UserID, g.Email, targetGatherCap, 0)
		if err != nil {
			return nil, fmt.Errorf("listing shared assets: %w", err)
		}
		warnIfTargetsTruncated(total, "shared assets", g.UserID)
		for _, s := range shared {
			if keep(s.Permission) {
				ids = append(ids, s.Asset.ID)
			}
		}
	}
	return ids, nil
}

// CollectionIDs returns the ids of collections the user owns plus shared
// collections whose permission satisfies keep.
func (g TargetGatherer) CollectionIDs(ctx context.Context, keep ShareKeep) ([]string, error) {
	var ids []string
	if g.Collections != nil {
		owned, total, err := g.Collections.List(ctx, CollectionFilter{OwnerID: g.UserID, Limit: targetGatherCap})
		if err != nil {
			return nil, fmt.Errorf("listing owned collections: %w", err)
		}
		warnIfTargetsTruncated(total, "owned collections", g.UserID)
		for _, c := range owned {
			ids = append(ids, c.ID)
		}
	}
	if g.Shares != nil {
		shared, total, err := g.Shares.ListSharedCollectionsWithUser(ctx, g.UserID, g.Email, targetGatherCap, 0)
		if err != nil {
			return nil, fmt.Errorf("listing shared collections: %w", err)
		}
		warnIfTargetsTruncated(total, "shared collections", g.UserID)
		for _, s := range shared {
			if keep(s.Permission) {
				ids = append(ids, s.Collection.ID)
			}
		}
	}
	return ids, nil
}

// warnIfTargetsTruncated logs when a user's owned/shared set exceeds the cap, so
// the silent omission of artifacts past the cap is observable to operators.
func warnIfTargetsTruncated(total int, kind, userID string) {
	if total > targetGatherCap {
		slog.Warn("feedback target set truncated; some artifacts are omitted",
			"kind", kind, "user", userID, "total", total, "cap", targetGatherCap)
	}
}
