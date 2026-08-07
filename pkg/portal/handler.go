package portal

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/feedbackapi"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/viewerlimit"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
	"github.com/txn2/mcp-data-platform/pkg/ratelimit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// Common error messages, path value keys, and query parameter names.
const (
	errAuthRequired        = "authentication required"
	errSearchScopeRequired = "a user identity (email) is required to scope search results"
	errAssetNotFound       = "asset not found"
	errAssetDeleted        = "asset has been deleted"
	errStorageNotReady     = "content storage not configured"
	errAccessDenied        = "access denied"
	// logKeyError is the structured-logging key for an error value.
	logKeyError       = "error"
	pathKeyID         = "id"
	paramLimit        = "limit"
	paramOffset       = "offset"
	paramQuery        = "q"
	headerContentType = "Content-Type"
)

// DefaultNoticeText is the notice shown on public shares when no custom text is
// provided. Exported so out-of-package share creators (e.g. the export adapters)
// apply the same default rather than re-declaring the literal.
const DefaultNoticeText = portaldomain.DefaultNoticeText

// defaultNoticeText is the internal alias retained for existing references.
const defaultNoticeText = DefaultNoticeText

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
	// Get returns a single insight by id, used to surface a knowledge page's
	// source-insight lineage (#678). A drained or deleted insight returns an error.
	Get(ctx context.Context, id string) (*knowledge.Insight, error)
}

// MemoryReader provides read-only access to user memory records. The
// search methods back the portal's per-user "my knowledge" search: both
// queries are scoped to the caller via CreatedBy server-side, so a user
// can never search another user's records.
type MemoryReader interface {
	List(ctx context.Context, filter memory.Filter) ([]memory.Record, int, error)
	HybridSearch(ctx context.Context, q memory.HybridQuery) ([]memory.ScoredRecord, error)
	LexicalSearch(ctx context.Context, q memory.LexicalQuery) ([]memory.ScoredRecord, error)
}

// InsightSearcher is the optional relevance-search capability of the insight
// store, declared canonically in the knowledge package next to its query and
// result types. The knowledge-search route (and the recall_insight tool) are
// registered only when the wired InsightStore satisfies it; the memory-backed
// adapter does, the legacy separate-table store does not.
type InsightSearcher = knowledge.InsightSearcher

// PersonaInfo holds resolved persona details for the current user.
type PersonaInfo struct {
	Name  string
	Tools []string // resolved tool names from Allow/Deny patterns
}

// PersonaResolver resolves a user's roles to their persona info.
type PersonaResolver func(roles []string) *PersonaInfo

// DataHubToolPrefix prefixes every DataHub MCP tool name.
const DataHubToolPrefix = "datahub_"

// HasCatalogAccess reports whether a user may read the DataHub catalog: their
// persona grants any DataHub tool (read or write), or they are an admin. It is
// the one rule behind every catalog read the portal serves — the DataHub REST
// surface (internal/httpserver/datahubapi) and the knowledge-page reference
// labels (#1159) — so a persona denied the catalog is denied it on both, and
// the portal never discloses more than the persona-filtered MCP surface would.
func HasCatalogAccess(user *User, resolver PersonaResolver, adminRoles []string) bool {
	if user == nil {
		return false
	}
	if resolver != nil {
		if info := resolver(user.Roles); info != nil {
			for _, t := range info.Tools {
				if strings.HasPrefix(t, DataHubToolPrefix) {
					return true
				}
			}
		}
	}
	return access.HasAnyRole(user.Roles, adminRoles)
}

// Deps holds dependencies for the portal handler.
type Deps struct {
	AssetStore         AssetStore
	ShareStore         ShareStore
	VersionStore       VersionStore
	CollectionStore    CollectionStore
	ThreadStore        ThreadStore
	KnowledgePageStore knowledgepage.Store
	// KnowledgePageDedupThreshold is the create-time duplicate-gate threshold (#705)
	// the REST create path shares with the MCP apply path. 0 disables the gate. The
	// platform resolves it (default/disabled) before wiring; the handler treats it as
	// final.
	KnowledgePageDedupThreshold float64
	S3Client                    S3Client
	S3Bucket                    string
	PublicBaseURL               string
	RateLimit                   RateLimitConfig
	// RateLimitResolver attributes the client IP for the public viewer's
	// rate limiter with trusted-proxy awareness (#904). nil yields the safe
	// trust-none default (direct peer address; X-Forwarded-For ignored), so an
	// attacker cannot mint unlimited per-IP buckets with a spoofed header.
	RateLimitResolver  *ratelimit.Resolver
	OIDCEnabled        bool
	AdminRoles         []string // roles that grant admin access in the portal
	PromptStore        PromptStore
	PromptRegistrar    PromptRegistrar
	PromptInfoProvider PromptInfoProvider
	AuditMetrics       AuditMetrics
	InsightStore       InsightReader
	ChangesetReader    ChangesetReader
	MemoryStore        MemoryReader
	MemoryWriter       MemoryWriter
	EmbeddingProvider  embedding.Provider
	PersonaResolver    PersonaResolver
	// SearchRouter backs GET /api/v1/portal/search, the REST surface over the
	// unified knowledge federation. nil disables the endpoint (no searchable
	// source configured).
	SearchRouter SearchRouter
	// DataHubRegistrar, when set, registers the portal DataHub Catalog and Context
	// Docs REST routes (#718) onto the portal mux. It is provided by cmd wiring
	// (internal/httpserver/datahubapi.Handler.Register) so the DataHub feature lives in its
	// own package; nil leaves the /api/v1/portal/datahub/* routes unregistered.
	DataHubRegistrar func(*http.ServeMux)
	// CatalogLabeler names the DataHub governance entities a knowledge page
	// references (#1159), so a citation to a glossary term, a tag, or a domain
	// renders as its display name instead of the generated key inside its URN.
	// It is provided by the same cmd wiring as DataHubRegistrar; nil (no DataHub
	// connection) falls back to the name derivable from the URN itself.
	CatalogLabeler CatalogLabeler
	// MentionResolver filters the @-mentions written in a thread comment to
	// the people who can open the thread's target (#627). nil disables
	// mentions (no database): tokens stay ordinary text and notify nobody.
	MentionResolver MentionResolver
	// Authenticator resolves a logged-in user from a public (unauthenticated)
	// request so the public viewer can auto-promote a signed-in viewer to a
	// derived share. Optional; nil disables auto-promote.
	Authenticator *Authenticator
	// Platform brand (far right of public viewer header)
	BrandName    string // display name (default: "MCP Data Platform")
	BrandLogoSVG string // inline SVG for header logo (empty = default icon)
	BrandURL     string // link URL (e.g., "https://plexara.io"); empty = no link

	// Implementor brand (far left of public viewer header, optional)
	ImplementorName    string // display name (e.g., "ACME Corp"); empty = hidden
	ImplementorLogoSVG string // inline SVG; empty = hidden
	ImplementorURL     string // link URL; empty = no link

	// Notifier receives share and thread trigger events (issue #631). nil
	// disables email notifications; implementations log their own failures
	// and never fail the originating request.
	Notifier Notifier
	// ShareGuest is the guest access path for email shares (#1001): branded
	// denial pages, one-time view links, and guest sessions. nil keeps the
	// pre-#1001 plain-text denials and registers no guest routes.
	ShareGuest *shareguest.Service
	// NotificationRegistrar, when set, registers the self-scoped
	// notification-preference REST routes onto the portal's authenticated
	// mux (the DataHubRegistrar pattern): the feature lives with the
	// notification substrate, not in this package. nil leaves the
	// /api/v1/portal/notification-prefs routes unregistered.
	NotificationRegistrar func(*http.ServeMux)
}

// Handler provides portal REST API endpoints.
type Handler struct {
	mux         *http.ServeMux
	publicMux   *http.ServeMux
	authedMux   http.Handler
	deps        Deps
	rateLimiter *viewerlimit.RateLimiter
	access      *access.Checker
}

// NewHandler creates a new portal API handler.
func NewHandler(deps Deps, authMiddle func(http.Handler) http.Handler) *Handler {
	h := &Handler{
		mux:         http.NewServeMux(),
		publicMux:   http.NewServeMux(),
		deps:        deps,
		rateLimiter: viewerlimit.New(deps.RateLimit, deps.RateLimitResolver),
		access:      newAccessChecker(deps),
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

// newAccessChecker builds the portal's authorization core from the wired
// dependencies. Both this package and the handler seams under internal/portal
// answer every permission question through it, so a check cannot come to mean
// two different things in two places.
func newAccessChecker(deps Deps) *access.Checker {
	cfg := access.Config{
		Assets:      deps.AssetStore,
		Collections: deps.CollectionStore,
		Shares:      deps.ShareStore,
		Prompts:     deps.PromptStore,
		AdminRoles:  deps.AdminRoles,
	}
	if deps.PersonaResolver != nil {
		cfg.PersonaTools = func(roles []string) []string {
			if info := deps.PersonaResolver(roles); info != nil {
				return info.Tools
			}
			return nil
		}
	}
	return access.New(cfg)
}

// feedbackConfig projects the portal's dependencies onto the feedback seam's
// narrower contract. The authorization core is passed rather than rebuilt, so
// the seam and the routes that stayed here answer permission questions through
// one checker.
func (h *Handler) feedbackConfig() feedbackapi.Config {
	cfg := feedbackapi.Config{
		Threads:        h.deps.ThreadStore,
		Assets:         h.deps.AssetStore,
		Collections:    h.deps.CollectionStore,
		Shares:         h.deps.ShareStore,
		Prompts:        h.deps.PromptStore,
		KnowledgePages: h.deps.KnowledgePageStore,
		Changesets:     h.deps.ChangesetReader,
		MemoryWriter:   h.deps.MemoryWriter,
		Mentions:       h.deps.MentionResolver,
		Notifier:       h.deps.Notifier,
		Access:         h.access,
	}
	if h.deps.PersonaResolver != nil {
		cfg.PersonaName = func(roles []string) string {
			if info := h.deps.PersonaResolver(roles); info != nil {
				return info.Name
			}
			return ""
		}
	}
	return cfg
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
	// Self-scoped notification preferences (#631), registered by the
	// notification substrate onto the authenticated mux.
	if h.deps.NotificationRegistrar != nil {
		h.deps.NotificationRegistrar(h.mux)
	}
	h.mux.HandleFunc("GET /api/v1/portal/assets", h.listAssets)
	// Relevance search is registered only when the wired asset store supports it
	// (pgvector-backed deployments); see AssetSearcher.
	if _, ok := h.deps.AssetStore.(AssetSearcher); ok {
		h.mux.HandleFunc("GET /api/v1/portal/assets/search", h.searchMyAssets)
	}
	h.mux.HandleFunc("POST /api/v1/portal/assets", h.createAsset)
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
		if _, ok := h.deps.CollectionStore.(CollectionSearcher); ok {
			h.mux.HandleFunc("GET /api/v1/portal/collections/search", h.searchMyCollections)
		}
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

	// Knowledge page routes (canonical business/domain knowledge)
	h.registerKnowledgePageRoutes()

	// Unified knowledge search (one query across every source the caller can access)
	h.registerSearchRoutes()

	// DataHub catalog + context-doc browse/search/edit (#718). Registered by cmd
	// wiring via internal/httpserver/datahubapi so the feature stays in its own package.
	if h.deps.DataHubRegistrar != nil {
		h.deps.DataHubRegistrar(h.mux)
	}

	// Feedback: threads, activity, worklists, sign-off, validation, and
	// capturing a thread as a reviewable insight. The family lives in
	// internal/portal/feedbackapi; this package supplies its dependencies.
	feedback := feedbackapi.New(h.feedbackConfig())
	feedback.Register(h.mux)
	feedback.RegisterInsightCapture(h.mux)

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
		// Relevance search is registered only when the wired insight store
		// supports it (memory-backed deployments); see InsightSearcher.
		if _, ok := h.deps.InsightStore.(InsightSearcher); ok {
			h.mux.HandleFunc("GET /api/v1/portal/knowledge/insights/search", h.searchMyInsights)
		}
	}

	// Memory routes (user-scoped memory records)
	if h.deps.MemoryStore != nil {
		h.mux.HandleFunc("GET /api/v1/portal/memory/records", h.listMyMemories)
		h.mux.HandleFunc("GET /api/v1/portal/memory/records/stats", h.getMyMemoryStats)
		h.mux.HandleFunc("GET /api/v1/portal/memory/records/search", h.searchMyMemories)
	}

	// Public routes: rate limited, then gated on the share's access mode.
	// publicChain is the only way a handler reaches this mux, so no route can
	// serve a token the gate would refuse (#999).
	h.publicMux.Handle("GET /portal/view/{token}", h.publicChain(h.publicView))
	h.publicMux.Handle("GET /portal/view/{token}/content", h.publicChain(h.publicAssetContent))
	h.publicMux.Handle("GET /portal/view/{token}/thumbnail", h.publicChain(h.publicAssetThumbnail))
	h.publicMux.Handle("GET /portal/view/{token}/collection-thumbnail", h.publicChain(h.publicCollectionThumbnail))
	h.publicMux.Handle("GET /portal/view/{token}/items/{assetId}/content", h.publicChain(h.publicCollectionItemContent))
	h.publicMux.Handle("GET /portal/view/{token}/items/{assetId}/thumbnail", h.publicChain(h.publicCollectionItemThumbnail))
	h.publicMux.Handle("GET /portal/view/{token}/items/{assetId}/view", h.publicChain(h.publicCollectionItemView))

	// Guest link routes (#1001): rate limited but outside the access gate,
	// since their whole audience is callers the gate refuses.
	if h.deps.ShareGuest != nil {
		h.publicMux.Handle("POST /portal/view/{token}/request-link",
			h.rateLimiter.Middleware(http.HandlerFunc(h.deps.ShareGuest.HandleRequestLink)))
		h.publicMux.Handle("GET /portal/view/{token}/guest",
			h.rateLimiter.Middleware(http.HandlerFunc(h.deps.ShareGuest.HandleClaim)))
		h.publicMux.Handle("POST /portal/view/{token}/resubscribe",
			h.rateLimiter.Middleware(http.HandlerFunc(h.deps.ShareGuest.HandleResubscribe)))
	}
}

// publicChain wraps a share-viewer handler in the rate limiter and the share
// access gate, rate limiting outermost so a flood is shed before it costs a
// share lookup.
func (h *Handler) publicChain(fn http.HandlerFunc) http.Handler {
	return h.rateLimiter.Middleware(h.publicShareGate(fn))
}

// --- Me handler ---

// meResponse is returned by GET /api/v1/portal/me.
type meResponse struct {
	UserID  string   `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Email   string   `json:"email,omitempty" example:"analyst@example.com"`
	Roles   []string `json:"roles" example:"analyst,data_engineer"`
	IsAdmin bool     `json:"is_admin" example:"false"`
	Persona string   `json:"persona,omitempty" example:"analyst"`
	Tools   []string `json:"tools,omitempty" example:"trino_query,datahub_search"`
	// CSRFToken is set only for cookie sessions; the SPA echoes it in the
	// X-CSRF-Token header on mutations. API-key clients are exempt.
	CSRFToken string `json:"csrf_token,omitempty"`
}

// getMe handles GET /api/v1/portal/me.
//
// @Summary      Get current user info
// @Description  Returns the authenticated user's profile including roles, persona, and available tools.
// @Tags         User
// @Produce      json
// @Success      200  {object}  meResponse
// @Failure      401  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/me [get]
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
		IsAdmin: h.access.IsAdmin(user),
	}
	// Cookie sessions carry a CSRF token the SPA echoes on mutations; API-key
	// callers are exempt. Issued here since only /me needs it.
	if user.FromCookie && h.deps.Authenticator != nil {
		resp.CSRFToken = h.deps.Authenticator.IssueCSRF(user.UserID)
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
	Total          int                     `json:"total" example:"42"`
	Limit          int                     `json:"limit" example:"20"`
	Offset         int                     `json:"offset" example:"0"`
	ShareSummaries map[string]ShareSummary `json:"share_summaries,omitempty"`
}

// listAssets handles GET /api/v1/portal/assets.
//
// @Summary      List assets
// @Description  Returns paginated assets owned by the current user with optional filtering.
// @Tags         Assets
// @Produce      json
// @Param        content_type  query  string   false  "Filter by content type"
// @Param        tag           query  string   false  "Filter by tag"
// @Param        limit         query  integer  false  "Results per page (default: 20)"
// @Param        offset        query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets [get]
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
	SharePermission portaldomain.SharePermission `json:"share_permission,omitempty" example:"viewer"`
	IsOwner         bool                         `json:"is_owner" example:"true"`
}

// getAsset handles GET /api/v1/portal/assets/{id}.
//
// @Summary      Get asset
// @Description  Returns a single asset by ID. Non-owners need share access.
// @Tags         Assets
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  assetResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id} [get]
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
		// Resolve the highest permission across a direct share and the collection
		// cascade, so a user who holds only a collection share can open the
		// individual asset (issue #839) and a collection editor is reported as
		// editor even when a lesser direct share also exists.
		perm, permErr := h.access.ResolveAssetPermission(r.Context(), id, user)
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

// getAssetContent handles GET /api/v1/portal/assets/{id}/content.
//
// @Summary      Get asset content
// @Description  Downloads the asset's binary content from S3.
// @Tags         Assets
// @Produce      octet-stream
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {file}  binary
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/content [get]
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

	blobserve.Serve(w, r, blobserve.Options{
		Name:        asset.Name,
		ContentType: cmp.Or(contentType, asset.ContentType),
		ModTime:     asset.UpdatedAt,
		Data:        data,
	})
}

// updateAssetContent handles PUT /api/v1/portal/assets/{id}/content.
//
// @Summary      Update asset content
// @Description  Uploads new binary content for the asset, creating a new version.
// @Tags         Assets
// @Accept       octet-stream
// @Produce      json
// @Param        id                path    string  true   "Asset ID"
// @Param        X-Change-Summary  header  string  false  "Change summary for the new version"
// @Param        body              body    []byte  true   "Raw file content"
// @Success      200  {object}  statusResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/content [put]
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

	// The asset's existing type is the declaration for the replacement. It
	// wins when specific, so editing a JSON asset keeps it JSON; when the
	// asset was stored under a generic type, the new content settles it.
	ct := ResolveContentType(asset.ContentType, data)

	versionID := uuid.New().String()
	versionedKey := fmt.Sprintf("portal/%s/%s/%s/content%s", asset.OwnerID, id, versionID, ExtensionForContentType(ct))

	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, versionedKey, data, ct); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload content")
		return
	}

	av := AssetVersion{
		ID:            versionID,
		AssetID:       id,
		S3Key:         versionedKey,
		S3Bucket:      asset.S3Bucket,
		ContentType:   ct,
		SizeBytes:     int64(len(data)),
		CreatedBy:     user.Email,
		ChangeSummary: changeSummaryFromHeader(r, "Content updated"),
	}

	if _, err := h.deps.VersionStore.CreateVersion(r.Context(), av); err != nil {
		h.cleanupOrphanedS3(r.Context(), asset.S3Bucket, versionedKey)
		writeError(w, http.StatusInternalServerError, "failed to create version")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: statusUpdated})
}

// uploadThumbnail handles PUT /api/v1/portal/assets/{id}/thumbnail.
//
// @Summary      Upload asset thumbnail
// @Description  Uploads a PNG thumbnail image for the asset.
// @Tags         Assets
// @Accept       png
// @Produce      json
// @Param        id       path   string  true   "Asset ID"
// @Param        variant  query  string  false  "Thumbnail variant"  Enums(light, dark)
// @Param        body     body   []byte  true   "PNG image data"
// @Success      200  {object}  statusResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/thumbnail [put]
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
	if mediaType != mimeTypePNG {
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

	variant, ok := parseThumbnailVariant(w, r)
	if !ok {
		return
	}

	thumbKey := DeriveThumbnailKeyVariant(asset.S3Key, variant)
	if err := h.deps.S3Client.PutObject(r.Context(), asset.S3Bucket, thumbKey, data, mimeTypePNG); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload thumbnail")
		return
	}

	id := r.PathValue(pathKeyID)
	updates := AssetUpdate{ThumbnailS3Key: &thumbKey}
	if variant == thumbnailVariantDark {
		updates = AssetUpdate{ThumbnailDarkS3Key: &thumbKey}
	}
	if err := h.deps.AssetStore.Update(r.Context(), id, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update asset metadata")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: statusUpdated})
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

// getThumbnail handles GET /api/v1/portal/assets/{id}/thumbnail.
//
// @Summary      Get asset thumbnail
// @Description  Downloads the asset's PNG thumbnail image. The dark variant
// @Description  falls back to the light/default thumbnail when none was captured.
// @Tags         Assets
// @Produce      png
// @Param        id       path   string  true   "Asset ID"
// @Param        variant  query  string  false  "Thumbnail variant"  Enums(light, dark)
// @Success      200  {file}  binary
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/thumbnail [get]
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

	variant, ok := parseThumbnailVariant(w, r)
	if !ok {
		return
	}

	thumbKey := resolveThumbnailKey(asset, variant)
	if thumbKey == "" {
		writeError(w, http.StatusNotFound, "no thumbnail available")
		return
	}

	if h.deps.S3Client == nil {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	data, _, err := h.deps.S3Client.GetObject(r.Context(), asset.S3Bucket, thumbKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retrieve thumbnail")
		return
	}

	// canViewAsset decided this on the caller, so the bytes may sit in that
	// caller's browser cache and nowhere a second caller can reach.
	blobserve.CachePrivate(w, time.Hour)
	blobserve.Serve(w, r, blobserve.Options{
		Name:        asset.ID + ".png",
		ContentType: mimeTypePNG,
		ModTime:     asset.UpdatedAt,
		Data:        data,
	})
}

// Thumbnail variant identifiers and the S3 filenames they map to. Light is the
// default/shared variant (used by single-theme content and public shares); dark
// is captured only for content rendered on a forced background (markdown, CSV).
const (
	thumbnailVariantLight  = "light"
	thumbnailVariantDark   = "dark"
	thumbnailLightFilename = "thumbnail.png"
	thumbnailDarkFilename  = "thumbnail_dark.png"
)

// parseThumbnailVariant reads the optional ?variant= query parameter. An empty
// value defaults to light. Any value other than "light" or "dark" is rejected
// with 400. Returns the normalized variant and true on success; on failure it
// writes the error response and returns false.
func parseThumbnailVariant(w http.ResponseWriter, r *http.Request) (string, bool) {
	switch r.URL.Query().Get("variant") {
	case "", thumbnailVariantLight:
		return thumbnailVariantLight, true
	case thumbnailVariantDark:
		return thumbnailVariantDark, true
	default:
		writeError(w, http.StatusBadRequest, "variant must be 'light' or 'dark'")
		return "", false
	}
}

// resolveThumbnailKey returns the stored S3 key to serve for the requested
// variant. The dark variant falls back to the light/default key when no dark
// variant has been captured (built-in-theme types only ever store light).
func resolveThumbnailKey(asset *Asset, variant string) string {
	if variant == thumbnailVariantDark && asset.ThumbnailDarkS3Key != "" {
		return asset.ThumbnailDarkS3Key
	}
	return asset.ThumbnailS3Key
}

// DeriveThumbnailKey replaces the filename in an S3 key with "thumbnail.png"
// (the light/default variant).
func DeriveThumbnailKey(s3Key string) string {
	return DeriveThumbnailKeyVariant(s3Key, thumbnailVariantLight)
}

// DeriveThumbnailKeyVariant replaces the filename in an S3 key with the
// thumbnail filename for the given variant ("dark" -> thumbnail_dark.png,
// anything else -> thumbnail.png).
func DeriveThumbnailKeyVariant(s3Key, variant string) string {
	filename := thumbnailLightFilename
	if variant == thumbnailVariantDark {
		filename = thumbnailDarkFilename
	}
	idx := strings.LastIndex(s3Key, "/")
	if idx < 0 {
		return filename
	}
	return s3Key[:idx+1] + filename
}

// updateAssetRequest is the request body for updating an asset.
type updateAssetRequest struct {
	Name        *string  `json:"name,omitempty" example:"Q4 Revenue Report"`
	Description *string  `json:"description,omitempty" example:"Updated quarterly revenue analysis"`
	Tags        []string `json:"tags,omitempty" example:"finance,quarterly"`
}

// updateAsset handles PUT /api/v1/portal/assets/{id}.
//
// @Summary      Update asset metadata
// @Description  Updates the asset's name, description, or tags. Only the owner can update.
// @Tags         Assets
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "Asset ID"
// @Param        body  body  updateAssetRequest  true  "Fields to update"
// @Success      200  {object}  statusResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id} [put]
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
	if asset.DeletedAt != nil {
		writeError(w, http.StatusGone, errAssetDeleted)
		return
	}
	// Owner or an active editor share may update metadata, matching the content
	// update path (updateAssetContent): an editor can edit a shared asset.
	if !h.canEditAsset(w, r, id, asset, user) {
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

	writeJSON(w, http.StatusOK, statusResponse{Status: statusUpdated})
}

// deleteAsset handles DELETE /api/v1/portal/assets/{id}.
//
// @Summary      Delete asset
// @Description  Soft-deletes an asset. Only the owner can delete.
// @Tags         Assets
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  statusResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id} [delete]
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

	writeJSON(w, http.StatusOK, statusResponse{Status: statusDeleted})
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
			"bucket", bucket, "key", key, logKeyError, err)
	}
}

// --- Version handlers ---

// listVersions handles GET /api/v1/portal/assets/{id}/versions.
//
// @Summary      List asset versions
// @Description  Returns paginated version history for an asset.
// @Tags         Assets
// @Produce      json
// @Param        id      path   string   true   "Asset ID"
// @Param        limit   query  integer  false  "Results per page (default: 20)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/versions [get]
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

// getVersionContent handles GET /api/v1/portal/assets/{id}/versions/{version}/content.
//
// @Summary      Get version content
// @Description  Downloads the binary content of a specific asset version.
// @Tags         Assets
// @Produce      octet-stream
// @Param        id       path  string   true  "Asset ID"
// @Param        version  path  integer  true  "Version number"
// @Success      200  {file}  binary
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/versions/{version}/content [get]
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

	versionNum, err := strconv.Atoi(r.PathValue(keyVersion))
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
	blobserve.Serve(w, r, blobserve.Options{
		Name:        asset.Name,
		ContentType: cmp.Or(contentType, ver.ContentType),
		ModTime:     ver.CreatedAt,
		Data:        data,
	})
}

// revertToVersion handles POST /api/v1/portal/assets/{id}/versions/{version}/revert.
//
// @Summary      Revert to version
// @Description  Reverts the asset content to a specific version by creating a new version with that content.
// @Tags         Assets
// @Produce      json
// @Param        id       path  string   true  "Asset ID"
// @Param        version  path  integer  true  "Version number to revert to"
// @Success      200  {object}  map[string]any
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/versions/{version}/revert [post]
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

	versionNum, err := strconv.Atoi(r.PathValue(keyVersion))
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
		"status":   statusReverted,
		keyVersion: assignedVersion,
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
	// ExpiresIn is a duration string ("24h") bounding a link share's life. It
	// applies to link shares only: a share addressed to a person is access
	// granted to that person and is revoked, not timed out.
	ExpiresIn        string  `json:"expires_in,omitempty" example:"24h"`
	SharedWithUserID string  `json:"shared_with_user_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	SharedWithEmail  string  `json:"shared_with_email,omitempty" example:"colleague@example.com"`
	HideExpiration   bool    `json:"hide_expiration,omitempty" example:"false"`
	NoticeText       *string `json:"notice_text,omitempty" example:"Confidential"` // nil = default, "" = hidden, custom = as-is
	Permission       string  `json:"permission,omitempty" example:"viewer"`        // "viewer" (default) or "editor"
	// AccessMode is "restricted", "authenticated", or "public". Empty means
	// the default for the share's shape: restricted when a recipient is
	// named, authenticated otherwise. "public" is never implied.
	AccessMode string `json:"access_mode,omitempty" example:"restricted"`
	// Notify controls whether a named recipient gets a "shared with you"
	// email. nil (omitted) means notify -- the default a share carries when
	// nobody says otherwise. false shares quietly. The recipient's own
	// notification preferences still apply when it is true; this only removes
	// the sharer's ability to force one.
	Notify *bool `json:"notify,omitempty" example:"true"`
	// Message is an optional plain-text note from the sharer, delivered in the
	// notification email and stored nowhere. Markup and links are rejected
	// (ValidateShareMessage) whatever the share's shape, so a malformed note
	// is reported rather than silently dropped; a note on a share that sends
	// no email is accepted and then goes nowhere.
	Message string `json:"message,omitempty" example:"Here's the Q3 revenue breakdown you asked about"`
}

// wantsNotify reports whether a share created from this request should notify
// its recipient. Omitted means yes.
func (req createShareRequest) wantsNotify() bool {
	return req.Notify == nil || *req.Notify
}

// normalizeRecipient reduces the request's recipient to the bare address it
// names, so every later comparison (self-share checks, view-time matching,
// the notification recipient) sees one spelling. An empty recipient stays
// empty: that is a link share, not an invalid address.
func (req *createShareRequest) normalizeRecipient() error {
	if strings.TrimSpace(req.SharedWithEmail) == "" {
		req.SharedWithEmail = ""
		return nil
	}
	addr, err := portaldomain.ParseEmail(req.SharedWithEmail)
	if err != nil {
		return err //nolint:wrapcheck // message is the verbatim 400 body
	}
	req.SharedWithEmail = addr
	return nil
}

// shareResponse is the response for a created share.
type shareResponse struct {
	Share    Share  `json:"share"`
	ShareURL string `json:"share_url,omitempty" example:"https://platform.example.com/portal/view/abc123"`
}

// createShare handles POST /api/v1/portal/assets/{id}/shares.
//
// @Summary      Create asset share
// @Description  Creates a share link or user-targeted share for an asset. Only the owner can share.
// @Tags         Shares
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "Asset ID"
// @Param        body  body  createShareRequest  true  "Share configuration"
// @Success      201  {object}  shareResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/shares [post]
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

	if req.wantsNotify() {
		h.notifyShare(r.Context(), &share, ShareEvent{
			Kind: "asset", ItemID: asset.ID, ItemTitle: asset.Name, Message: req.Message,
		})
	}

	resp := shareResponse{Share: share}
	if h.deps.PublicBaseURL != "" {
		resp.ShareURL = fmt.Sprintf("%s/portal/view/%s", h.deps.PublicBaseURL, share.Token)
	}

	writeJSON(w, http.StatusCreated, resp)
}

// shareTarget identifies what a share is for: an asset, a collection, or a prompt.
type shareTarget struct {
	AssetID      string
	CollectionID string
	PromptID     string
}

// buildShare validates the request and constructs a Share, returning an error for invalid input.
func buildShare(target shareTarget, createdBy string, req createShareRequest) (Share, error) {
	token, err := GenerateShareToken()
	if err != nil {
		return Share{}, errors.New("failed to generate share token")
	}

	if err := req.normalizeRecipient(); err != nil {
		return Share{}, err
	}
	email := req.SharedWithEmail
	if err := ValidateShareMessage(req.Message); err != nil {
		return Share{}, err
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

	mode, modeErr := shareaccess.Resolve(req.AccessMode, email != "" || req.SharedWithUserID != "")
	if modeErr != nil {
		return Share{}, modeErr //nolint:wrapcheck // message is the verbatim 400 body the caller must act on
	}

	share := Share{
		ID:               uuid.New().String(),
		AssetID:          target.AssetID,
		CollectionID:     target.CollectionID,
		PromptID:         target.PromptID,
		Token:            token,
		CreatedBy:        createdBy,
		SharedWithUserID: req.SharedWithUserID,
		SharedWithEmail:  email,
		Permission:       perm,
		AccessMode:       mode,
		HideExpiration:   req.HideExpiration,
		NoticeText:       noticeText,
	}

	// A share addressed to a person grants that person access; it ends when
	// the owner revokes it, not on a clock. Expiry belongs to link shares,
	// where the URL is the credential and a bounded life limits what a
	// forwarded or leaked link is worth. Setting both is a contradiction, so
	// it is refused rather than silently resolved either way.
	if req.ExpiresIn != "" {
		if email != "" || req.SharedWithUserID != "" {
			return Share{}, errors.New("expires_in does not apply to a share addressed to a person; revoke the share to end access")
		}
		dur, parseErr := time.ParseDuration(req.ExpiresIn)
		if parseErr != nil {
			return Share{}, errors.New("invalid expires_in duration")
		}
		exp := time.Now().Add(dur)
		share.ExpiresAt = &exp
	}

	return share, nil
}

// listShares handles GET /api/v1/portal/assets/{id}/shares.
//
// @Summary      List asset shares
// @Description  Returns all shares for an asset. Only the owner can view shares.
// @Tags         Shares
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {array}   Share
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/shares [get]
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

// revokeShare handles DELETE /api/v1/portal/shares/{id}.
//
// @Summary      Revoke share
// @Description  Revokes a share by its ID. Only the owner can revoke.
// @Tags         Shares
// @Produce      json
// @Param        id  path  string  true  "Share ID"
// @Success      200  {object}  statusResponse
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/shares/{id} [delete]
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

	switch {
	case share.PromptID != "":
		if h.deps.PromptStore == nil {
			writeError(w, http.StatusNotFound, "associated prompt not found")
			return
		}
		pr, err := h.deps.PromptStore.GetByID(r.Context(), share.PromptID)
		if err != nil || pr == nil {
			writeError(w, http.StatusNotFound, "associated prompt not found")
			return
		}
		if pr.OwnerEmail != user.Email {
			writeError(w, http.StatusForbidden, "only the owner can revoke this share")
			return
		}
	case share.CollectionID != "":
		if err := h.verifyCollectionOwner(r.Context(), share.CollectionID, user); err != nil {
			writeError(w, err.code, err.message)
			return
		}
	default:
		asset, err := h.deps.AssetStore.Get(r.Context(), share.AssetID)
		if err != nil {
			writeError(w, http.StatusNotFound, "associated asset not found")
			return
		}
		if asset.OwnerID != user.UserID {
			writeError(w, http.StatusForbidden, "only the owner can revoke this share")
			return
		}
	}

	if err := h.deps.ShareStore.Revoke(r.Context(), shareID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke share")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: statusRevoked})
}

type ownerError struct {
	code    int
	message string
}

func (e *ownerError) Error() string { return e.message }

func (h *Handler) verifyCollectionOwner(ctx context.Context, collectionID string, user *User) *ownerError {
	if h.deps.CollectionStore == nil {
		return &ownerError{http.StatusNotFound, "collections not available"}
	}
	coll, err := h.deps.CollectionStore.Get(ctx, collectionID)
	if err != nil {
		return &ownerError{http.StatusNotFound, "associated collection not found"}
	}
	if coll.OwnerID != user.UserID {
		return &ownerError{http.StatusForbidden, "only the owner can revoke this share"}
	}
	return nil
}

// listSharedWithMe handles GET /api/v1/portal/shared-with-me.
//
// @Summary      List assets shared with me
// @Description  Returns paginated assets that other users have shared with the current user.
// @Tags         Shares
// @Produce      json
// @Param        limit   query  integer  false  "Results per page (default: 20)"
// @Param        offset  query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/shared-with-me [get]
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

// getActivityOverview handles GET /api/v1/portal/activity/overview.
//
// @Summary      Get activity overview
// @Description  Returns aggregate activity metrics for the current user within an optional time range.
// @Tags         Activity
// @Produce      json
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Success      200  {object}  audit.Overview
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/activity/overview [get]
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

// getActivityTimeseries handles GET /api/v1/portal/activity/timeseries.
//
// @Summary      Get activity timeseries
// @Description  Returns time-bucketed activity data for the current user.
// @Tags         Activity
// @Produce      json
// @Param        resolution  query  string  false  "Bucket resolution: minute, hour, day (default: hour)"
// @Param        start_time  query  string  false  "Start time (RFC 3339)"
// @Param        end_time    query  string  false  "End time (RFC 3339)"
// @Success      200  {array}   audit.TimeseriesBucket
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/activity/timeseries [get]
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

// getActivityBreakdown handles GET /api/v1/portal/activity/breakdown.
//
// @Summary      Get activity breakdown
// @Description  Returns activity breakdown grouped by a dimension (tool_name, user_id, persona, toolkit_kind, or connection).
// @Tags         Activity
// @Produce      json
// @Param        group_by    query  string   false  "Grouping dimension (default: tool_name)"
// @Param        limit       query  integer  false  "Maximum entries to return"
// @Param        start_time  query  string   false  "Start time (RFC 3339)"
// @Param        end_time    query  string   false  "End time (RFC 3339)"
// @Success      200  {array}   audit.BreakdownEntry
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/activity/breakdown [get]
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

// listMyInsights handles GET /api/v1/portal/knowledge/insights.
//
// @Summary      List my insights
// @Description  Returns paginated insights captured by the current user.
// @Tags         Knowledge
// @Produce      json
// @Param        status    query  string   false  "Filter by status"
// @Param        category  query  string   false  "Filter by category"
// @Param        limit     query  integer  false  "Results per page (default: 20)"
// @Param        offset    query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge/insights [get]
func (h *Handler) listMyInsights(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	filter := knowledge.InsightFilter{
		// Insights are owned by email (see capture_insight and the 000031
		// migration), matching how memories are scoped below.
		CapturedBy: user.Email,
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

// getMyInsightStats handles GET /api/v1/portal/knowledge/insights/stats.
//
// @Summary      Get my insight stats
// @Description  Returns aggregate statistics for the current user's insights.
// @Tags         Knowledge
// @Produce      json
// @Success      200  {object}  knowledge.InsightStats
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge/insights/stats [get]
func (h *Handler) getMyInsightStats(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	stats, err := h.deps.InsightStore.Stats(r.Context(), knowledge.InsightFilter{
		// Owned by email, consistent with listMyInsights and capture_insight.
		CapturedBy: user.Email,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query insight stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// --- Memory handlers ---

// memoryStatsResponse holds aggregated memory statistics for a user.
type memoryStatsResponse struct {
	Total       int            `json:"total" example:"150"`
	ByDimension map[string]int `json:"by_dimension"`
	ByCategory  map[string]int `json:"by_category"`
	ByStatus    map[string]int `json:"by_status"`
}

// listMyMemories handles GET /api/v1/portal/memory/records.
//
// @Summary      List my memory records
// @Description  Returns paginated memory records for the current user with optional filtering.
// @Tags         Memory
// @Produce      json
// @Param        dimension  query  string   false  "Filter by dimension"
// @Param        category   query  string   false  "Filter by category"
// @Param        status     query  string   false  "Filter by status"
// @Param        source     query  string   false  "Filter by source"
// @Param        limit      query  integer  false  "Results per page (default: 20)"
// @Param        offset     query  integer  false  "Offset for pagination (default: 0)"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/memory/records [get]
func (h *Handler) listMyMemories(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	q := r.URL.Query()
	filter := memory.Filter{
		CreatedBy: user.Email,
		Dimension: q.Get("dimension"),
		SinkClass: q.Get("sink_class"),
		Category:  q.Get("category"),
		Status:    q.Get("status"),
		Source:    q.Get("source"),
		Limit:     intParam(r, paramLimit, memory.DefaultLimit),
		Offset:    intParam(r, paramOffset, 0),
	}

	records, total, err := h.deps.MemoryStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memory records")
		return
	}

	if records == nil {
		records = []memory.Record{}
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: records, Total: total,
		Limit: filter.EffectiveLimit(), Offset: filter.Offset,
	})
}

// getMyMemoryStats handles GET /api/v1/portal/memory/records/stats.
//
// @Summary      Get my memory stats
// @Description  Returns aggregated memory statistics grouped by dimension, category, and status.
// @Tags         Memory
// @Produce      json
// @Success      200  {object}  memoryStatsResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/memory/records/stats [get]
func (h *Handler) getMyMemoryStats(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	// Fetch all records for the user across all pages to build accurate stats.
	filter := memory.Filter{
		CreatedBy: user.Email,
		Limit:     memory.MaxLimit,
	}

	stats := memoryStatsResponse{
		ByDimension: make(map[string]int),
		ByCategory:  make(map[string]int),
		ByStatus:    make(map[string]int),
	}

	for {
		records, total, err := h.deps.MemoryStore.List(r.Context(), filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to query memory stats")
			return
		}
		if stats.Total == 0 {
			stats.Total = total
		}
		for _, rec := range records {
			stats.ByDimension[rec.Dimension]++
			stats.ByCategory[rec.Category]++
			stats.ByStatus[rec.Status]++
		}
		if len(records) < memory.MaxLimit {
			break
		}
		filter.Offset += memory.MaxLimit
	}

	writeJSON(w, http.StatusOK, stats)
}

// scoredMemoryRecord is a memory record plus its search relevance score.
// The embedded record preserves the same JSON shape as the list endpoint,
// so the only addition the client sees is the "score" field.
type scoredMemoryRecord struct {
	memory.Record
	Score float64 `json:"score"`
}

// scoredInsightRecord is an insight plus its search relevance score,
// embedding the insight to preserve the list endpoint's JSON shape.
type scoredInsightRecord struct {
	knowledge.Insight
	Score float64 `json:"score"`
}

// searchMyMemories handles GET /api/v1/portal/memory/records/search.
//
// @Summary      Search my memory records
// @Description  Ranks the current user's memory records by relevance to q. Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Always scoped server-side to the requesting user.
// @Tags         Memory
// @Produce      json
// @Param        q          query  string   true   "Search query"
// @Param        dimension  query  string   false  "Filter by dimension"
// @Param        status     query  string   false  "Filter by status"
// @Param        limit      query  integer  false  "Max results (default: 20)"
// @Success      200  {object}  paginatedResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/memory/records/search [get]
func (h *Handler) searchMyMemories(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	// Email is the sole server-side scoping key. An empty email would make
	// scopeFilters omit the created_by predicate and return every user's
	// records, so fail closed rather than run an unscoped search (#516).
	if user.Email == "" {
		writeError(w, http.StatusForbidden, errSearchScopeRequired)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit := intParam(r, paramLimit, memory.DefaultLimit)
	emb := h.embedSearchQuery(r.Context(), query)

	var (
		scored []memory.ScoredRecord
		err    error
	)
	if len(emb) > 0 {
		scored, err = h.deps.MemoryStore.HybridSearch(r.Context(), memory.HybridQuery{
			Embedding: emb,
			QueryText: query,
			CreatedBy: user.Email,
			Dimension: r.URL.Query().Get("dimension"),
			Status:    r.URL.Query().Get("status"),
			Limit:     limit,
		})
	} else {
		scored, err = h.deps.MemoryStore.LexicalSearch(r.Context(), memory.LexicalQuery{
			QueryText: query,
			CreatedBy: user.Email,
			Dimension: r.URL.Query().Get("dimension"),
			Status:    r.URL.Query().Get("status"),
			Limit:     limit,
		})
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search memory records")
		return
	}

	results := make([]scoredMemoryRecord, 0, len(scored))
	for i := range scored {
		results = append(results, scoredMemoryRecord{Record: scored[i].Record, Score: scored[i].Score})
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: results, Total: len(results), Limit: limit, Offset: 0,
	})
}

// searchMyInsights handles GET /api/v1/portal/knowledge/insights/search.
//
// @Summary      Search my knowledge insights
// @Description  Ranks the current user's knowledge insights by relevance to q. Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Always scoped server-side to the requesting user.
// @Tags         Knowledge
// @Produce      json
// @Param        q       query  string   true   "Search query"
// @Param        status  query  string   false  "Filter by insight status"
// @Param        limit   query  integer  false  "Max results (default: 20)"
// @Success      200  {object}  paginatedResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/knowledge/insights/search [get]
func (h *Handler) searchMyInsights(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	// The route is only registered when the store implements InsightSearcher,
	// so this assertion holds; guard defensively regardless.
	if user.Email == "" {
		writeError(w, http.StatusForbidden, errSearchScopeRequired)
		return
	}

	searcher, ok := h.deps.InsightStore.(InsightSearcher)
	if !ok {
		writeError(w, http.StatusNotFound, "knowledge search not available")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit := intParam(r, paramLimit, knowledge.DefaultLimit)
	scored, err := searcher.Search(r.Context(), knowledge.InsightSearchQuery{
		QueryText:  query,
		Embedding:  h.embedSearchQuery(r.Context(), query),
		CapturedBy: user.Email,
		Status:     r.URL.Query().Get("status"),
		Limit:      limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search insights")
		return
	}

	results := make([]scoredInsightRecord, 0, len(scored))
	for i := range scored {
		results = append(results, scoredInsightRecord{Insight: scored[i].Insight, Score: scored[i].Score})
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: results, Total: len(results), Limit: limit, Offset: 0,
	})
}

// embedSearchQuery returns the query embedding for the portal's memory,
// knowledge, and asset relevance search, or nil to signal the lexical-only
// fallback. It delegates to embedding.EmbedForSearch so the portal and the
// agent surfaces (memory_recall, recall_insight) make one shared hybrid-vs-
// lexical decision and cannot drift.
func (h *Handler) embedSearchQuery(ctx context.Context, query string) []float32 {
	return embedding.EmbedForSearch(ctx, h.deps.EmbeddingProvider, query)
}

// --- Helpers ---

// statusResponse is a generic status response.
type statusResponse struct {
	Status string `json:"status" example:"updated"`
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

// GenerateShareToken generates a cryptographically random hex token for share
// links. Exported so out-of-package share creators (e.g. the export adapters)
// mint tokens with the same length and encoding as portal-issued shares.
func GenerateShareToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// canViewAsset checks owner or any share access, writing an HTTP error on failure.
func (h *Handler) canViewAsset(w http.ResponseWriter, r *http.Request, assetID string, asset *Asset, user *User) bool {
	granted, err := h.access.AssetViewGrant(r.Context(), assetID, asset, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check share access")
		return false
	}
	if !granted {
		writeError(w, http.StatusForbidden, errAccessDenied)
	}
	return granted
}

// userCanViewAsset reports whether the user may view the asset without writing
// an HTTP response — the pure form of canViewAsset, for callers that resolve
// many entities in a loop.
func (h *Handler) userCanViewAsset(r *http.Request, assetID string, asset *Asset, user *User) bool {
	return h.access.CanViewAsset(r.Context(), assetID, asset, user)
}

// canEditAsset checks owner or editor share access, writing an HTTP error on failure.
func (h *Handler) canEditAsset(w http.ResponseWriter, r *http.Request, assetID string, asset *Asset, user *User) bool {
	if asset.OwnerID == user.UserID {
		return true
	}
	perm, err := h.access.ResolveAssetPermission(r.Context(), assetID, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check share access")
		return false
	}
	if perm == PermissionEditor {
		return true
	}
	writeError(w, http.StatusForbidden, "only the owner or an editor can update this asset")
	return false
}

// resolveSharePermission validates and resolves the permission for a new share.
// Public links (no user/email target) are always forced to viewer.
func resolveSharePermission(req createShareRequest, email string) (SharePermission, error) {
	perm := PermissionViewer
	if req.Permission != "" {
		if !ValidSharePermission(req.Permission) {
			return "", errors.New("invalid permission: must be viewer or editor")
		}
		perm = SharePermission(req.Permission)
	}
	if email == "" && req.SharedWithUserID == "" {
		perm = PermissionViewer
	}
	return perm, nil
}

// copyAsset handles POST /api/v1/portal/assets/{id}/copy.
//
// @Summary      Copy asset
// @Description  Creates an independent copy of a shared asset in the current user's My Assets.
// @Tags         Assets
// @Produce      json
// @Param        id  path  string  true  "Asset ID to copy"
// @Success      201  {object}  Asset
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/copy [post]
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
				"asset_id", newID, logKeyError, err)
		}
	}

	return &newAsset, nil
}

// createAssetRequest is the request body for creating an asset from inline content.
type createAssetRequest struct {
	Name        string   `json:"name" example:"My Prompt"`
	Description string   `json:"description,omitempty" example:"Saved from prompt library"`
	ContentType string   `json:"content_type" example:"text/markdown"`
	Content     string   `json:"content" example:"# Prompt content"`
	Tags        []string `json:"tags,omitempty"`
}

// validateCreateAssetRequest validates the request body and returns the
// normalized name and content type, or an httpError with the appropriate
// status code. Extracted from createAsset to keep its cyclomatic complexity
// within the project's ≤10 limit.
func validateCreateAssetRequest(req createAssetRequest) (name, contentType string, err *httpError) {
	name = strings.TrimSpace(req.Name)
	if vErr := ValidateAssetName(name); vErr != nil {
		return "", "", &httpError{http.StatusBadRequest, vErr.Error()}
	}
	if strings.TrimSpace(req.ContentType) == "" {
		return "", "", &httpError{http.StatusBadRequest, "content_type is required"}
	}
	if req.Content == "" {
		return "", "", &httpError{http.StatusBadRequest, "content is required"}
	}
	// Detection runs before the allowlist check, so a caller that declared a
	// generic type is admitted or refused on what the content actually is —
	// the same type the asset will be stored and rendered under. The check
	// itself is ValidateContentType, shared with the tool paths.
	contentType = ResolveContentType(req.ContentType, []byte(req.Content))
	if vErr := ValidateContentType(contentType); vErr != nil {
		return "", "", &httpError{http.StatusUnsupportedMediaType, vErr.Error()}
	}
	if int64(len(req.Content)) > MaxContentUploadBytes {
		return "", "", &httpError{http.StatusRequestEntityTooLarge, "content exceeds 10 MB limit"}
	}
	if vErr := ValidateDescription(strings.TrimSpace(req.Description)); vErr != nil {
		return "", "", &httpError{http.StatusBadRequest, vErr.Error()}
	}
	if vErr := ValidateTags(req.Tags); vErr != nil {
		return "", "", &httpError{http.StatusBadRequest, vErr.Error()}
	}
	return name, contentType, nil
}

// createAsset handles POST /api/v1/portal/assets.
//
// @Summary      Create asset from inline content
// @Description  Creates a new asset by uploading inline text content (markdown, HTML, SVG, JSON, etc.). Use this to snapshot generated content into the asset portal without going through the MCP save_asset tool.
// @Tags         Assets
// @Accept       json
// @Produce      json
// @Param        body  body  createAssetRequest  true  "Asset content and metadata"
// @Success      201  {object}  Asset
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      413  {object}  problemDetail
// @Failure      415  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets [post]
func (h *Handler) createAsset(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	var req createAssetRequest
	// LimitReader cap = content cap + 64 KB headroom for JSON wrapper and metadata
	// (name 255 + description 2000 + 20×100 tags + JSON keys/quotes ≈ ~5 KB worst-case).
	// Without sufficient headroom, a legitimate near-cap content with max metadata
	// would be truncated mid-JSON and return 400 instead of either 201 or 413.
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxContentUploadBytes+64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name, ct, verr := validateCreateAssetRequest(req)
	if verr != nil {
		writeError(w, verr.code, verr.msg)
		return
	}

	if !h.versionedStorageReady() {
		writeError(w, http.StatusServiceUnavailable, errStorageNotReady)
		return
	}

	ctx := r.Context()
	newID := uuid.New().String()
	s3Key := fmt.Sprintf("portal/%s/%s/content%s", user.UserID, newID, ExtensionForContentType(ct))
	data := []byte(req.Content)

	if err := h.deps.S3Client.PutObject(ctx, h.deps.S3Bucket, s3Key, data, ct); err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to upload content")
		return
	}

	tags := append([]string(nil), req.Tags...)
	newAsset := Asset{
		ID:          newID,
		OwnerID:     user.UserID,
		OwnerEmail:  user.Email,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		ContentType: ct,
		S3Bucket:    h.deps.S3Bucket,
		S3Key:       s3Key,
		SizeBytes:   int64(len(data)),
		Tags:        tags,
	}

	if err := h.deps.AssetStore.Insert(ctx, newAsset); err != nil {
		h.cleanupOrphanedS3(ctx, h.deps.S3Bucket, s3Key)
		writeError(w, http.StatusInternalServerError, "failed to create asset")
		return
	}

	v1 := AssetVersion{
		ID:            uuid.New().String(),
		AssetID:       newID,
		S3Key:         s3Key,
		S3Bucket:      h.deps.S3Bucket,
		ContentType:   ct,
		SizeBytes:     int64(len(data)),
		CreatedBy:     user.Email,
		ChangeSummary: "Initial version",
	}
	if _, err := h.deps.VersionStore.CreateVersion(ctx, v1); err != nil {
		slog.Warn("failed to create initial version for new asset", // #nosec G706 -- structured log, not user-facing
			"asset_id", newID, logKeyError, err)
	}

	writeJSON(w, http.StatusCreated, newAsset)
}
