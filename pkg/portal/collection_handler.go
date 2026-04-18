package portal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// Common error messages for collection handlers.
const (
	errCollectionNotFound = "collection not found"
	errCollectionDeleted  = "collection has been deleted"
	errInvalidRequestBody = "invalid request body"
)

// --- Create Collection ---

type createCollectionRequest struct {
	Name        string `json:"name" example:"Q4 Analysis"`
	Description string `json:"description,omitempty" example:"Quarterly analysis collection"`
}

// createCollection handles POST /api/v1/portal/collections.
//
// @Summary      Create collection
// @Description  Creates a new collection for the current user.
// @Tags         Collections
// @Accept       json
// @Produce      json
// @Param        body  body  createCollectionRequest  true  "Collection details"
// @Success      201  {object}  Collection
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections [post]
func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	var req createCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	if err := ValidateCollectionName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateCollectionDescription(req.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	coll := Collection{
		ID:          uuid.New().String(),
		OwnerID:     user.UserID,
		OwnerEmail:  user.Email,
		Name:        req.Name,
		Description: req.Description,
	}

	if err := h.deps.CollectionStore.Insert(r.Context(), coll); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collection")
		return
	}

	// Re-read to get server-generated timestamps.
	created, err := h.deps.CollectionStore.Get(r.Context(), coll.ID)
	if err != nil {
		writeJSON(w, http.StatusCreated, coll)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// --- List Collections ---

type listCollectionsResponse struct {
	Data           []Collection            `json:"data"`
	Total          int                     `json:"total" example:"10"`
	Limit          int                     `json:"limit" example:"20"`
	Offset         int                     `json:"offset" example:"0"`
	ShareSummaries map[string]ShareSummary `json:"share_summaries,omitempty"`
}

// listCollections handles GET /api/v1/portal/collections.
//
// @Summary      List collections
// @Description  Returns paginated collections owned by the current user.
// @Tags         Collections
// @Produce      json
// @Param        search  query  string   false  "Search term"
// @Param        limit   query  integer  false  "Results per page (default: 20)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  listCollectionsResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections [get]
func (h *Handler) listCollections(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	filter := CollectionFilter{OwnerID: user.UserID}
	if v := r.URL.Query().Get("search"); v != "" {
		filter.Search = v
	}
	if v := r.URL.Query().Get(paramLimit); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := r.URL.Query().Get(paramOffset); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Offset = n
		}
	}

	collections, total, err := h.deps.CollectionStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list collections")
		return
	}

	if collections == nil {
		collections = []Collection{}
	}

	// Fetch share summaries for the listed collections.
	ids := make([]string, len(collections))
	for i, c := range collections {
		ids[i] = c.ID
	}
	summaries, _ := h.deps.ShareStore.ListActiveCollectionShareSummaries(r.Context(), ids)

	writeJSON(w, http.StatusOK, listCollectionsResponse{
		Data:           collections,
		Total:          total,
		Limit:          filter.EffectiveLimit(),
		Offset:         filter.Offset,
		ShareSummaries: summaries,
	})
}

// --- Get Collection ---

type getCollectionResponse struct {
	Collection
	IsOwner         bool            `json:"is_owner" example:"true"`
	SharePermission SharePermission `json:"share_permission,omitempty" example:"viewer"`
}

// getCollection handles GET /api/v1/portal/collections/{id}.
//
// @Summary      Get collection
// @Description  Returns a single collection by ID. Non-owners need share access.
// @Tags         Collections
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  getCollectionResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id} [get]
func (h *Handler) getCollection(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.DeletedAt != nil {
		writeError(w, http.StatusGone, errCollectionDeleted)
		return
	}

	isOwner := coll.OwnerID == user.UserID

	// If not owner, check if the user has share access.
	var perm SharePermission
	if !isOwner {
		perm = h.collectionSharePermission(r, id, user)
		if perm == "" {
			writeError(w, http.StatusForbidden, errAccessDenied)
			return
		}
	}

	if coll.Sections == nil {
		coll.Sections = []CollectionSection{}
	}
	for i := range coll.Sections {
		if coll.Sections[i].Items == nil {
			coll.Sections[i].Items = []CollectionItem{}
		}
	}

	writeJSON(w, http.StatusOK, getCollectionResponse{
		Collection:      *coll,
		IsOwner:         isOwner,
		SharePermission: perm,
	})
}

// collectionSharePermission returns the highest share permission for a user on a collection.
func (h *Handler) collectionSharePermission(r *http.Request, collectionID string, user *User) SharePermission {
	perm, err := h.deps.ShareStore.GetUserCollectionPermission(r.Context(), collectionID, user.UserID, user.Email)
	if err != nil {
		return ""
	}
	return perm
}

// --- Update Collection ---

type updateCollectionRequest struct {
	Name        *string `json:"name,omitempty" example:"Updated Collection Name"`
	Description *string `json:"description,omitempty" example:"Updated description"`
}

// updateCollection handles PUT /api/v1/portal/collections/{id}.
//
// @Summary      Update collection
// @Description  Updates a collection's name and/or description. Only the owner can update.
// @Tags         Collections
// @Accept       json
// @Produce      json
// @Param        id    path  string                    true  "Collection ID"
// @Param        body  body  updateCollectionRequest    true  "Fields to update"
// @Success      200  {object}  Collection
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id} [put]
func (h *Handler) updateCollection(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can update this collection")
		return
	}

	var req updateCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	name, description, valErr := resolveCollectionUpdate(coll, req)
	if valErr != nil {
		writeError(w, http.StatusBadRequest, valErr.Error())
		return
	}

	if err := h.deps.CollectionStore.Update(r.Context(), id, name, description); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update collection")
		return
	}

	updated, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch updated collection")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// resolveCollectionUpdate merges and validates the update request fields with the existing collection.
func resolveCollectionUpdate(coll *Collection, req updateCollectionRequest) (resolvedName, resolvedDesc string, err error) {
	resolvedName = coll.Name
	if req.Name != nil {
		resolvedName = *req.Name
		if err := ValidateCollectionName(resolvedName); err != nil {
			return "", "", err
		}
	}

	resolvedDesc = coll.Description
	if req.Description != nil {
		resolvedDesc = *req.Description
		if err := ValidateCollectionDescription(resolvedDesc); err != nil {
			return "", "", err
		}
	}

	return resolvedName, resolvedDesc, nil
}

// --- Delete Collection ---

// deleteCollection handles DELETE /api/v1/portal/collections/{id}.
//
// @Summary      Delete collection
// @Description  Soft-deletes a collection. Only the owner can delete.
// @Tags         Collections
// @Param        id  path  string  true  "Collection ID"
// @Success      204
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id} [delete]
func (h *Handler) deleteCollection(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can delete this collection")
		return
	}

	if err := h.deps.CollectionStore.SoftDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete collection")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Update Config ---

// updateCollectionConfig handles PUT /api/v1/portal/collections/{id}/config.
//
// @Summary      Update collection config
// @Description  Updates the collection's display configuration. Only the owner can update.
// @Tags         Collections
// @Accept       json
// @Produce      json
// @Param        id    path  string            true  "Collection ID"
// @Param        body  body  CollectionConfig  true  "Configuration object"
// @Success      200  {object}  Collection
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/config [put]
func (h *Handler) updateCollectionConfig(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can update collection settings")
		return
	}

	var config CollectionConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	if err := h.deps.CollectionStore.UpdateConfig(r.Context(), id, config); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update config")
		return
	}

	updated, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch updated collection")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// --- Set Sections ---

type setSectionsRequest struct {
	Sections []sectionInput `json:"sections"`
}

type sectionInput struct {
	Title       string      `json:"title" example:"Key Findings"`
	Description string      `json:"description,omitempty" example:"Summary of key findings"`
	Items       []itemInput `json:"items"`
}

type itemInput struct {
	AssetID string `json:"asset_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// setCollectionSections handles PUT /api/v1/portal/collections/{id}/sections.
//
// @Summary      Set collection sections
// @Description  Replaces all sections and items in a collection. Only the owner can modify.
// @Tags         Collections
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "Collection ID"
// @Param        body  body  setSectionsRequest  true  "Sections with items"
// @Success      200  {object}  Collection
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/sections [put]
func (h *Handler) setCollectionSections(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can modify sections")
		return
	}

	var req setSectionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	sections, convErr := convertSectionInputs(req.Sections)
	if convErr != nil {
		writeError(w, http.StatusBadRequest, convErr.Error())
		return
	}

	if err := h.deps.CollectionStore.SetSections(r.Context(), id, sections); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update sections")
		return
	}

	// Return the updated collection.
	updated, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch updated collection")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// convertSectionInputs validates and converts section inputs to domain types.
func convertSectionInputs(inputs []sectionInput) ([]CollectionSection, error) {
	sections := make([]CollectionSection, len(inputs))
	for i, s := range inputs {
		if err := ValidateSectionTitle(s.Title); err != nil {
			return nil, fmt.Errorf("section %d: %s", i, err.Error())
		}
		if err := ValidateSectionDescription(s.Description); err != nil {
			return nil, fmt.Errorf("section %d: %s", i, err.Error())
		}

		items := make([]CollectionItem, len(s.Items))
		for j, item := range s.Items {
			if item.AssetID == "" {
				return nil, fmt.Errorf("section %d, item %d: asset_id is required", i, j)
			}
			items[j] = CollectionItem{
				ID:      uuid.New().String(),
				AssetID: item.AssetID,
			}
		}

		sections[i] = CollectionSection{
			ID:          uuid.New().String(),
			Title:       s.Title,
			Description: s.Description,
			Items:       items,
		}
	}

	// Validate totals.
	if err := ValidateSections(sections); err != nil {
		return nil, err
	}

	return sections, nil
}

// --- Collection Thumbnail ---

// uploadCollectionThumbnail handles PUT /api/v1/portal/collections/{id}/thumbnail.
//
// @Summary      Upload collection thumbnail
// @Description  Uploads a PNG thumbnail image for the collection. Only the owner can upload.
// @Tags         Collections
// @Accept       png
// @Produce      json
// @Param        id    path  string  true  "Collection ID"
// @Param        body  body  []byte  true  "PNG image data"
// @Success      204
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/thumbnail [put]
func (h *Handler) uploadCollectionThumbnail(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can upload a thumbnail")
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, MaxThumbnailUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read upload")
		return
	}
	if int64(len(data)) > MaxThumbnailUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "thumbnail too large")
		return
	}

	s3Key := fmt.Sprintf("portal/collections/%s/thumbnail.png", id)
	if err := h.deps.S3Client.PutObject(r.Context(), h.deps.S3Bucket, s3Key, data, "image/png"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to upload thumbnail")
		return
	}

	if err := h.deps.CollectionStore.UpdateThumbnail(r.Context(), id, s3Key); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update thumbnail reference")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// getCollectionThumbnail handles GET /api/v1/portal/collections/{id}/thumbnail.
//
// @Summary      Get collection thumbnail
// @Description  Downloads the collection's PNG thumbnail image.
// @Tags         Collections
// @Produce      png
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {file}  binary
// @Failure      401  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/thumbnail [get]
func (h *Handler) getCollectionThumbnail(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil || coll.ThumbnailS3Key == "" {
		writeError(w, http.StatusNotFound, "thumbnail not found")
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	data, contentType, err := h.deps.S3Client.GetObject(r.Context(), h.deps.S3Bucket, coll.ThumbnailS3Key)
	if err != nil {
		writeError(w, http.StatusNotFound, "thumbnail not found")
		return
	}

	w.Header().Set(headerContentType, contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served from S3, content-type set by uploader
}

// --- Collection Sharing ---

// createCollectionShare handles POST /api/v1/portal/collections/{id}/shares.
//
// @Summary      Create collection share
// @Description  Creates a share link or user-targeted share for a collection. Only the owner can share.
// @Tags         Shares
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "Collection ID"
// @Param        body  body  createShareRequest  true  "Share configuration"
// @Success      201  {object}  shareResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/shares [post]
func (h *Handler) createCollectionShare(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can share this collection")
		return
	}

	var req createShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	share, buildErr := buildShare(shareTarget{CollectionID: id}, user.Email, req)
	if buildErr != nil {
		writeError(w, http.StatusBadRequest, buildErr.Error())
		return
	}

	if err := h.deps.ShareStore.Insert(r.Context(), share); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create share")
		return
	}

	resp := shareResponse{Share: share}
	if h.deps.PublicBaseURL != "" {
		resp.ShareURL = fmt.Sprintf("%s/portal/view/%s", h.deps.PublicBaseURL, share.Token)
	}

	writeJSON(w, http.StatusCreated, resp)
}

// listCollectionShares handles GET /api/v1/portal/collections/{id}/shares.
//
// @Summary      List collection shares
// @Description  Returns all shares for a collection. Only the owner can view shares.
// @Tags         Shares
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {array}   Share
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/shares [get]
func (h *Handler) listCollectionShares(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	id := r.PathValue(pathKeyID)
	coll, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errCollectionNotFound)
		return
	}
	if coll.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can view shares for this collection")
		return
	}

	shares, err := h.deps.ShareStore.ListByCollection(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list shares")
		return
	}

	if shares == nil {
		shares = []Share{}
	}
	writeJSON(w, http.StatusOK, shares)
}

// --- Shared Collections With Me ---

type listSharedCollectionsResponse struct {
	Data   []SharedCollection `json:"data"`
	Total  int                `json:"total" example:"5"`
	Limit  int                `json:"limit" example:"20"`
	Offset int                `json:"offset" example:"0"`
}

// listSharedCollections handles GET /api/v1/portal/shared-collections.
//
// @Summary      List collections shared with me
// @Description  Returns paginated collections that other users have shared with the current user.
// @Tags         Shares
// @Produce      json
// @Param        limit   query  integer  false  "Results per page (default: 20)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  listSharedCollectionsResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/shared-collections [get]
func (h *Handler) listSharedCollections(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	limit := defaultLimit
	if v := r.URL.Query().Get(paramLimit); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	var offset int
	if v := r.URL.Query().Get(paramOffset); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			offset = n
		}
	}

	collections, total, err := h.deps.ShareStore.ListSharedCollectionsWithUser(r.Context(), user.UserID, user.Email, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list shared collections")
		return
	}

	if collections == nil {
		collections = []SharedCollection{}
	}

	writeJSON(w, http.StatusOK, listSharedCollectionsResponse{
		Data:   collections,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}
