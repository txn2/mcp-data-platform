package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// pathValueID is the URL path parameter name for asset identifiers.
const pathValueID = "id"

// errAdminAssetNotFound is the error message for missing assets.
const errAdminAssetNotFound = "asset not found"

// errAdminStorageNotReady is the error message when S3 is not configured.
const errAdminStorageNotReady = "content storage not configured"

// errAdminAssetDeleted is the error message for deleted assets.
const errAdminAssetDeleted = "asset has been deleted"

// headerContentType is the HTTP Content-Type header name.
const headerContentType = "Content-Type"

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
	h.mux.HandleFunc("PUT /api/v1/admin/assets/{id}/thumbnail", h.uploadAdminThumbnail)
	h.mux.HandleFunc("GET /api/v1/admin/assets/{id}/thumbnail", h.getAdminThumbnail)
	h.mux.HandleFunc("DELETE /api/v1/admin/assets/{id}", h.deleteAdminAsset)
	h.mux.HandleFunc("GET /api/v1/admin/assets/{id}/versions", h.listAdminVersions)
	h.mux.HandleFunc("GET /api/v1/admin/assets/{id}/versions/{version}/content", h.getAdminVersionContent)
	h.mux.HandleFunc("POST /api/v1/admin/assets/{id}/versions/{version}/revert", h.revertAdminVersion)
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
		writeError(w, http.StatusServiceUnavailable, errAdminStorageNotReady)
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
	w.Header().Set(headerContentType, contentType)
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
		writeError(w, http.StatusServiceUnavailable, errAdminStorageNotReady)
		return
	}

	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAdminAssetDeleted)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, portal.MaxContentUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if int64(len(data)) > portal.MaxContentUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "content exceeds 10 MB limit")
		return
	}

	if h.deps.VersionStore == nil {
		writeError(w, http.StatusServiceUnavailable, "version tracking not available")
		return
	}

	versionID := uuid.New().String()
	ext := portal.ExtensionForContentType(asset.ContentType)
	versionedKey := fmt.Sprintf("portal/%s/%s/%s/content%s", asset.OwnerID, id, versionID, ext)

	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, versionedKey, data, asset.ContentType); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload content")
		return
	}

	av := portal.AssetVersion{
		ID:            versionID,
		AssetID:       id,
		S3Key:         versionedKey,
		S3Bucket:      asset.S3Bucket,
		ContentType:   asset.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     "admin",
		ChangeSummary: "Content updated (admin)",
	}

	if _, err := h.deps.VersionStore.CreateVersion(r.Context(), av); err != nil {
		cleanupOrphanedS3(r.Context(), h.deps.S3Client, asset.S3Bucket, versionedKey)
		writeError(w, http.StatusInternalServerError, "failed to create version")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// uploadAdminThumbnail uploads a PNG thumbnail for an asset (no owner check for admins).
func (h *Handler) uploadAdminThumbnail(w http.ResponseWriter, r *http.Request) {
	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errAdminStorageNotReady)
		return
	}

	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAdminAssetDeleted)
		return
	}

	ct := r.Header.Get(headerContentType)
	mediaType, _, _ := mime.ParseMediaType(ct)
	if mediaType != "image/png" {
		writeError(w, http.StatusBadRequest, "thumbnail must be image/png")
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, portal.MaxThumbnailUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if int64(len(data)) > portal.MaxThumbnailUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("thumbnail exceeds %d KB limit", portal.MaxThumbnailUploadBytes>>10))
		return
	}

	thumbKey := portal.DeriveThumbnailKey(asset.S3Key)
	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, thumbKey, data, "image/png"); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload thumbnail")
		return
	}

	updates := portal.AssetUpdate{ThumbnailS3Key: &thumbKey}
	if err := h.deps.AssetStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update asset metadata")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// getAdminThumbnail returns an asset's thumbnail (no owner check for admins).
func (h *Handler) getAdminThumbnail(w http.ResponseWriter, r *http.Request) {
	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errAdminStorageNotReady)
		return
	}

	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAdminAssetDeleted)
		return
	}

	if asset.ThumbnailS3Key == "" {
		writeError(w, http.StatusNotFound, "no thumbnail available")
		return
	}

	data, _, err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.ThumbnailS3Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve thumbnail")
		return
	}

	w.Header().Set(headerContentType, "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served as image/png, not rendered as HTML
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

// listAdminVersions returns version history for an asset.
func (h *Handler) listAdminVersions(w http.ResponseWriter, r *http.Request) {
	if h.deps.VersionStore == nil {
		writeJSON(w, http.StatusOK, adminVersionListResponse{Data: []portal.AssetVersion{}})
		return
	}
	id := r.PathValue(pathValueID)
	if _, err := h.deps.AssetStore.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	limit := adminIntParam(r, "limit", 0)
	offset := adminIntParam(r, "offset", 0)
	versions, total, err := h.deps.VersionStore.ListByAsset(r.Context(), id, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	if versions == nil {
		versions = []portal.AssetVersion{}
	}
	writeJSON(w, http.StatusOK, adminVersionListResponse{Data: versions, Total: total, Limit: limit, Offset: offset})
}

// adminVersionListResponse is the paginated response for version listing.
type adminVersionListResponse struct {
	Data   []portal.AssetVersion `json:"data"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

// getAdminVersionContent returns content for a specific version.
func (h *Handler) getAdminVersionContent(w http.ResponseWriter, r *http.Request) {
	if h.deps.VersionStore == nil || h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errAdminStorageNotReady)
		return
	}

	id := r.PathValue(pathValueID)
	if _, err := h.deps.AssetStore.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}

	versionNum, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version number")
		return
	}

	ver, err := h.deps.VersionStore.GetByVersion(r.Context(), id, versionNum)
	if err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}

	data, contentType, err := h.deps.S3Client.GetObject(r.Context(), ver.S3Bucket, ver.S3Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve version content")
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set(headerContentType, contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served with explicit Content-Type
}

// revertAdminVersion creates a new version by reverting to a previous version's content.
func (h *Handler) revertAdminVersion(w http.ResponseWriter, r *http.Request) {
	if h.deps.VersionStore == nil || h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errAdminStorageNotReady)
		return
	}

	id := r.PathValue(pathValueID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAdminAssetNotFound)
		return
	}
	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAdminAssetDeleted)
		return
	}

	versionNum, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version number")
		return
	}

	targetVer, err := h.deps.VersionStore.GetByVersion(r.Context(), id, versionNum)
	if err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}

	data, _, err := h.deps.S3Client.GetObject(r.Context(), targetVer.S3Bucket, targetVer.S3Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read version content")
		return
	}

	versionID := uuid.New().String()
	ext := portal.ExtensionForContentType(targetVer.ContentType)
	newKey := fmt.Sprintf("portal/%s/%s/%s/content%s", asset.OwnerID, id, versionID, ext)

	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, newKey, data, targetVer.ContentType); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload reverted content")
		return
	}

	av := portal.AssetVersion{
		ID:            versionID,
		AssetID:       id,
		S3Key:         newKey,
		S3Bucket:      asset.S3Bucket,
		ContentType:   targetVer.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     "admin",
		ChangeSummary: fmt.Sprintf("Reverted from v%d (admin)", versionNum),
	}
	assignedVersion, err := h.deps.VersionStore.CreateVersion(r.Context(), av)
	if err != nil {
		cleanupOrphanedS3(r.Context(), h.deps.S3Client, asset.S3Bucket, newKey)
		writeError(w, http.StatusInternalServerError, "failed to create revert version")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "reverted",
		"version": assignedVersion,
	})
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

// cleanupOrphanedS3 attempts to delete an S3 object that was uploaded but whose
// corresponding version record failed to persist. Errors are logged but not propagated.
func cleanupOrphanedS3(ctx context.Context, s3Client portal.S3Client, bucket, key string) {
	if s3Client == nil {
		return
	}
	if err := s3Client.DeleteObject(ctx, bucket, key); err != nil {
		slog.Warn("failed to clean up orphaned S3 object", // #nosec G706 -- structured log, not user-facing
			"bucket", bucket, "key", key, "error", err)
	}
}

// adminIntParam extracts an integer query parameter with a default value.
func adminIntParam(r *http.Request, name string, defaultVal int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}
	return v
}
