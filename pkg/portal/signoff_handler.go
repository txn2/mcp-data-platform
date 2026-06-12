package portal

import (
	"net/http"
	"strings"
)

// Sign-off aggregation (Phase 3 / #603). "Signed off by N of M stakeholders",
// where N is the number of distinct users who left an approval event on the
// artifact's threads and M is the artifact owner plus its active share grantees.

// signoffSummary is the response for an artifact's sign-off aggregation.
type signoffSummary struct {
	SignedOff    int `json:"signed_off"`
	Stakeholders int `json:"stakeholders"`
}

// assetSignoff handles GET /api/v1/portal/assets/{id}/signoff.
//
// @Summary      Asset sign-off summary
// @Description  Count of stakeholders who signed off (N) out of the total stakeholders (M = owner + active share grantees).
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  signoffSummary
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/signoff [get]
func (h *Handler) assetSignoff(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	id := r.PathValue("id")
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		writeError(w, http.StatusNotFound, "asset not found")
		return
	}
	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}
	shares, err := h.deps.ShareStore.ListByAsset(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load shares")
		return
	}
	h.writeSignoff(w, r, targetTypeAsset, id, stakeholderCount(asset.OwnerID, asset.OwnerEmail, shares))
}

// collectionSignoff handles GET /api/v1/portal/collections/{id}/signoff.
//
// @Summary      Collection sign-off summary
// @Description  Count of stakeholders who signed off (N) out of the total stakeholders (M = owner + active share grantees).
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  signoffSummary
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/signoff [get]
func (h *Handler) collectionSignoff(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	id := r.PathValue("id")
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil || coll == nil || coll.DeletedAt != nil {
		writeError(w, http.StatusNotFound, "collection not found")
		return
	}
	if coll.OwnerID != user.UserID && !h.userIsAdmin(user) && h.collectionSharePermission(r, id, user) == "" {
		writeError(w, http.StatusForbidden, "you do not have access to this collection")
		return
	}
	shares, err := h.deps.ShareStore.ListByCollection(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load shares")
		return
	}
	h.writeSignoff(w, r, targetTypeCollection, id, stakeholderCount(coll.OwnerID, coll.OwnerEmail, shares))
}

// writeSignoff counts sign-offs and writes the N-of-M summary. signed_off is
// clamped to the stakeholder count (m) so the badge can never read "N of M"
// with N > M (an approval from someone outside owner+grantees, e.g. a
// collection-inherited viewer, must not over-report).
func (h *Handler) writeSignoff(w http.ResponseWriter, r *http.Request, targetType, id string, m int) {
	n, err := h.deps.ThreadStore.CountSignoffs(r.Context(), targetType, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count sign-offs")
		return
	}
	if n > m {
		n = m
	}
	writeJSON(w, http.StatusOK, signoffSummary{SignedOff: n, Stakeholders: m})
}

// stakeholderCount returns M: the artifact owner (always 1) plus the number of
// distinct users holding an active share, excluding the owner themselves — by
// id OR email, since a self-share may be recorded by either (an owner who
// self-shares must not be counted twice).
func stakeholderCount(ownerID, ownerEmail string, shares []Share) int {
	ownerKey := strings.ToLower(ownerEmail)
	seen := make(map[string]struct{}, len(shares))
	for _, s := range shares {
		if !isShareActive(s) {
			continue
		}
		key := s.SharedWithUserID
		if key == "" {
			key = strings.ToLower(s.SharedWithEmail)
		}
		if key == "" || key == ownerID || (ownerKey != "" && key == ownerKey) {
			continue
		}
		seen[key] = struct{}{}
	}
	return 1 + len(seen)
}
