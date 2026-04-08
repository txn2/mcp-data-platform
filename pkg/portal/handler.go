package portal

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Common error messages, path value keys, and query parameter names.
const (
	errAuthRequired    = "authentication required"
	errAssetNotFound   = "asset not found"
	errAssetDeleted    = "asset has been deleted"
	errStorageNotReady = "content storage not configured"
	errAccessDenied    = "access denied"
	pathKeyID          = "id"
	paramLimit         = "limit"
	paramOffset        = "offset"
	headerContentType  = "Content-Type"
)

// defaultNoticeText is the notice shown on public shares when no custom text is provided.
const defaultNoticeText = "Proprietary & Confidential. Only share with authorized viewers."

// AuditMetrics provides aggregate audit metrics scoped to individual users.
type AuditMetrics interface {
	Timeseries(ctx context.Context, filter audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error)
	Breakdown(ctx context.Context, filter audit.BreakdownFilter) ([]audit.BreakdownEntry, error)
	Overview(ctx context.Context, filter audit.MetricsFilter) (*audit.Overview, error)
}

// InsightReader provides read-only access to user insights.
type InsightReader interface {
	List(ctx context.Context, filter knowledge.InsightFilter) ([]knowledge.Insight, int, error)
	Stats(ctx context.Context, filter knowledge.InsightFilter) (*knowledge.InsightStats, error)
}

// PersonaInfo holds resolved persona details for the current user.
type PersonaInfo struct {
	Name  string
	Tools []string // resolved tool names from Allow/Deny patterns
}

// PersonaResolver resolves a user's roles to their persona info.
type PersonaResolver func(roles []string) *PersonaInfo

// Deps holds dependencies for the portal handler.
type Deps struct {
	AssetStore      AssetStore
	ShareStore      ShareStore
	VersionStore    VersionStore
	CollectionStore CollectionStore
	S3Client        S3Client
	S3Bucket        string
	PublicBaseURL   string
	RateLimit       RateLimitConfig
	OIDCEnabled     bool
	AdminRoles      []string // roles that grant admin access in the portal
	PromptStore        PromptStore
	PromptRegistrar    PromptRegistrar
	PromptInfoProvider PromptInfoProvider
	AuditMetrics    AuditMetrics
	InsightStore    InsightReader
	PersonaResolver PersonaResolver
	// Platform brand (far right of public viewer header)
	BrandName    string // display name (default: "MCP Data Platform")
	BrandLogoSVG string // inline SVG for header logo (empty = default icon)
	BrandURL     string // link URL (e.g., "https://plexara.io"); empty = no link

	// Implementor brand (far left of public viewer header, optional)
	ImplementorName    string // display name (e.g., "ACME Corp"); empty = hidden
	ImplementorLogoSVG string // inline SVG; empty = hidden
	ImplementorURL     string // link URL; empty = no link
}

// Handler provides portal REST API endpoints.
type Handler struct {
	mux         *http.ServeMux
	publicMux   *http.ServeMux
	authedMux   http.Handler
	deps        Deps
	rateLimiter *RateLimiter
}

// NewHandler creates a new portal API handler.
func NewHandler(deps Deps, authMiddle func(http.Handler) http.Handler) *Handler {
	h := &Handler{
		mux:         http.NewServeMux(),
		publicMux:   http.NewServeMux(),
		deps:        deps,
		rateLimiter: NewRateLimiter(deps.RateLimit),
	}
	h.registerRoutes()

	// Wrap the authenticated mux once at startup, not on every request.
	if authMiddle != nil {
		h.authedMux = authMiddle(h.mux)
	} else {
		h.authedMux = h.mux
	}

	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/portal/view/") {
		h.publicMux.ServeHTTP(w, r)
		return
	}
	h.authedMux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	// Authenticated routes
	h.mux.HandleFunc("GET /api/v1/portal/me", h.getMe)
	h.mux.HandleFunc("GET /api/v1/portal/assets", h.listAssets)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}", h.getAsset)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/content", h.getAssetContent)
	h.mux.HandleFunc("PUT /api/v1/portal/assets/{id}/content", h.updateAssetContent)
	h.mux.HandleFunc("PUT /api/v1/portal/assets/{id}/thumbnail", h.uploadThumbnail)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/thumbnail", h.getThumbnail)
	h.mux.HandleFunc("PUT /api/v1/portal/assets/{id}", h.updateAsset)
	h.mux.HandleFunc("DELETE /api/v1/portal/assets/{id}", h.deleteAsset)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/versions", h.listVersions)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/versions/{version}/content", h.getVersionContent)
	h.mux.HandleFunc("POST /api/v1/portal/assets/{id}/versions/{version}/revert", h.revertToVersion)
	h.mux.HandleFunc("POST /api/v1/portal/assets/{id}/shares", h.createShare)
	h.mux.HandleFunc("GET /api/v1/portal/assets/{id}/shares", h.listShares)
	h.mux.HandleFunc("DELETE /api/v1/portal/shares/{id}", h.revokeShare)
	h.mux.HandleFunc("GET /api/v1/portal/shared-with-me", h.listSharedWithMe)
	h.mux.HandleFunc("POST /api/v1/portal/assets/{id}/copy", h.copyAsset)

	// Collection routes
	if h.deps.CollectionStore != nil {
		h.mux.HandleFunc("POST /api/v1/portal/collections", h.createCollection)
		h.mux.HandleFunc("GET /api/v1/portal/collections", h.listCollections)
		h.mux.HandleFunc("GET /api/v1/portal/collections/{id}", h.getCollection)
		h.mux.HandleFunc("PUT /api/v1/portal/collections/{id}", h.updateCollection)
		h.mux.HandleFunc("DELETE /api/v1/portal/collections/{id}", h.deleteCollection)
		h.mux.HandleFunc("PUT /api/v1/portal/collections/{id}/config", h.updateCollectionConfig)
		h.mux.HandleFunc("PUT /api/v1/portal/collections/{id}/sections", h.setCollectionSections)
		h.mux.HandleFunc("PUT /api/v1/portal/collections/{id}/thumbnail", h.uploadCollectionThumbnail)
		h.mux.HandleFunc("GET /api/v1/portal/collections/{id}/thumbnail", h.getCollectionThumbnail)
		h.mux.HandleFunc("POST /api/v1/portal/collections/{id}/shares", h.createCollectionShare)
		h.mux.HandleFunc("GET /api/v1/portal/collections/{id}/shares", h.listCollectionShares)
		h.mux.HandleFunc("GET /api/v1/portal/shared-collections", h.listSharedCollections)
	}

	// Prompt routes
	h.registerPromptRoutes()

	// Activity routes (user-scoped audit metrics)
	if h.deps.AuditMetrics != nil {
		h.mux.HandleFunc("GET /api/v1/portal/activity/overview", h.getActivityOverview)
		h.mux.HandleFunc("GET /api/v1/portal/activity/timeseries", h.getActivityTimeseries)
		h.mux.HandleFunc("GET /api/v1/portal/activity/breakdown", h.getActivityBreakdown)
	}

	// Knowledge routes (user-scoped insights)
	if h.deps.InsightStore != nil {
		h.mux.HandleFunc("GET /api/v1/portal/knowledge/insights", h.listMyInsights)
		h.mux.HandleFunc("GET /api/v1/portal/knowledge/insights/stats", h.getMyInsightStats)
	}

	// Public routes (rate limited)
	h.publicMux.Handle("GET /portal/view/{token}",
		h.rateLimiter.Middleware(http.HandlerFunc(h.publicView)))
	h.publicMux.Handle("GET /portal/view/{token}/items/{assetId}/content",
		h.rateLimiter.Middleware(http.HandlerFunc(h.publicCollectionItemContent)))
	h.publicMux.Handle("GET /portal/view/{token}/items/{assetId}/thumbnail",
		h.rateLimiter.Middleware(http.HandlerFunc(h.publicCollectionItemThumbnail)))
	h.publicMux.Handle("GET /portal/view/{token}/items/{assetId}/view",
		h.rateLimiter.Middleware(http.HandlerFunc(h.publicCollectionItemView)))
}

// --- Me handler ---

// meResponse is returned by GET /api/v1/portal/me.
type meResponse struct {
	UserID  string   `json:"user_id"`
	Email   string   `json:"email,omitempty"`
	Roles   []string `json:"roles"`
	IsAdmin bool     `json:"is_admin"`
	Persona string   `json:"persona,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	resp := meResponse{
		UserID:  user.UserID,
		Email:   user.Email,
		Roles:   user.Roles,
		IsAdmin: hasAnyRole(user.Roles, h.deps.AdminRoles),
	}

	if h.deps.PersonaResolver != nil {
		if info := h.deps.PersonaResolver(user.Roles); info != nil {
			resp.Persona = info.Name
			resp.Tools = info.Tools
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Asset handlers ---

// paginatedResponse wraps paginated results.
type paginatedResponse struct {
	Data           any                     `json:"data"`
	Total          int                     `json:"total"`
	Limit          int                     `json:"limit"`
	Offset         int                     `json:"offset"`
	ShareSummaries map[string]ShareSummary `json:"share_summaries,omitempty"`
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
		Limit:       intParam(r, paramLimit, defaultLimit),
		Offset:      intParam(r, paramOffset, 0),
	}

	assets, total, err := h.deps.AssetStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list assets")
		return
	}

	if assets == nil {
		assets = []Asset{}
	}

	// Fetch share summaries for the returned assets.
	var summaries map[string]ShareSummary
	if len(assets) > 0 {
		ids := make([]string, len(assets))
		for i, a := range assets {
			ids[i] = a.ID
		}
		summaries, _ = h.deps.ShareStore.ListActiveShareSummaries(r.Context(), ids)
	}

	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: assets, Total: total,
		Limit: filter.EffectiveLimit(), Offset: filter.Offset,
		ShareSummaries: summaries,
	})
}

// assetResponse is the response for GET /api/v1/portal/assets/{id}.
// It extends the Asset with optional share context when the viewer is not the owner.
type assetResponse struct {
	Asset
	SharePermission SharePermission `json:"share_permission,omitempty"`
	IsOwner         bool            `json:"is_owner"`
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
		writeError(w, http.StatusGone, errAssetDeleted)
		return
	}

	resp := assetResponse{Asset: *asset, IsOwner: asset.OwnerID == user.UserID}

	if !resp.IsOwner {
		perm, permErr := h.sharePermissionForUser(r, id, user)
		if permErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to check share access")
			return
		}
		if perm == "" {
			writeError(w, http.StatusForbidden, errAccessDenied)
			return
		}
		resp.SharePermission = perm
	}

	writeJSON(w, http.StatusOK, resp)
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
		writeError(w, http.StatusGone, errAssetDeleted)
		return
	}

	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
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
	_, _ = w.Write(data) // #nosec G705 -- content served with explicit Content-Type, not rendered as HTML
}

func (h *Handler) updateAssetContent(w http.ResponseWriter, r *http.Request) {
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

	if !h.canEditAsset(w, r, id, asset, user) {
		return
	}

	if !h.versionedStorageReady() {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, MaxContentUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if int64(len(data)) > MaxContentUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "content exceeds 10 MB limit")
		return
	}

	versionID := uuid.New().String()
	ext := ExtensionForContentType(asset.ContentType)
	versionedKey := fmt.Sprintf("portal/%s/%s/%s/content%s", asset.OwnerID, id, versionID, ext)

	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, versionedKey, data, asset.ContentType); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload content")
		return
	}

	av := AssetVersion{
		ID:            versionID,
		AssetID:       id,
		S3Key:         versionedKey,
		S3Bucket:      asset.S3Bucket,
		ContentType:   asset.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     user.Email,
		ChangeSummary: changeSummaryFromHeader(r, "Content updated"),
	}

	if _, err := h.deps.VersionStore.CreateVersion(r.Context(), av); err != nil {
		h.cleanupOrphanedS3(r.Context(), asset.S3Bucket, versionedKey)
		writeError(w, http.StatusInternalServerError, "failed to create version")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

func (h *Handler) uploadThumbnail(w http.ResponseWriter, r *http.Request) {
	asset, ok := h.requireOwnedAsset(w, r)
	if !ok {
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	ct := r.Header.Get(headerContentType)
	mediaType, _, _ := mime.ParseMediaType(ct)
	if mediaType != "image/png" {
		writeError(w, http.StatusBadRequest, "thumbnail must be image/png")
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, MaxThumbnailUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if int64(len(data)) > MaxThumbnailUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("thumbnail exceeds %d KB limit", MaxThumbnailUploadBytes>>10))
		return
	}

	thumbKey := DeriveThumbnailKey(asset.S3Key)
	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, thumbKey, data, "image/png"); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload thumbnail")
		return
	}

	id := r.PathValue(pathKeyID)
	updates := AssetUpdate{ThumbnailS3Key: &thumbKey}
	if err := h.deps.AssetStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update asset metadata")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "updated"})
}

// requireOwnedAsset validates auth, fetches the asset, checks deletion and ownership.
// Returns the asset and true on success, or writes the error response and returns false.
func (h *Handler) requireOwnedAsset(w http.ResponseWriter, r *http.Request) (*Asset, bool) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, false
	}

	id := r.PathValue(pathKeyID)
	asset, err := h.deps.AssetStore.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errAssetNotFound)
		return nil, false
	}

	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAssetDeleted)
		return nil, false
	}

	if asset.OwnerID != user.UserID {
		writeError(w, http.StatusForbidden, "only the owner can update this asset")
		return nil, false
	}

	return asset, true
}

func (h *Handler) getThumbnail(w http.ResponseWriter, r *http.Request) {
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

	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}

	if asset.ThumbnailS3Key == "" {
		writeError(w, http.StatusNotFound, "no thumbnail available")
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	data, _, err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, asset.ThumbnailS3Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve thumbnail")
		return
	}

	w.Header().Set(headerContentType, "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served as image/png, not rendered as HTML
}

// DeriveThumbnailKey replaces the filename in an S3 key with "thumbnail.png".
func DeriveThumbnailKey(s3Key string) string {
	idx := strings.LastIndex(s3Key, "/")
	if idx < 0 {
		return "thumbnail.png"
	}
	return s3Key[:idx+1] + "thumbnail.png"
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

// versionedStorageReady returns true if both S3 and version tracking are configured.
func (h *Handler) versionedStorageReady() bool {
	return h.deps.S3Client != nil && h.deps.VersionStore != nil
}

// cleanupOrphanedS3 attempts to delete an S3 object that was uploaded but whose
// corresponding version record failed to persist. Errors are logged but not propagated.
func (h *Handler) cleanupOrphanedS3(ctx context.Context, bucket, key string) {
	if h.deps.S3Client == nil {
		return
	}
	if err := h.deps.S3Client.DeleteObject(ctx, bucket, key); err != nil {
		slog.Warn("failed to clean up orphaned S3 object", // #nosec G706 -- structured log, not user-facing
			"bucket", bucket, "key", key, "error", err)
	}
}

// --- Version handlers ---

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request) {
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
	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}
	if h.deps.VersionStore == nil {
		writeJSON(w, http.StatusOK, paginatedResponse{Data: []AssetVersion{}, Total: 0})
		return
	}

	limit := intParam(r, paramLimit, defaultLimit)
	offset := intParam(r, paramOffset, 0)
	versions, total, err := h.deps.VersionStore.ListByAsset(r.Context(), id, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	if versions == nil {
		versions = []AssetVersion{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{Data: versions, Total: total, Limit: limit, Offset: offset})
}

func (h *Handler) getVersionContent(w http.ResponseWriter, r *http.Request) {
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
	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}
	if !h.versionedStorageReady() {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
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
	w.Header().Set(headerContentType, cmp.Or(contentType, "application/octet-stream"))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) // #nosec G705 -- content served with explicit Content-Type
}

func (h *Handler) revertToVersion(w http.ResponseWriter, r *http.Request) {
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
	if !h.canEditAsset(w, r, id, asset, user) {
		return
	}
	if !h.versionedStorageReady() {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
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

	assignedVersion, revertErr := h.revertContentToVersion(r.Context(), asset, id, targetVer, user.Email)
	if revertErr != nil {
		writeError(w, revertErr.code, revertErr.msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "reverted",
		"version": assignedVersion,
	})
}

type httpError struct {
	code int
	msg  string
}

func (h *Handler) revertContentToVersion(ctx context.Context, asset *Asset, assetID string, targetVer *AssetVersion, createdBy string) (int, *httpError) {
	data, _, err := h.deps.S3Client.GetObject(ctx, targetVer.S3Bucket, targetVer.S3Key)
	if err != nil {
		return 0, &httpError{http.StatusInternalServerError, "failed to read version content"}
	}

	versionID := uuid.New().String()
	ext := ExtensionForContentType(targetVer.ContentType)
	newKey := fmt.Sprintf("portal/%s/%s/%s/content%s", asset.OwnerID, assetID, versionID, ext)

	if err := h.deps.S3Client.PutObject(ctx, asset.S3Bucket, newKey, data, targetVer.ContentType); err != nil {
		return 0, &httpError{http.StatusServiceUnavailable, "failed to upload reverted content"}
	}

	av := AssetVersion{
		ID:            versionID,
		AssetID:       assetID,
		S3Key:         newKey,
		S3Bucket:      asset.S3Bucket,
		ContentType:   targetVer.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     createdBy,
		ChangeSummary: fmt.Sprintf("Reverted from v%d", targetVer.Version),
	}
	assignedVersion, err := h.deps.VersionStore.CreateVersion(ctx, av)
	if err != nil {
		h.cleanupOrphanedS3(ctx, asset.S3Bucket, newKey)
		return 0, &httpError{http.StatusInternalServerError, "failed to create revert version"}
	}
	return assignedVersion, nil
}

// --- Share handlers ---

// createShareRequest is the request body for creating a share.
type createShareRequest struct {
	ExpiresIn        string  `json:"expires_in,omitempty"` // duration string, e.g. "24h"
	SharedWithUserID string  `json:"shared_with_user_id,omitempty"`
	SharedWithEmail  string  `json:"shared_with_email,omitempty"`
	HideExpiration   bool    `json:"hide_expiration,omitempty"`
	NoticeText       *string `json:"notice_text,omitempty"` // nil = default, "" = hidden, custom = as-is
	Permission       string  `json:"permission,omitempty"`  // "viewer" (default) or "editor"
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

	share, buildErr := buildShare(shareTarget{AssetID: assetID}, user.Email, req)
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

// shareTarget identifies what a share is for — either an asset or a collection.
type shareTarget struct {
	AssetID      string
	CollectionID string
}

// buildShare validates the request and constructs a Share, returning an error for invalid input.
func buildShare(target shareTarget, createdBy string, req createShareRequest) (Share, error) {
	token, err := generateToken()
	if err != nil {
		return Share{}, fmt.Errorf("failed to generate share token")
	}

	email := strings.ToLower(strings.TrimSpace(req.SharedWithEmail))
	if email != "" {
		if err := ValidateEmail(email); err != nil {
			return Share{}, err
		}
	}

	noticeText := defaultNoticeText
	if req.NoticeText != nil {
		noticeText = *req.NoticeText
		if err := ValidateNoticeText(noticeText); err != nil {
			return Share{}, err
		}
	}

	perm, permErr := resolveSharePermission(req, email)
	if permErr != nil {
		return Share{}, permErr
	}

	share := Share{
		ID:               uuid.New().String(),
		AssetID:          target.AssetID,
		CollectionID:     target.CollectionID,
		Token:            token,
		CreatedBy:        createdBy,
		SharedWithUserID: req.SharedWithUserID,
		SharedWithEmail:  email,
		Permission:       perm,
		HideExpiration:   req.HideExpiration,
		NoticeText:       noticeText,
	}

	if req.ExpiresIn != "" {
		dur, parseErr := time.ParseDuration(req.ExpiresIn)
		if parseErr != nil {
			return Share{}, fmt.Errorf("invalid expires_in duration")
		}
		exp := time.Now().Add(dur)
		share.ExpiresAt = &exp
	}

	return share, nil
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

	limit := intParam(r, paramLimit, defaultLimit)
	offset := intParam(r, paramOffset, 0)

	shared, total, err := h.deps.ShareStore.ListSharedWithUser(r.Context(), user.UserID, strings.ToLower(user.Email), limit, offset)
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

// --- Activity handlers ---

func (h *Handler) getActivityOverview(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	overview, err := h.deps.AuditMetrics.Overview(r.Context(), audit.MetricsFilter{
		StartTime: parseTimeParam(q, "start_time"),
		EndTime:   parseTimeParam(q, "end_time"),
		UserID:    user.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query activity overview")
		return
	}

	writeJSON(w, http.StatusOK, overview)
}

func (h *Handler) getActivityTimeseries(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	resolution := audit.Resolution(q.Get("resolution"))
	if resolution == "" {
		resolution = audit.ResolutionHour
	}
	if !audit.ValidResolutions[resolution] {
		writeError(w, http.StatusBadRequest, "invalid resolution: must be minute, hour, or day")
		return
	}

	buckets, err := h.deps.AuditMetrics.Timeseries(r.Context(), audit.TimeseriesFilter{
		Resolution: resolution,
		StartTime:  parseTimeParam(q, "start_time"),
		EndTime:    parseTimeParam(q, "end_time"),
		UserID:     user.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query activity timeseries")
		return
	}

	writeJSON(w, http.StatusOK, buckets)
}

func (h *Handler) getActivityBreakdown(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	groupBy := audit.BreakdownDimension(q.Get("group_by"))
	if groupBy == "" {
		groupBy = audit.BreakdownByToolName
	}
	if !audit.ValidBreakdownDimensions[groupBy] {
		writeError(w, http.StatusBadRequest,
			"invalid group_by: must be tool_name, user_id, persona, toolkit_kind, or connection")
		return
	}

	var limit int
	if v := q.Get(paramLimit); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := h.deps.AuditMetrics.Breakdown(r.Context(), audit.BreakdownFilter{
		GroupBy:   groupBy,
		Limit:     limit,
		StartTime: parseTimeParam(q, "start_time"),
		EndTime:   parseTimeParam(q, "end_time"),
		UserID:    user.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query activity breakdown")
		return
	}

	writeJSON(w, http.StatusOK, entries)
}

// parseTimeParam parses an RFC 3339 time parameter from query values.
func parseTimeParam(q url.Values, key string) *time.Time {
	v := q.Get(key)
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

// --- Knowledge handlers ---

func (h *Handler) listMyInsights(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	filter := knowledge.InsightFilter{
		CapturedBy: user.UserID,
		Status:     q.Get("status"),
		Category:   q.Get("category"),
		Limit:      intParam(r, paramLimit, knowledge.DefaultLimit),
		Offset:     intParam(r, paramOffset, 0),
	}

	insights, total, err := h.deps.InsightStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list insights")
		return
	}

	if insights == nil {
		insights = []knowledge.Insight{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: insights, Total: total,
		Limit: filter.EffectiveLimit(), Offset: filter.Offset,
	})
}

func (h *Handler) getMyInsightStats(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	stats, err := h.deps.InsightStore.Stats(r.Context(), knowledge.InsightFilter{
		CapturedBy: user.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query insight stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
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

// changeSummaryFromHeader reads the X-Change-Summary header from the request.
// If the header is empty or whitespace-only, it returns the provided fallback.
// The result is truncated to MaxChangeSummaryLength characters.
func changeSummaryFromHeader(r *http.Request, fallback string) string {
	if s := strings.TrimSpace(r.Header.Get("X-Change-Summary")); s != "" {
		if len(s) > MaxChangeSummaryLength {
			return s[:MaxChangeSummaryLength]
		}
		return s
	}
	return fallback
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

// hasAnyRole returns true if any role in userRoles is also in targetRoles.
func hasAnyRole(userRoles, targetRoles []string) bool {
	for _, r := range userRoles {
		if slices.Contains(targetRoles, r) {
			return true
		}
	}
	return false
}

// isShareActive returns true if the share is not revoked and not expired.
func isShareActive(s Share) bool {
	if s.Revoked {
		return false
	}
	return s.ExpiresAt == nil || !s.ExpiresAt.Before(time.Now())
}

// sharePermissionForUser returns the highest permission level a user has for a shared asset.
// Returns empty string if not shared with this user.
func (h *Handler) sharePermissionForUser(r *http.Request, assetID string, user *User) (SharePermission, error) {
	shares, err := h.deps.ShareStore.ListByAsset(r.Context(), assetID)
	if err != nil {
		return "", fmt.Errorf("checking share permission: %w", err)
	}
	var best SharePermission
	for _, s := range shares {
		if !isShareActive(s) {
			continue
		}
		matched := s.SharedWithUserID == user.UserID ||
			(user.Email != "" && strings.EqualFold(s.SharedWithEmail, user.Email))
		if !matched {
			continue
		}
		if s.Permission == PermissionEditor {
			return PermissionEditor, nil // highest possible, short-circuit
		}
		if best == "" {
			best = s.Permission
		}
	}
	return best, nil
}

// canViewAsset checks owner or any share access, writing an HTTP error on failure.
func (h *Handler) canViewAsset(w http.ResponseWriter, r *http.Request, assetID string, asset *Asset, user *User) bool {
	if asset.OwnerID == user.UserID {
		return true
	}
	perm, err := h.sharePermissionForUser(r, assetID, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check share access")
		return false
	}
	if perm == "" {
		writeError(w, http.StatusForbidden, errAccessDenied)
		return false
	}
	return true
}

// canEditAsset checks owner or editor share access, writing an HTTP error on failure.
func (h *Handler) canEditAsset(w http.ResponseWriter, r *http.Request, assetID string, asset *Asset, user *User) bool {
	if asset.OwnerID == user.UserID {
		return true
	}
	perm, err := h.sharePermissionForUser(r, assetID, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check share access")
		return false
	}
	if perm != PermissionEditor {
		writeError(w, http.StatusForbidden, "only the owner or an editor can update this asset")
		return false
	}
	return true
}

// resolveSharePermission validates and resolves the permission for a new share.
// Public links (no user/email target) are always forced to viewer.
func resolveSharePermission(req createShareRequest, email string) (SharePermission, error) {
	perm := PermissionViewer
	if req.Permission != "" {
		if !ValidSharePermission(req.Permission) {
			return "", fmt.Errorf("invalid permission: must be viewer or editor")
		}
		perm = SharePermission(req.Permission)
	}
	if email == "" && req.SharedWithUserID == "" {
		perm = PermissionViewer
	}
	return perm, nil
}

// copyAsset creates an independent copy of a shared asset in the current user's My Assets.
func (h *Handler) copyAsset(w http.ResponseWriter, r *http.Request) {
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

	if !h.canViewAsset(w, r, id, asset, user) {
		return
	}

	if asset.SizeBytes > MaxContentUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "asset too large to copy")
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	newAsset, copyErr := h.performAssetCopy(r.Context(), asset, user)
	if copyErr != nil {
		writeError(w, copyErr.code, copyErr.msg)
		return
	}

	writeJSON(w, http.StatusCreated, newAsset)
}

func (h *Handler) performAssetCopy(ctx context.Context, asset *Asset, user *User) (*Asset, *httpError) {
	data, contentType, err := h.deps.S3Client.GetObject(ctx, asset.S3Bucket, asset.S3Key)
	if err != nil {
		return nil, &httpError{http.StatusInternalServerError, "failed to read source content"}
	}

	newID := uuid.New().String()
	newS3Key := fmt.Sprintf("portal/%s/%s/content", user.UserID, newID)

	if err := h.deps.S3Client.PutObject(ctx, h.deps.S3Bucket, newS3Key, data, contentType); err != nil {
		return nil, &httpError{http.StatusServiceUnavailable, "failed to copy content"}
	}

	newAsset := Asset{
		ID:          newID,
		OwnerID:     user.UserID,
		OwnerEmail:  user.Email,
		Name:        asset.Name + " (copy)",
		Description: asset.Description,
		ContentType: asset.ContentType,
		S3Bucket:    h.deps.S3Bucket,
		S3Key:       newS3Key,
		SizeBytes:   int64(len(data)),
		Tags:        asset.Tags,
		Provenance:  asset.Provenance,
	}

	if err := h.deps.AssetStore.Insert(ctx, newAsset); err != nil {
		return nil, &httpError{http.StatusInternalServerError, "failed to create asset copy"}
	}

	if h.deps.VersionStore != nil {
		v1 := AssetVersion{
			ID:            uuid.New().String(),
			AssetID:       newID,
			S3Key:         newS3Key,
			S3Bucket:      h.deps.S3Bucket,
			ContentType:   contentType,
			SizeBytes:     int64(len(data)),
			CreatedBy:     user.Email,
			ChangeSummary: "Copied from " + asset.ID,
		}
		if _, err := h.deps.VersionStore.CreateVersion(ctx, v1); err != nil {
			slog.Warn("failed to create initial version for copied asset", // #nosec G706 -- structured log, not user-facing
				"asset_id", newID, "error", err)
		}
	}

	return &newAsset, nil
}
