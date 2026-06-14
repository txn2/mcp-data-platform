package portal

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
)

// Worklists / inbox (Phase 3 / #603). Two self-scoped cross-artifact views so
// no feedback is dropped:
//   - practitioner: open threads that require resolution across every artifact
//     the caller owns or can edit.
//   - SME: threads awaiting the caller's validation (validation_state=pending on
//     threads the caller authored, i.e. the requests routed back to them).

// practitionerWorklist handles GET /api/v1/portal/worklist/practitioner.
//
// @Summary      Practitioner worklist
// @Description  Open, resolution-required feedback threads across every artifact the caller owns or can edit.
// @Tags         Feedback
// @Produce      json
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/worklist/practitioner [get]
func (h *Handler) practitionerWorklist(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	assetIDs, collIDs, err := h.ownedOrEditableTargets(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve your artifacts")
		return
	}
	// No artifacts: nothing to do (do not fall through to an unscoped query).
	if len(assetIDs) == 0 && len(collIDs) == 0 {
		writeJSON(w, http.StatusOK, paginatedResponse{Data: []ThreadWithMeta{}, Limit: defaultThreadLimit})
		return
	}
	requires := true
	filter := ThreadFilter{
		TargetAssetIDs:      assetIDs,
		TargetCollectionIDs: collIDs,
		Status:              ThreadStatusOpen,
		RequiresResolution:  &requires,
		Limit:               intParam(r, paramLimit, defaultThreadLimit),
		Offset:              intParam(r, paramOffset, 0),
	}
	h.writeWorklist(w, r, filter)
}

// smeWorklist handles GET /api/v1/portal/worklist/sme.
//
// @Summary      SME validation worklist
// @Description  Threads awaiting the caller's validation (validation requests routed back to the feedback author).
// @Tags         Feedback
// @Produce      json
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/worklist/sme [get]
func (h *Handler) smeWorklist(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	filter := ThreadFilter{
		AuthorID:        user.UserID,
		AuthorEmail:     user.Email,
		ValidationState: ValidationStatePending,
		Limit:           intParam(r, paramLimit, defaultThreadLimit),
		Offset:          intParam(r, paramOffset, 0),
	}
	h.writeWorklist(w, r, filter)
}

// writeWorklist runs a worklist filter and writes the paginated result.
func (h *Handler) writeWorklist(w http.ResponseWriter, r *http.Request, filter ThreadFilter) {
	threads, total, err := h.deps.ThreadStore.ListThreads(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load worklist")
		return
	}
	if threads == nil {
		threads = []ThreadWithMeta{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: threads, Total: total, Limit: filter.EffectiveLimit(), Offset: filter.Offset,
	})
}

// ownedOrEditableTargets returns the ids of every asset and collection the user
// owns or holds an active editor share on (the worklist's "I can act on it"
// scope). The feedback activity feed reuses gatherAssetIDs/gatherCollectionIDs
// with keepAnyShare for its broader "I can view it" scope.
func (h *Handler) ownedOrEditableTargets(ctx context.Context, user *User) (assetIDs, collIDs []string, err error) {
	assetIDs, err = h.gatherAssetIDs(ctx, user, keepEditorShare)
	if err != nil {
		return nil, nil, err
	}
	collIDs, err = h.gatherCollectionIDs(ctx, user, keepEditorShare)
	if err != nil {
		return nil, nil, err
	}
	return assetIDs, collIDs, nil
}

// keepEditorShare keeps only editor-permission shares (worklist scope).
func keepEditorShare(p SharePermission) bool { return p == PermissionEditor }

// keepAnyShare keeps shares at any permission (activity-feed view scope).
func keepAnyShare(SharePermission) bool { return true }

// gatherAssetIDs returns the assets the user owns plus the shared assets whose
// permission satisfies keepShare. The keepShare predicate is the only
// difference between the worklist (editor) and the activity feed (any) scopes.
func (h *Handler) gatherAssetIDs(ctx context.Context, user *User, keepShare func(SharePermission) bool) ([]string, error) {
	var ids []string
	if h.deps.AssetStore != nil {
		owned, total, err := h.deps.AssetStore.List(ctx, AssetFilter{OwnerID: user.UserID, Limit: worklistTargetCap})
		if err != nil {
			return nil, fmt.Errorf("listing owned assets: %w", err)
		}
		warnIfTruncated(total, "owned assets", user.UserID)
		for _, a := range owned {
			ids = append(ids, a.ID)
		}
	}
	if h.deps.ShareStore != nil {
		shared, total, err := h.deps.ShareStore.ListSharedWithUser(ctx, user.UserID, user.Email, worklistTargetCap, 0)
		if err != nil {
			return nil, fmt.Errorf("listing shared assets: %w", err)
		}
		warnIfTruncated(total, "shared assets", user.UserID)
		for _, s := range shared {
			if keepShare(s.Permission) {
				ids = append(ids, s.Asset.ID)
			}
		}
	}
	return ids, nil
}

// gatherCollectionIDs returns the collections the user owns plus the shared
// collections whose permission satisfies keepShare.
func (h *Handler) gatherCollectionIDs(ctx context.Context, user *User, keepShare func(SharePermission) bool) ([]string, error) {
	var ids []string
	if h.deps.CollectionStore != nil {
		owned, total, err := h.deps.CollectionStore.List(ctx, CollectionFilter{OwnerID: user.UserID, Limit: worklistTargetCap})
		if err != nil {
			return nil, fmt.Errorf("listing owned collections: %w", err)
		}
		warnIfTruncated(total, "owned collections", user.UserID)
		for _, c := range owned {
			ids = append(ids, c.ID)
		}
	}
	if h.deps.ShareStore != nil {
		shared, total, err := h.deps.ShareStore.ListSharedCollectionsWithUser(ctx, user.UserID, user.Email, worklistTargetCap, 0)
		if err != nil {
			return nil, fmt.Errorf("listing shared collections: %w", err)
		}
		warnIfTruncated(total, "shared collections", user.UserID)
		for _, s := range shared {
			if keepShare(s.Permission) {
				ids = append(ids, s.Collection.ID)
			}
		}
	}
	return ids, nil
}

// worklistTargetCap bounds how many owned/shared artifacts the worklist gathers.
const worklistTargetCap = 1000

// warnIfTruncated logs when a user's owned/shared set exceeds the worklist cap,
// so the silent omission of artifacts past the cap is at least observable to
// operators (the worklist's purpose is that no feedback is dropped).
func warnIfTruncated(total int, kind, userID string) {
	if total > worklistTargetCap {
		slog.Warn("worklist target set truncated; some artifacts are omitted",
			"kind", kind, "user", userID, "total", total, "cap", worklistTargetCap)
	}
}
