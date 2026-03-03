package portal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Common error messages and path value keys used across handlers.
const (
	errAuthRequired  = "authentication required"
	errAssetNotFound = "asset not found"
	pathKeyID        = "id"
)

// Deps holds dependencies for the portal handler.
type Deps struct {
	AssetStore    AssetStore
	ShareStore    ShareStore
	S3Client      S3Client
	S3Bucket      string
	PublicBaseURL string
	RateLimit     RateLimitConfig
}

// Handler provides portal REST API endpoints.
type Handler struct {
	mux         *http.ServeMux
	publicMux   *http.ServeMux
	deps        Deps
	authMiddle  func(http.Handler) http.Handler
	rateLimiter *RateLimiter
}

// NewHandler creates a new portal API handler.
func NewHandler(deps Deps, authMiddle func(http.Handler) http.Handler) *Handler {
	h := &Handler{
		mux:         http.NewServeMux(),
		publicMux:   http.NewServeMux(),
		deps:        deps,
		authMiddle:  authMiddle,
		rateLimiter: NewRateLimiter(deps.RateLimit),
	}
	h.registerRoutes()
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/portal/view/") {
		h.publicMux.ServeHTTP(w, r)
		return
	}
	if h.authMiddle != nil {
		h.authMiddle(h.mux).ServeHTTP(w, r)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	// Authenticated routes
	h.mux.HandleFunc("GET /api/v1/portal/assets", h.listAssets)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}", h.getAsset)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/content", h.getAssetContent)
	h.mux.HandleFunc("PUT /api/v1/portal/assets/{id}", h.updateAsset)
	h.mux.HandleFunc("DELETE /api/v1/portal/assets/{id}", h.deleteAsset)
	h.mux.HandleFunc("POST /api/v1/portal/assets/{id}/shares", h.createShare)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/shares", h.listShares)
	h.mux.HandleFunc("DELETE /api/v1/portal/shares/{id}", h.revokeShare)
	h.mux.HandleFunc("GET /api/v1/portal/shared-with-me", h.listSharedWithMe)

	// Public route (rate limited)
	h.publicMux.Handle("GET /portal/view/{token}",
		h.rateLimiter.Middleware(http.HandlerFunc(h.publicView)))
}

// --- Asset handlers ---

// paginatedResponse wraps paginated results.
type paginatedResponse struct {
	Data   any `json:"data"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func (h *Handler) listAssets(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	filter := AssetFilter{
		OwnerID:     user.UserID,
		ContentType: r.URL.Query().Get("content_type"),
		Tag:         r.URL.Query().Get("tag"),
		Limit:       intParam(r, "limit", defaultLimit),
		Offset:      intParam(r, "offset", 0),
	}

	assets, total, err := h.deps.AssetStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list assets")
		return
	}

	if assets == nil {
		assets = []Asset{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: assets, Total: total,
		Limit: filter.EffectiveLimit(), Offset: filter.Offset,
	})
}

func (h *Handler) getAsset(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusGone, "asset has been deleted")
		return
	}

	// Owner can always access; also check if shared with user.
	if asset.OwnerID != user.UserID {
		if !h.isSharedWithUser(r, id, user.UserID) {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}
	}

	writeJSON(w, http.StatusOK, asset)
}

func (h *Handler) getAssetContent(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusGone, "asset has been deleted")
		return
	}

	if asset.OwnerID != user.UserID {
		if !h.isSharedWithUser(r, id, user.UserID) {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}
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
	_, _ = w.Write(data) // #nosec G705 -- content served with explicit Content-Type, not rendered as HTML
}

// updateAssetRequest is the request body for updating an asset.
type updateAssetRequest struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func (h *Handler) updateAsset(w http.ResponseWriter, r *http.Request) {
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
	if asset.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can update this asset")
		return
	}

	var req updateAssetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updates := AssetUpdate{
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
	}

	if err := validateUpdateRequest(updates); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.deps.AssetStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update asset")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

func (h *Handler) deleteAsset(w http.ResponseWriter, r *http.Request) {
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
	if asset.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can delete this asset")
		return
	}

	if err := h.deps.AssetStore.SoftDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete asset")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}

// --- Share handlers ---

// createShareRequest is the request body for creating a share.
type createShareRequest struct {
	ExpiresIn        string `json:"expires_in,omitempty"` // duration string, e.g. "24h"
	SharedWithUserID string `json:"shared_with_user_id,omitempty"`
}

// shareResponse is the response for a created share.
type shareResponse struct {
	Share    Share  `json:"share"`
	ShareURL string `json:"share_url,omitempty"`
}

func (h *Handler) createShare(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	assetID := r.PathValue(pathKeyID)
	asset, err := h.deps.AssetStore.Get(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return
	}
	if asset.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can share this asset")
		return
	}

	var req createShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token, err := generateToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate share token")
		return
	}

	share := Share{
		ID:               uuid.New().String(),
		AssetID:          assetID,
		Token:            token,
		CreatedBy:        user.UserID,
		SharedWithUserID: req.SharedWithUserID,
	}

	if req.ExpiresIn != "" {
		dur, parseErr := time.ParseDuration(req.ExpiresIn)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in duration")
			return
		}
		exp := time.Now().Add(dur)
		share.ExpiresAt = &exp
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

func (h *Handler) listShares(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	assetID := r.PathValue(pathKeyID)
	asset, err := h.deps.AssetStore.Get(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return
	}
	if asset.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can view shares for this asset")
		return
	}

	shares, err := h.deps.ShareStore.ListByAsset(r.Context(), assetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list shares")
		return
	}

	if shares == nil {
		shares = []Share{}
	}
	writeJSON(w, http.StatusOK, shares)
}

func (h *Handler) revokeShare(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	shareID := r.PathValue(pathKeyID)
	share, err := h.deps.ShareStore.GetByID(r.Context(), shareID)
	if err != nil {
		writeError(w, http.StatusNotFound, "share not found")
		return
	}

	// Verify ownership: check that the share's asset belongs to this user.
	asset, err := h.deps.AssetStore.Get(r.Context(), share.AssetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "associated asset not found")
		return
	}
	if asset.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can revoke this share")
		return
	}

	if err := h.deps.ShareStore.Revoke(r.Context(), shareID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke share")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "revoked"})
}

func (h *Handler) listSharedWithMe(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	limit := intParam(r, "limit", defaultLimit)
	offset := intParam(r, "offset", 0)

	shared, total, err := h.deps.ShareStore.ListSharedWithUser(r.Context(), user.UserID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list shared assets")
		return
	}

	if shared == nil {
		shared = []SharedAsset{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: shared, Total: total, Limit: limit, Offset: offset,
	})
}

// --- Helpers ---

// statusResponse is a generic status response.
type statusResponse struct {
	Status string `json:"status"`
}

// validateUpdateRequest validates the fields in an update request.
func validateUpdateRequest(updates AssetUpdate) error {
	if updates.Name != nil {
		if err := ValidateAssetName(*updates.Name); err != nil {
			return err
		}
	}
	if updates.Description != nil {
		if err := ValidateDescription(*updates.Description); err != nil {
			return err
		}
	}
	if updates.Tags != nil {
		if err := ValidateTags(updates.Tags); err != nil {
			return err
		}
	}
	return nil
}

func intParam(r *http.Request, name string, fallback int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// tokenBytes is the number of random bytes used for share tokens (256 bits).
const tokenBytes = 32

func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// isSharedWithUser checks whether an asset has been shared with a specific user.
func (h *Handler) isSharedWithUser(r *http.Request, assetID, userID string) bool {
	shares, err := h.deps.ShareStore.ListByAsset(r.Context(), assetID)
	if err != nil {
		return false
	}
	for _, s := range shares {
		if s.SharedWithUserID == userID && !s.Revoked {
			if s.ExpiresAt == nil || s.ExpiresAt.After(time.Now()) {
				return true
			}
		}
	}
	return false
}
