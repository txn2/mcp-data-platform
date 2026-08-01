package feedbackapi

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/access"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
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
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/signoff [get]
func (h *Handler) assetSignoff(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	id := r.PathValue("id")
	asset, err := h.cfg.Assets.Get(r.Context(), id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, "asset not found")
		return
	}
	if !h.assetViewable(w, r, id, asset, user) {
		return
	}
	shares, err := h.cfg.Shares.ListByAsset(r.Context(), id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to load shares")
		return
	}
	h.writeSignoff(w, r, portaldomain.TargetTypeAsset, id, stakeholderCount(asset.OwnerID, asset.OwnerEmail, shares))
}

// collectionSignoff handles GET /api/v1/portal/collections/{id}/signoff.
//
// @Summary      Collection sign-off summary
// @Description  Count of stakeholders who signed off (N) out of the total stakeholders (M = owner + active share grantees).
// @Tags         Feedback
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  signoffSummary
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/signoff [get]
func (h *Handler) collectionSignoff(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	id := r.PathValue("id")
	coll, err := h.cfg.Collections.Get(r.Context(), id)
	if err != nil || coll == nil || coll.DeletedAt != nil {
		httpjson.WriteError(w, http.StatusNotFound, "collection not found")
		return
	}
	if coll.OwnerID != user.UserID && !h.access.IsAdmin(user) && h.access.CollectionSharePermission(r.Context(), id, user) == "" {
		httpjson.WriteError(w, http.StatusForbidden, "you do not have access to this collection")
		return
	}
	shares, err := h.cfg.Shares.ListByCollection(r.Context(), id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to load shares")
		return
	}
	h.writeSignoff(w, r, portaldomain.TargetTypeCollection, id, stakeholderCount(coll.OwnerID, coll.OwnerEmail, shares))
}

// writeSignoff counts sign-offs and writes the N-of-M summary. signed_off is
// clamped to the stakeholder count (m) so the badge can never read "N of M"
// with N > M (an approval from someone outside owner+grantees, e.g. a
// collection-inherited viewer, must not over-report).
func (h *Handler) writeSignoff(w http.ResponseWriter, r *http.Request, targetType, id string, m int) {
	n, err := h.cfg.Threads.CountSignoffs(r.Context(), targetType, id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to count sign-offs")
		return
	}
	if n > m {
		n = m
	}
	httpjson.WriteJSON(w, http.StatusOK, signoffSummary{SignedOff: n, Stakeholders: m})
}

// stakeholderCount returns M: the artifact owner (always 1) plus the number of
// distinct users holding an active share, excluding the owner themselves — by
// id OR email, since a self-share may be recorded by either (an owner who
// self-shares must not be counted twice).
func stakeholderCount(ownerID, ownerEmail string, shares []portaldomain.Share) int {
	ownerKey := strings.ToLower(ownerEmail)
	seen := make(map[string]struct{}, len(shares))
	for _, s := range shares {
		if !access.IsShareActive(s) {
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
