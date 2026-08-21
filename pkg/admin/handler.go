// Package admin provides REST API endpoints for administrative operations.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	httpswagger "github.com/swaggo/http-swagger/v2"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/pkcestore"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/personastore"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// PersonaRegistry abstracts persona.Registry for testability.
type PersonaRegistry interface {
	All() []*persona.Persona
	Get(name string) (*persona.Persona, bool)
	Register(p *persona.Persona) error
	Unregister(name string) error
}

// APIKeyManager manages API keys at runtime.
type APIKeyManager interface {
	ListKeys() []auth.APIKeySummary
	GenerateKey(def auth.APIKey) (string, error)
	RemoveByName(name string) bool
}

// PromptRegistrar registers/unregisters prompts with the live MCP server.
type PromptRegistrar interface {
	RegisterRuntimePrompt(p *prompt.Prompt)
	UnregisterRuntimePrompt(name string)
}

// PromptInfoProvider returns metadata about platform-registered prompts
// (auto, workflow, toolkit, custom config). These are system prompts not
// stored in the database.
type PromptInfoProvider interface {
	AllPromptInfos() []registry.PromptInfo
}

// ReloadNotifier announces an admin-side configuration change to peer
// replicas so their in-memory state is rebuilt (issue #501). The handler
// reloads the local replica synchronously as before, then calls the
// matching method here to broadcast the change. Implemented by
// *platform.Platform; nil on single-replica or test setups, in which
// case the broadcast is skipped.
type ReloadNotifier interface {
	PublishCatalogReload(catalogID string)
	PublishConnectionReload(kind, name string, op platform.ConnectionReloadOp)
	PublishPersonaReload()
	PublishAPIKeyReload()
}

// ToolkitRegistry abstracts registry.Registry for testability.
type ToolkitRegistry interface {
	All() []registry.Toolkit
	AllTools() []string
	GetToolkitForTool(toolName string) registry.ToolkitMatch
}

// ConfigStore abstracts configstore.Store for testability.
type ConfigStore interface {
	Get(ctx context.Context, key string) (*configstore.Entry, error)
	Set(ctx context.Context, key, value, author string) error
	Delete(ctx context.Context, key, author string) error
	List(ctx context.Context) ([]configstore.Entry, error)
	Changelog(ctx context.Context, limit, offset int) ([]configstore.ChangelogEntry, int, error)
	Mode() string
}

// Deps holds dependencies for the admin handler.
type Deps struct {
	Config              *platform.Config
	ConfigStore         ConfigStore
	FileDefaults        map[string]string
	PersonaRegistry     PersonaRegistry
	ToolkitRegistry     ToolkitRegistry
	ReloadNotifier      ReloadNotifier
	MCPServer           *mcp.Server
	AuditQuerier        AuditQuerier
	AuditMetricsQuerier AuditMetricsQuerier
	// SessionViewer reads sessions off the audit log: the same history
	// AuditQuerier serves, grouped by the session id every event already
	// carries. nil leaves the /api/v1/admin/sessions routes unregistered.
	SessionViewer SessionViewer
	// CallCatalog is the record of every data-access call the platform made
	// (#1321), and the review queue of the ones worth publishing. nil leaves
	// the /api/v1/admin/calls routes unregistered.
	CallCatalog CallCatalog
	// CallPromoter publishes a reviewed record. nil leaves the promote and
	// reject actions unregistered.
	CallPromoter      *CallPromoter
	Knowledge         *KnowledgeHandler
	APIKeyManager     APIKeyManager
	BrowserAuth       *browsersession.Authenticator
	DatabaseAvailable bool
	PlatformTools     []platform.ToolInfo
	AssetStore        portal.AssetStore
	ShareStore        portal.ShareStore
	VersionStore      portal.VersionStore
	// CollectionStore backs the admin asset-collection routes (#1292). nil
	// leaves them unregistered, as a deployment without a database has no
	// collections to serve.
	CollectionStore   portal.CollectionStore
	S3Client          portal.S3Client
	S3Bucket          string
	ConnectionStore   ConnectionStore
	ConnectionSources *platform.ConnectionSourceMap
	ToolkitsConfig    map[string]any
	PersonaStore      personastore.Store
	APIKeyStore       platform.APIKeyStore
	// UserStore is the known-users directory (#614). nil disables the
	// /api/v1/admin/users routes (no database configured).
	UserStore          user.Store
	PromptStore        prompt.Store
	PromptRegistrar    PromptRegistrar
	PromptInfoProvider PromptInfoProvider
	FilePersonaNames   map[string]bool
	EnrichmentStore    EnrichmentStore
	EnrichmentEngine   EnrichmentEngine
	// PKCEStore holds in-flight authorization_code+PKCE state between
	// oauth-start and the callback. Required for the OAuth routes: there is
	// no fallback, and oauth-start answers 503 when this is nil. Use a
	// pkcestore.MemoryStore for single-replica deployments and a
	// pkcestore.PostgresStore for HA, where oauth-start and the callback may
	// land on different replicas.
	PKCEStore pkcestore.Store
	// ConnOAuthStore persists OAuth tokens for every connection kind
	// in one shared table (migration 000039's connection_oauth_tokens).
	// The unified connection OAuth handler reads and writes through
	// this; toolkit Authenticators read through it on every outbound
	// request. nil disables the unified OAuth routes.
	ConnOAuthStore connoauth.Store
	// OAuthKinds maps connection kind ("mcp", "api", future kinds) to
	// the per-kind config extractor + post-auth side-effect hook. The
	// unified handler dispatches on the {kind} path parameter through
	// this registry. New connection kinds register here at startup —
	// they do NOT add parallel handler files or token stores.
	OAuthKinds OAuthKindHandlers
	// AuthEvents writes the durable per-connection OAuth-lifecycle
	// audit trail (connect, refresh, rotation, revocation, deletion).
	// nil disables event writes — the admin endpoints still work; the
	// History panel and "what killed my token at 17:54" diagnostics
	// just won't have data. Set this when ConnOAuthStore is set; a
	// platform with no DB has no place to write events anyway.
	AuthEvents *authevents.Writer
	// AuthEventStore is the read surface exposed via the History
	// admin endpoint. Distinct from AuthEvents because writes are
	// nil-safe (best-effort) while reads need a real implementation
	// to return 200 instead of an empty list.
	AuthEventStore authevents.Store
	// APICatalogStore manages OpenAPI spec bundles referenced by
	// api-kind connections via config.catalog_id. nil disables the
	// /api/v1/admin/api-catalogs routes; connection saves still
	// succeed but catalog_id is unvalidated. Wire this in lockstep
	// with the apigateway toolkit's SetCatalogStore so admin writes
	// and toolkit reads share one store.
	APICatalogStore APICatalogStore
	// Embedder is the embedding provider used by the api-catalog
	// admin path to compute per-operation vectors at spec-upsert
	// time. Nil disables the compute-and-store step: spec writes
	// still succeed, but the api-gateway toolkit's semantic and
	// hybrid ranking modes fall back to lexical until the operator
	// wires an embedder and re-saves (or re-embeds) the spec.
	Embedder embedding.Provider

	// EmbedJobs is the Postgres-backed job queue for api-catalog
	// embedding work. The admin handler enqueues jobs on every
	// spec write, cancels them on spec/catalog delete (#998), and
	// lets the worker / reconciler / reaper run the actual
	// embedding pass off the request path. nil when the platform
	// was built without a database; spec writes still succeed in
	// that mode but no embeddings are persisted.
	EmbedJobs catalogindex.Store

	// IndexJobs is the cross-kind read + command surface for the admin
	// Indexing dashboard (per-kind job-state counts, coverage, the job
	// list / drill-down, and the manual re-index action). It serves
	// every index_jobs consumer uniformly, so a new consumer gets
	// dashboard visibility for free. nil when no queue is wired (no
	// database or no configured embedding provider); the dashboard
	// then renders a degraded empty state instead of an error.
	IndexJobs IndexJobsService

	// NotificationSettings persists the admin SMTP configuration (#631).
	// nil disables the /api/v1/admin/settings/smtp routes.
	NotificationSettings smtp.SettingsStore
	// SendTestEmail delivers a test email through the stored SMTP
	// settings. nil disables the test-email route.
	SendTestEmail func(ctx context.Context, to string) error
	// NotificationPrefs reads per-address notification preferences so the
	// SMTP test-send UI can surface a target's opt-out state (#1022). nil
	// disables the recipient-status route.
	NotificationPrefs notification.PrefsStore
	// ReviewQueueAlert persists the knowledge review-queue staleness alert
	// threshold, cooldown, and recipients (#803). nil disables the
	// /api/v1/admin/settings/review-queue-alert routes.
	ReviewQueueAlert reviewalert.SettingsStore
	// NotificationHistory reads the delivery history behind the admin
	// Notifications tab: what was sent, what failed and why. nil disables
	// the /api/v1/admin/notifications routes.
	NotificationHistory NotificationHistory
	// NotificationRetention is how long a resolved queue row survives, shown
	// alongside the history so an admin reads it as a recent window rather
	// than a complete record. Zero omits the claim.
	NotificationRetention time.Duration
}

// IndexJobsService is the cross-kind index-jobs surface the admin
// Indexing dashboard consumes. Implemented by *indexjobs.Reporter over
// the shared queue store + kind registry; declared here so admin can
// mock it without depending on the queue's concrete types beyond its
// value structs.
type IndexJobsService interface {
	// Kinds returns every registered source kind, sorted.
	Kinds() []string
	// Counts returns the per-state job rollup for one source kind.
	Counts(ctx context.Context, kind string) (*indexjobs.KindCounts, error)
	// Coverage returns the indexed-vs-expected rollup for the kind, or
	// nil when the kind reports no coverage. Returns
	// indexjobs.ErrUnknownKind for an unregistered kind.
	Coverage(ctx context.Context, kind string) (*indexjobs.Coverage, error)
	// List returns jobs matching the filter, newest first. A zero-value
	// SourceKind lists across every kind.
	List(ctx context.Context, filter indexjobs.ListFilter) ([]indexjobs.Job, error)
	// ActiveFailures returns the units with open (unresolved) failures,
	// one entry per unit, most-recently-failed first. An empty kind
	// lists across every kind, which the cross-kind triage panel relies
	// on.
	ActiveFailures(ctx context.Context, kind string, limit int) ([]indexjobs.FailedUnit, error)
	// Reindex enqueues manual-retry jobs for the kind (a single unit
	// when sourceID is set, every out-of-sync unit otherwise) and
	// returns the source ids enqueued. Returns indexjobs.ErrUnknownKind
	// for an unregistered kind.
	Reindex(ctx context.Context, kind, sourceID string) ([]string, error)
	// Resolve dismisses every open failure for the unit, the explicit
	// fallback when a failure will never be superseded. Returns the
	// number of failed rows resolved (zero is not an error).
	Resolve(ctx context.Context, kind, sourceID string) (int, error)
}

// EnrichmentEngine is the admin-facing surface of an enrichment.Engine.
// Defined as a small interface here so tests don't need to construct a
// real Engine.
type EnrichmentEngine interface {
	Sources() *enrichment.SourceRegistry
}

// docsPrefix is the path prefix for the public Swagger UI.
const docsPrefix = "/api/v1/admin/docs/"

// publicPrefix is the path prefix for unauthenticated public endpoints.
const publicPrefix = "/api/v1/admin/public/"

// oauthCallbackPrefix is the URL the gateway's OAuth flow redirects back
// to. Lives outside publicPrefix because operators register the exact
// URL with their OAuth provider; renaming would break every connected
// upstream. Authenticated by the per-flow PKCE state, not by an admin
// session.
const oauthCallbackPrefix = "/api/v1/admin/oauth/"

// apiGatewayOAuthCallbackPrefix is the parallel callback URL for the
// HTTP API gateway's authorization-code flow. Kept distinct from the
// MCP gateway's prefix so an operator can read the URL and tell which
// connection family the redirect belongs to. Same admin-session
// bypass rationale as oauthCallbackPrefix — PKCE state, not session,
// authenticates the callback.
const apiGatewayOAuthCallbackPrefix = "/api/v1/admin/api-gateway/oauth/"

// Handler provides admin REST API endpoints.
type Handler struct {
	mux        *http.ServeMux
	publicMux  *http.ServeMux
	deps       Deps
	authMiddle func(http.Handler) http.Handler
	// toolsDenyMu serializes read-modify-write of the tools.deny config
	// entry across concurrent setToolVisibility calls. Without it, two
	// admins toggling visibility on different tools could each load the
	// same starting list, modify their own copy, and the second writer
	// overwrites the first — silently losing one of the changes (#343).
	//
	// In-process serialization is sufficient for single-replica
	// deployments. Multi-replica deployments would additionally need a
	// row-level DB lock or version-token CAS at the config_entries layer;
	// that's a separate concern not addressed here.
	toolsDenyMu sync.Mutex
}

// statusResponse is a generic status response.
type statusResponse struct {
	Status string `json:"status" example:"ok"`
}

// @title MCP Data Platform API
// @version 1.0
// @description REST API for the MCP Data Platform. Covers admin endpoints (system, config, personas, auth keys, audit, knowledge, memory, connections), portal endpoints (assets, collections, shares, prompts, activity), and resource management.
//
// @host localhost:8080
// @BasePath /api/v1
//
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

// NewHandler creates a new admin API handler.
func NewHandler(deps Deps, authMiddle func(http.Handler) http.Handler) *Handler {
	h := &Handler{
		mux:        http.NewServeMux(),
		publicMux:  http.NewServeMux(),
		deps:       deps,
		authMiddle: authMiddle,
	}
	h.registerRoutes()
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, docsPrefix) ||
		strings.HasPrefix(r.URL.Path, publicPrefix) ||
		strings.HasPrefix(r.URL.Path, oauthCallbackPrefix) ||
		strings.HasPrefix(r.URL.Path, apiGatewayOAuthCallbackPrefix) {
		h.publicMux.ServeHTTP(w, r)
		return
	}
	if h.authMiddle != nil {
		h.authMiddle(h.mux).ServeHTTP(w, r)
		return
	}
	h.mux.ServeHTTP(w, r)
}

// registerRoutes registers all admin API routes.
func (h *Handler) registerRoutes() {
	h.registerKnowledgeRoutes()
	h.registerSystemRoutes()
	h.registerAuditRoutes()
	h.registerSessionRoutes()
	h.registerCallRoutes()
	h.registerConfigRoutes()
	h.registerPersonaRoutes()
	h.registerAuthKeyRoutes()
	h.registerUserRoutes()
	h.registerAssetRoutes()
	h.registerCollectionRoutes()
	h.registerConnectionRoutes()
	h.registerCatalogRoutes()
	h.registerGatewayRoutes()
	h.registerConnectionOAuthRoutes()
	h.registerEnrichmentRoutes()
	h.registerPromptRoutes()
	h.registerIndexJobsRoutes()
	h.registerSettingsRoutes()
	h.registerNotificationRoutes()
}

// registerKnowledgeRoutes registers knowledge management endpoints or a
// 409 fallback when the feature is enabled in config but unavailable.
func (h *Handler) registerKnowledgeRoutes() {
	if h.deps.Knowledge != nil {
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/insights", h.deps.Knowledge.ListInsights)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/insights/stats", h.deps.Knowledge.GetStats)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/insights/{id}", h.deps.Knowledge.GetInsight)
		h.mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}/status", h.deps.Knowledge.UpdateInsightStatus)
		h.mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}", h.deps.Knowledge.UpdateInsight)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/changesets", h.deps.Knowledge.ListChangesets)
		h.mux.HandleFunc("GET /api/v1/admin/knowledge/changesets/{id}", h.deps.Knowledge.GetChangeset)
		h.mux.HandleFunc("POST /api/v1/admin/knowledge/changesets/{id}/rollback", h.deps.Knowledge.RollbackChangeset)
	} else if h.deps.Config != nil && (h.deps.Config.Knowledge.Enabled == nil || *h.deps.Config.Knowledge.Enabled) {
		h.mux.HandleFunc("/api/v1/admin/knowledge/", h.featureUnavailable("knowledge", "database"))
	}
}

// registerSystemRoutes registers system info, tools, and connections endpoints.
func (h *Handler) registerSystemRoutes() {
	h.mux.HandleFunc("GET /api/v1/admin/system/info", h.getSystemInfo)
	h.mux.HandleFunc("GET /api/v1/admin/tools", h.listTools)
	h.mux.HandleFunc("GET /api/v1/admin/tools/schemas", h.getToolSchemas)
	h.mux.HandleFunc("GET /api/v1/admin/tools/{name}", h.getToolDetail)
	h.mux.HandleFunc("POST /api/v1/admin/tools/call", h.callTool)
	if h.isMutable() {
		h.mux.HandleFunc("PUT /api/v1/admin/tools/{name}/visibility", h.setToolVisibility)
	}
	h.mux.HandleFunc("GET /api/v1/admin/connections", h.listConnections)
	h.mux.HandleFunc("GET /api/v1/admin/embedding/status", h.getEmbeddingStatus)
	h.publicMux.HandleFunc("GET /api/v1/admin/public/branding", h.getPublicBranding)
	h.publicMux.Handle(docsPrefix, httpswagger.Handler(
		httpswagger.URL(docsPrefix+"doc.json"),
	))
}

// registerConfigRoutes registers config read/write endpoints.
func (h *Handler) registerConfigRoutes() {
	if h.deps.Config != nil {
		h.mux.HandleFunc("GET /api/v1/admin/config", h.getConfig)
	}
	h.mux.HandleFunc("GET /api/v1/admin/config/mode", h.configMode)
	h.mux.HandleFunc("GET /api/v1/admin/config/export", h.exportConfig)
	h.mux.HandleFunc("GET /api/v1/admin/config/agent-instructions-baseline", h.getAgentInstructionsBaseline)
	if h.deps.ConfigStore != nil {
		h.mux.HandleFunc("GET /api/v1/admin/config/effective", h.listEffectiveConfig)
		h.mux.HandleFunc("GET /api/v1/admin/config/entries", h.listConfigEntries)
		h.mux.HandleFunc("GET /api/v1/admin/config/entries/{key}", h.getConfigEntry)
	}
	if h.isMutable() {
		h.mux.HandleFunc("PUT /api/v1/admin/config/entries/{key}", h.setConfigEntry)
		h.mux.HandleFunc("DELETE /api/v1/admin/config/entries/{key}", h.deleteConfigEntry)
		h.mux.HandleFunc("GET /api/v1/admin/config/changelog", h.getConfigChangelog)
	}
}

// registerPersonaRoutes registers persona read endpoints and conditional write
// endpoints (only in database config mode).
func (h *Handler) registerPersonaRoutes() {
	if h.deps.PersonaRegistry == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/personas", h.listPersonas)
	h.mux.HandleFunc("GET /api/v1/admin/personas/{name}", h.getPersona)
	h.mux.HandleFunc("POST /api/v1/admin/personas/{name}/test-access", h.testPersonaAccess)
	if h.isMutable() {
		h.mux.HandleFunc("POST /api/v1/admin/personas", h.createPersona)
		h.mux.HandleFunc("PUT /api/v1/admin/personas/{name}", h.updatePersona)
		h.mux.HandleFunc("DELETE /api/v1/admin/personas/{name}", h.deletePersona)
	}
}

// registerAuthKeyRoutes registers auth key read endpoints and conditional write
// endpoints (only in database config mode).
func (h *Handler) registerAuthKeyRoutes() {
	if h.deps.APIKeyManager == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/auth/keys", h.listAuthKeys)
	if h.isMutable() {
		h.mux.HandleFunc("POST /api/v1/admin/auth/keys", h.createAuthKey)
		h.mux.HandleFunc("DELETE /api/v1/admin/auth/keys/{name}", h.deleteAuthKey)
	} else {
		// Register write patterns as read-only so the mux returns 405
		// instead of 404 (POST already gets 405 from the GET pattern,
		// but DELETE /auth/keys/{name} has no matching GET pattern).
		h.mux.HandleFunc("POST /api/v1/admin/auth/keys", h.readOnlyMethod())
		h.mux.HandleFunc("DELETE /api/v1/admin/auth/keys/{name}", h.readOnlyMethod())
	}
}

// configModeFile is the config store mode value for read-only file mode.
const configModeFile = "file"

// isMutable returns true if the config store supports mutations (DB mode).
func (h *Handler) isMutable() bool {
	return h.deps.ConfigStore != nil && h.deps.ConfigStore.Mode() != configModeFile
}

// readOnlyMethod returns a handler that responds with 405 Method Not Allowed
// for write operations that are not available in file config mode.
func (*Handler) readOnlyMethod() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "not available in file config mode")
	}
}

// featureUnavailable returns a handler that responds with 409 Conflict
// when a feature is enabled in config but unavailable in the current
// operating mode (e.g., knowledge enabled but no database configured).
func (*Handler) featureUnavailable(feature, requires string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("%s is enabled but not available without %s configuration", feature, requires))
	}
}

// problemDetail represents an RFC 9457 Problem Details response.
type problemDetail struct {
	Type   string `json:"type" example:"about:blank"`
	Title  string `json:"title" example:"Not Found"`
	Status int    `json:"status" example:"404"`
	Detail string `json:"detail,omitempty" example:"resource not found"`
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response using RFC 9457 Problem Details.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}
