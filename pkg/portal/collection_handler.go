package portal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Common error messages for collection handlers.
const (
	errCollectionNotFound = "collection not found"
	errCollectionDeleted  = "collection has been deleted"
)

// --- Create Collection ---

type createCollectionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	var req createCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	Data           []Collection           `json:"data"`
	Total          int                    `json:"total"`
	Limit          int                    `json:"limit"`
	Offset         int                    `json:"offset"`
	ShareSummaries map[string]ShareSummary `json:"share_summaries,omitempty"`
}

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
	IsOwner         bool            `json:"is_owner"`
	SharePermission SharePermission `json:"share_permission,omitempty"`
}

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
	shares, err := h.deps.ShareStore.ListByCollection(r.Context(), collectionID)
	if err != nil {
		return ""
	}

	var best SharePermission
	for _, share := range shares {
		if share.Revoked {
			continue
		}
		if share.ExpiresAt != nil && share.ExpiresAt.Before(time.Now()) {
			continue
		}
		if share.SharedWithUserID == user.UserID || (share.SharedWithEmail != "" && share.SharedWithEmail == user.Email) {
			if share.Permission == PermissionEditor {
				return PermissionEditor
			}
			best = PermissionViewer
		}
	}
	return best
}

// --- Update Collection ---

type updateCollectionRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

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
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := coll.Name
	if req.Name != nil {
		name = *req.Name
		if err := ValidateCollectionName(name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	description := coll.Description
	if req.Description != nil {
		description = *req.Description
		if err := ValidateCollectionDescription(description); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
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

// --- Delete Collection ---

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
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Items       []itemInput `json:"items"`
}

type itemInput struct {
	AssetID string `json:"asset_id"`
}

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
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Convert to domain types, assigning IDs and validating.
	sections := make([]CollectionSection, len(req.Sections))
	for i, s := range req.Sections {
		if err := ValidateSectionTitle(s.Title); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("section %d: %s", i, err.Error()))
			return
		}
		if err := ValidateSectionDescription(s.Description); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("section %d: %s", i, err.Error()))
			return
		}

		items := make([]CollectionItem, len(s.Items))
		for j, item := range s.Items {
			if item.AssetID == "" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("section %d, item %d: asset_id is required", i, j))
				return
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
		writeError(w, http.StatusBadRequest, err.Error())
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

// --- Collection Thumbnail ---

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
	_, _ = w.Write(data)
}

// --- Collection Sharing ---

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
		writeError(w, http.StatusBadRequest, "invalid request body")
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
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

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
