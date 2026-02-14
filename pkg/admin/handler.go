// Package admin provides REST API endpoints for administrative operations.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	httpswagger "github.com/swaggo/http-swagger/v2"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// AuditQuerier queries audit events.
type AuditQuerier interface {
	Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error)
	Count(ctx context.Context, filter audit.QueryFilter) (int, error)
	Distinct(ctx context.Context, column string, startTime, endTime *time.Time) ([]string, error)
	DistinctPairs(ctx context.Context, col1, col2 string, startTime, endTime *time.Time) (map[string]string, error)
}

// AuditMetricsQuerier provides aggregate audit metrics.
type AuditMetricsQuerier interface {
	Timeseries(ctx context.Context, filter audit.TimeseriesFilter) ([]audit.TimeseriesBucket, error)
	Breakdown(ctx context.Context, filter audit.BreakdownFilter) ([]audit.BreakdownEntry, error)
	Overview(ctx context.Context, startTime, endTime *time.Time) (*audit.Overview, error)
	Performance(ctx context.Context, startTime, endTime *time.Time) (*audit.PerformanceStats, error)
}

// PersonaRegistry abstracts persona.Registry for testability.
type PersonaRegistry interface {
	All() []*persona.Persona
	Get(name string) (*persona.Persona, bool)
	Register(p *persona.Persona) error
	Unregister(name string) error
	DefaultName() string
}

// APIKeyManager manages API keys at runtime.
type APIKeyManager interface {
	ListKeys() []auth.APIKeySummary
	GenerateKey(name string, roles []string) (string, error)
	RemoveByName(name string) bool
}

// ToolkitRegistry abstracts registry.Registry for testability.
type ToolkitRegistry interface {
	All() []registry.Toolkit
	AllTools() []string
	GetToolkitForTool(toolName string) registry.ToolkitMatch
}

// ConfigStore abstracts configstore.Store for testability.
type ConfigStore interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, data []byte, meta configstore.SaveMeta) error
	History(ctx context.Context, limit int) ([]configstore.Revision, error)
	Mode() string
}

// Deps holds dependencies for the admin handler.
type Deps struct {
	Config              *platform.Config
	ConfigStore         ConfigStore
	PersonaRegistry     PersonaRegistry
	ToolkitRegistry     ToolkitRegistry
	MCPServer           *mcp.Server
	AuditQuerier        AuditQuerier
	AuditMetricsQuerier AuditMetricsQuerier
	Knowledge           *KnowledgeHandler
	APIKeyManager       APIKeyManager
	DatabaseAvailable   bool
	PlatformTools       []platform.ToolInfo
}

// docsPrefix is the path prefix for the public Swagger UI.
const docsPrefix = "/api/v1/admin/docs/"

// publicPrefix is the path prefix for unauthenticated public endpoints.
const publicPrefix = "/api/v1/admin/public/"

// Handler provides admin REST API endpoints.
type Handler struct {
	mux        *http.ServeMux
	publicMux  *http.ServeMux
	deps       Deps
	authMiddle func(http.Handler) http.Handler
}

// statusResponse is a generic status response.
type statusResponse struct {
	Status string `json:"status"`
}

// @title MCP Data Platform Admin API
// @version 1.0
// @description Administrative REST API for managing the MCP Data Platform.
// @description Endpoints cover system info, configuration, personas, auth keys, audit logs, and knowledge management.
//
// @host localhost:8080
// @BasePath /api/v1/admin
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
	if strings.HasPrefix(r.URL.Path, docsPrefix) || strings.HasPrefix(r.URL.Path, publicPrefix) {
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
	h.registerAuditMetricsRoutes()
	h.registerConfigRoutes()
	h.registerPersonaRoutes()
	h.registerAuthKeyRoutes()
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
	} else if h.deps.Config != nil && h.deps.Config.Knowledge.Enabled {
		h.mux.HandleFunc("/api/v1/admin/knowledge/", h.featureUnavailable("knowledge", "database"))
	}
}

// registerSystemRoutes registers system info, tools, and connections endpoints.
func (h *Handler) registerSystemRoutes() {
	h.mux.HandleFunc("GET /api/v1/admin/system/info", h.getSystemInfo)
	h.mux.HandleFunc("GET /api/v1/admin/tools", h.listTools)
	h.mux.HandleFunc("GET /api/v1/admin/tools/schemas", h.getToolSchemas)
	h.mux.HandleFunc("POST /api/v1/admin/tools/call", h.callTool)
	h.mux.HandleFunc("GET /api/v1/admin/connections", h.listConnections)
	h.publicMux.HandleFunc("GET /api/v1/admin/public/branding", h.getPublicBranding)
	h.publicMux.Handle(docsPrefix, httpswagger.Handler(
		httpswagger.URL(docsPrefix+"doc.json"),
	))
}

// registerAuditRoutes registers audit event endpoints or a 409 fallback
// when audit is enabled in config but no database is available.
func (h *Handler) registerAuditRoutes() {
	if h.deps.AuditQuerier != nil {
		h.mux.HandleFunc("GET /api/v1/admin/audit/events/filters", h.listAuditEventFilters)
		h.mux.HandleFunc("GET /api/v1/admin/audit/events", h.listAuditEvents)
		h.mux.HandleFunc("GET /api/v1/admin/audit/events/{id}", h.getAuditEvent)
		h.mux.HandleFunc("GET /api/v1/admin/audit/stats", h.getAuditStats)
	} else if h.deps.Config != nil && h.deps.Config.Audit.Enabled {
		h.mux.HandleFunc("/api/v1/admin/audit/", h.featureUnavailable("audit", "database"))
	}
}

// registerConfigRoutes registers config read/write endpoints.
func (h *Handler) registerConfigRoutes() {
	if h.deps.Config != nil {
		h.mux.HandleFunc("GET /api/v1/admin/config", h.getConfig)
	}
	h.mux.HandleFunc("GET /api/v1/admin/config/mode", h.configMode)
	h.mux.HandleFunc("GET /api/v1/admin/config/export", h.exportConfig)
	if h.deps.ConfigStore != nil {
		h.mux.HandleFunc("POST /api/v1/admin/config/import", h.importConfig)
		h.mux.HandleFunc("GET /api/v1/admin/config/history", h.configHistory)
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
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
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
