package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// maxContentUploadBytes is the maximum size for content uploads (10 MB).
const maxContentUploadBytes = 10 << 20

// pathValueID is the URL path parameter name for asset identifiers.
const pathValueID = "id"

// errAdminAssetNotFound is the error message for missing assets.
const errAdminAssetNotFound = "asset not found"

// registerAssetRoutes registers asset management routes if stores are available.
func (h *Handler) registerAssetRoutes() {
	if h.deps.AssetStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/assets", h.listAllAssets)
	h.mux.HandleFunc("GET /api/v1/admin/assets/{id}", h.getAdminAsset)
	h.mux.HandleFunc("GET /api/v1/admin/assets/{id}/content", h.getAdminAssetContent)
	h.mux.HandleFunc("PUT /api/v1/admin/assets/{id}", h.updateAdminAsset)
	h.mux.HandleFunc("PUT /api/v1/admin/assets/{id}/content", h.updateAdminAssetContent)
	h.mux.HandleFunc("DELETE /api/v1/admin/assets/{id}", h.deleteAdminAsset)
}

// listAllAssets returns all platform assets (no owner filter).
func (h *Handler) listAllAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := portal.AssetFilter{
		Search: q.Get("search"),
		Limit:  limit,
		Offset: offset,
	}

	assets, total, err := h.deps.AssetStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list assets")
		return
	}
	if assets == nil {
		assets = []portal.Asset{}
	}

	var summaries map[string]portal.ShareSummary
	if len(assets) > 0 && h.deps.ShareStore != nil {
		ids := make([]string, len(assets))
		for i, a := range assets {
			ids[i] = a.ID
		}
		summaries, _ = h.deps.ShareStore.ListActiveShareSummaries(r.Context(), ids)
	}

	writeJSON(w, http.StatusOK, adminAssetListResponse{
		Data:           assets,
		Total:          total,
		Limit:          filter.EffectiveLimit(),
		Offset:         filter.Offset,
		ShareSummaries: summaries,
	})
}

// adminAssetListResponse is the paginated response for admin asset listing.
type adminAssetListResponse struct {
	Data           []portal.Asset                 `json:"data"`
	Total          int                            `json:"total"`
	Limit          int                            `json:"limit"`
	Offset         int                            `json:"offset"`
	ShareSummaries map[string]portal.ShareSummary `json:"share_summaries,omitempty"`
}

// getAdminAsset returns a single asset without owner restriction.
func (h *Handler) getAdminAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// getAdminAssetContent returns an asset's S3 content without owner restriction.
func (h *Handler) getAdminAssetContent(w http.ResponseWriter, r *http.Request) {
	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage not configured")
		return
	}

	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	data, contentType, err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.S3Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve content")
		return
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served with explicit Content-Type
}

// adminUpdateAssetRequest is the request body for admin asset updates.
type adminUpdateAssetRequest struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// updateAdminAsset updates any asset without owner restriction.
func (h *Handler) updateAdminAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathValueID)
	if _, err := h.deps.AssetStore.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	var req adminUpdateAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := portal.AssetUpdate{
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
	}

	if err := validateAdminAssetUpdate(updates); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.deps.AssetStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update asset")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// updateAdminAssetContent replaces an asset's S3 content (no owner check for admins).
func (h *Handler) updateAdminAssetContent(w http.ResponseWriter, r *http.Request) {
	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, "content storage not configured")
		return
	}

	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxContentUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if int64(len(data)) > maxContentUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "content exceeds 10 MB limit")
		return
	}

	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, asset.S3Key, data, asset.ContentType); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload content")
		return
	}

	updates := portal.AssetUpdate{HasContent: true, SizeBytes: int64(len(data))}
	if err := h.deps.AssetStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update asset metadata")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// deleteAdminAsset soft-deletes any asset without owner restriction.
func (h *Handler) deleteAdminAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathValueID)
	if err := h.deps.AssetStore.SoftDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "asset not found or already deleted")
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}

// validateAdminAssetUpdate validates the fields in an admin update request.
func validateAdminAssetUpdate(updates portal.AssetUpdate) error {
	if updates.Name != nil {
		if err := portal.ValidateAssetName(*updates.Name); err != nil {
			return fmt.Errorf("invalid name: %w", err)
		}
	}
	if updates.Description != nil {
		if err := portal.ValidateDescription(*updates.Description); err != nil {
			return fmt.Errorf("invalid description: %w", err)
		}
	}
	if updates.Tags != nil {
		if err := portal.ValidateTags(updates.Tags); err != nil {
			return fmt.Errorf("invalid tags: %w", err)
		}
	}
	return nil
}
