package portal

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// provenancePageResponse is one page of an asset's captures, newest first.
//
// Total is how many the asset holds, so a reader that has taken Offset+len(Captures)
// of them knows whether another page follows.
type provenancePageResponse struct {
	Captures []ProvenanceCapture `json:"captures"`
	Total    int                 `json:"total" example:"329"`
	Offset   int                 `json:"offset" example:"20"`
	Limit    int                 `json:"limit" example:"20"`
}

// listAssetProvenance handles GET /api/v1/portal/assets/{id}/provenance.
//
// @Summary      Page an asset's provenance
// @Description  Returns one page of the asset's provenance captures, newest first. A single asset read carries only the newest captures inline; this is how the rest are reached.
// @Tags         Assets
// @Produce      json
// @Param        id      path   string   true   "Asset ID"
// @Param        offset  query  integer  false  "Captures to skip, counting back from the newest (default: 0)"
// @Param        limit   query  integer  false  "Captures per page (default: 20, max: 100)"
// @Success      200  {object}  provenancePageResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/provenance [get]
func (h *Handler) listAssetProvenance(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return
	}
	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAssetDeleted)
		return
	}
	// The page is part of the asset's record, so it is authorized exactly as
	// the asset is: its owner, or whoever holds a share that reaches it.
	if !access.OwnsAsset(asset, user) {
		perm, permErr := h.access.ResolveAssetPermission(r.Context(), id, user)
		if permErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to check share access")
			return
		}
		if perm == "" {
			writeError(w, http.StatusForbidden, errAccessDenied)
			return
		}
	}

	offset, limit := portaldomain.ClampProvenancePage(
		intParam(r, paramOffset, 0), intParam(r, paramLimit, portaldomain.DefaultProvenancePageSize))
	captures, total, err := h.deps.AssetStore.ListProvenanceCaptures(r.Context(), id, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read provenance")
		return
	}
	if captures == nil {
		captures = []ProvenanceCapture{}
	}

	writeJSON(w, http.StatusOK, provenancePageResponse{
		Captures: captures, Total: total, Offset: offset, Limit: limit,
	})
}
