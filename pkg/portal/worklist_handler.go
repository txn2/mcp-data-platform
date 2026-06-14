package portal

import (
	"context"
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
// scope). Delegates to the shared gather helpers; the activity feed uses the
// same helpers with KeepAnyShare for its broader "I can view it" scope.
func (h *Handler) ownedOrEditableTargets(ctx context.Context, user *User) (assetIDs, collIDs []string, err error) {
	g := h.targetGatherer(user)
	assetIDs, err = g.AssetIDs(ctx, KeepEditorShares)
	if err != nil {
		return nil, nil, err
	}
	collIDs, err = g.CollectionIDs(ctx, KeepEditorShares)
	if err != nil {
		return nil, nil, err
	}
	return assetIDs, collIDs, nil
}

// targetGatherer builds a TargetGatherer from the handler's stores for the user.
func (h *Handler) targetGatherer(user *User) TargetGatherer {
	return TargetGatherer{
		Assets:      h.deps.AssetStore,
		Collections: h.deps.CollectionStore,
		Shares:      h.deps.ShareStore,
		UserID:      user.UserID,
		Email:       user.Email,
	}
}
