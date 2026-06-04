package admin

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// systemInfoResponse is returned by GET /system/info.
type systemInfoResponse struct {
	Name            string         `json:"name" example:"acme-data-platform"`
	Version         string         `json:"version" example:"1.55.11"`
	Commit          string         `json:"commit" example:"b5d2a78"`
	BuildDate       string         `json:"build_date" example:"2026-04-15T00:00:00Z"`
	Description     string         `json:"description" example:"Semantic data platform"`
	Transport       string         `json:"transport" example:"http"`
	ConfigMode      string         `json:"config_mode" example:"database"`
	PortalTitle     string         `json:"portal_title" example:"ACME Data Platform"`
	PortalLogo      string         `json:"portal_logo" example:"https://example.com/logo.svg"`
	PortalLogoLight string         `json:"portal_logo_light" example:"https://example.com/logo-light.svg"`
	PortalLogoDark  string         `json:"portal_logo_dark" example:"https://example.com/logo-dark.svg"`
	Features        systemFeatures `json:"features"`
	ToolkitCount    int            `json:"toolkit_count" example:"5"`
	PersonaCount    int            `json:"persona_count" example:"3"`
}

// systemFeatures lists platform features based on runtime availability.
type systemFeatures struct {
	Audit     bool `json:"audit" example:"true"`
	OAuth     bool `json:"oauth" example:"false"`
	Knowledge bool `json:"knowledge" example:"true"`
	Admin     bool `json:"admin" example:"true"`
	Database  bool `json:"database" example:"true"`
}

// getSystemInfo handles GET /api/v1/admin/system/info.
//
// @Summary      Get system info
// @Description  Returns platform identity, version, runtime feature availability, and config mode.
// @Tags         System
// @Produce      json
// @Success      200  {object}  systemInfoResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/system/info [get]
func (h *Handler) getSystemInfo(w http.ResponseWriter, _ *http.Request) {
	cfg := h.deps.Config
	resp := systemInfoResponse{
		Version:    mcpserver.Version,
		Commit:     mcpserver.Commit,
		BuildDate:  mcpserver.Date,
		ConfigMode: configModeFile,
	}
	if cfg != nil {
		resp.Name = cfg.Server.Name
		resp.Description = cfg.Server.Description
		resp.Transport = cfg.Server.Transport
		resp.PortalTitle = cfg.Portal.Title
		resp.PortalLogo = cfg.Portal.Logo
		resp.PortalLogoLight = cfg.Portal.LogoLight
		resp.PortalLogoDark = cfg.Portal.LogoDark
		resp.Features = systemFeatures{
			Audit:     h.deps.AuditQuerier != nil,
			OAuth:     cfg.OAuth.Enabled,
			Knowledge: h.deps.Knowledge != nil,
			Admin:     cfg.Admin.Enabled,
			Database:  h.deps.DatabaseAvailable,
		}
	}
	if h.deps.ConfigStore != nil {
		resp.ConfigMode = h.deps.ConfigStore.Mode()
	}
	if h.deps.ToolkitRegistry != nil {
		resp.ToolkitCount = len(h.deps.ToolkitRegistry.All())
	}
	if h.deps.PersonaRegistry != nil {
		resp.PersonaCount = len(h.deps.PersonaRegistry.All())
	}
	writeJSON(w, http.StatusOK, resp)
}

// publicBrandingResponse is returned by the unauthenticated branding endpoint.
type publicBrandingResponse struct {
	Name            string `json:"name" example:"acme-data-platform"`
	Version         string `json:"version" example:"1.55.11"`
	PortalTitle     string `json:"portal_title" example:"ACME Data Platform"`
	PortalTagline   string `json:"portal_tagline" example:"Sign in to access your data."`
	PortalLogo      string `json:"portal_logo" example:"https://example.com/logo.svg"`
	PortalLogoLight string `json:"portal_logo_light" example:"https://example.com/logo-light.svg"`
	PortalLogoDark  string `json:"portal_logo_dark" example:"https://example.com/logo-dark.svg"`
	OIDCEnabled     bool   `json:"oidc_enabled" example:"false"`
}

// getPublicBranding handles GET /api/v1/admin/public/branding.
// This endpoint is unauthenticated and returns only non-sensitive display info.
func (h *Handler) getPublicBranding(w http.ResponseWriter, _ *http.Request) {
	resp := publicBrandingResponse{
		Version: mcpserver.Version,
	}
	if h.deps.Config != nil {
		resp.Name = h.deps.Config.Server.Name
		resp.PortalTitle = h.deps.Config.Portal.Title
		resp.PortalTagline = h.deps.Config.Portal.Tagline
		resp.PortalLogo = h.deps.Config.Portal.Logo
		resp.PortalLogoLight = h.deps.Config.Portal.LogoLight
		resp.PortalLogoDark = h.deps.Config.Portal.LogoDark
		resp.OIDCEnabled = h.deps.Config.Auth.BrowserSession.Enabled && h.deps.Config.Auth.OIDC.Enabled
	}
	writeJSON(w, http.StatusOK, resp)
}

// toolInfo describes a single tool and its owning toolkit.
type toolInfo struct {
	Name       string `json:"name" example:"trino_query"`
	Title      string `json:"title,omitempty" example:"Trino Query"`
	Toolkit    string `json:"toolkit" example:"acme-warehouse"`
	Kind       string `json:"kind" example:"trino"`
	Connection string `json:"connection" example:"acme-warehouse"`
	// Hidden is true when the tool is excluded from tools/list responses by
	// the platform-wide tools.allow / tools.deny visibility filter. Persona
	// authorization is independent and not reflected here.
	Hidden bool `json:"hidden" example:"false"`
}

// toolListResponse wraps a list of tools.
type toolListResponse struct {
	Tools []toolInfo `json:"tools"`
	Total int        `json:"total" example:"12"`
}

// listTools handles GET /api/v1/admin/tools.
//
// @Summary      List tools
// @Description  Returns all registered tools across all toolkits and platform-level tools.
// @Tags         System
// @Produce      json
// @Success      200  {object}  toolListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/tools [get]
func (h *Handler) listTools(w http.ResponseWriter, r *http.Request) {
	titleMap := h.buildToolTitleMap(r)

	var allow, deny []string
	if h.deps.Config != nil {
		// Snapshot via accessors — tools.deny is mutated at runtime by
		// the visibility endpoint and would otherwise race the iteration
		// inside IsToolVisible.
		allow = h.deps.Config.ToolsAllowSnapshot()
		deny = h.deps.Config.ToolsDenySnapshot()
	}

	tools := h.collectToolkitTools(titleMap, allow, deny)
	for _, pt := range h.deps.PlatformTools {
		tools = append(tools, toolInfo{
			Name:   pt.Name,
			Title:  titleMap[pt.Name],
			Kind:   pt.Kind,
			Hidden: !middleware.IsToolVisible(pt.Name, allow, deny),
		})
	}
	if tools == nil {
		tools = []toolInfo{}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	writeJSON(w, http.StatusOK, toolListResponse{Tools: tools, Total: len(tools)})
}

// collectToolkitTools enumerates every tool registered by every toolkit
// in the registry and resolves its display connection. Tools from
// fan-out toolkits (the gateway) are namespaced per upstream and must
// be attributed to the upstream that owns them — falling back to
// tk.Connection() (the toolkit's instance-level default) would lump
// every gateway tool under one bucket regardless of which upstream
// owns it, making the admin Tools page group all of them under
// "platform" / the toolkit's default name. resolveToolConnection
// implements the per-tool override.
func (h *Handler) collectToolkitTools(titleMap map[string]string, allow, deny []string) []toolInfo {
	if h.deps.ToolkitRegistry == nil {
		return nil
	}
	var tools []toolInfo
	for _, tk := range h.deps.ToolkitRegistry.All() {
		resolver, _ := tk.(registry.ConnectionResolver)
		defaultConn := tk.Connection()
		for _, name := range tk.Tools() {
			tools = append(tools, toolInfo{
				Name:       name,
				Title:      titleMap[name],
				Toolkit:    tk.Name(),
				Kind:       tk.Kind(),
				Connection: resolveToolConnection(resolver, name, defaultConn),
				Hidden:     !middleware.IsToolVisible(name, allow, deny),
			})
		}
	}
	return tools
}

func resolveToolConnection(resolver registry.ConnectionResolver, name, defaultConn string) string {
	if resolver == nil {
		return defaultConn
	}
	if perTool := resolver.ConnectionForTool(name); perTool != "" {
		return perTool
	}
	return defaultConn
}

// buildToolTitleMap returns a map of tool name → title from the MCP server.
// Returns an empty map if the MCP server is unavailable.
func (h *Handler) buildToolTitleMap(r *http.Request) map[string]string {
	titles := make(map[string]string)
	if h.deps.MCPServer == nil {
		return titles
	}

	session, cleanup, err := h.connectInternalSession(r)
	if err != nil {
		return titles
	}
	defer cleanup()

	listResult, err := session.ListTools(r.Context(), &mcp.ListToolsParams{})
	if err != nil {
		return titles
	}

	for _, tool := range listResult.Tools {
		if tool.Title != "" {
			titles[tool.Name] = tool.Title
		}
	}
	return titles
}

// connectionInfo describes a toolkit connection.
//
// Tools and HiddenTools are declared as plain []string but ship as JSON
// arrays, NEVER null. MarshalJSON enforces the non-nil invariant at
// serialization time because the persona editor UI dereferences
// .length / .map on the wire value, and several toolkits (notably
// gateway with no live upstream) return nil from Tools().
type connectionInfo struct {
	Kind        string   `json:"kind" example:"trino"`
	Name        string   `json:"name" example:"acme-warehouse"`
	Connection  string   `json:"connection" example:"acme-warehouse"`
	Tools       []string `json:"tools" example:"trino_query,trino_describe_table,trino_browse"`
	HiddenTools []string `json:"hidden_tools"`
}

// MarshalJSON enforces the non-nil wire invariant for Tools and
// HiddenTools no matter how the struct was constructed. This closes
// the loophole where a future handler builds a connectionInfo by
// struct literal and forgets to normalize.
func (c connectionInfo) MarshalJSON() ([]byte, error) {
	type alias connectionInfo
	v := alias(c)
	if v.Tools == nil {
		v.Tools = []string{}
	}
	if v.HiddenTools == nil {
		v.HiddenTools = []string{}
	}
	return json.Marshal(v) //nolint:wrapcheck // value struct of basic types cannot fail to marshal
}

// connectionListResponse wraps a list of connections.
type connectionListResponse struct {
	Connections []connectionInfo `json:"connections"`
	Total       int              `json:"total" example:"5"`
}

// listConnections handles GET /api/v1/admin/connections.
//
// @Summary      List connections
// @Description  Returns all toolkit connections with their tools.
// @Tags         System
// @Produce      json
// @Success      200  {object}  connectionListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/connections [get]
func (h *Handler) listConnections(w http.ResponseWriter, _ *http.Request) {
	var allow, deny []string
	if h.deps.Config != nil {
		allow = h.deps.Config.ToolsAllowSnapshot()
		deny = h.deps.Config.ToolsDenySnapshot()
	}

	var conns []connectionInfo
	if h.deps.ToolkitRegistry != nil {
		for _, tk := range h.deps.ToolkitRegistry.All() {
			tools := tk.Tools()
			hidden := hiddenTools(tools, allow, deny)
			// Multi-connection toolkits (apigateway, trino with multiple
			// catalogs, etc.) must expand to one entry per real connection
			// because that is what the persona filter authorizes against.
			if lister, ok := tk.(toolkit.ConnectionLister); ok {
				for _, conn := range lister.ListConnections() {
					conns = append(conns, connectionInfo{
						Kind:        tk.Kind(),
						Name:        conn.Name,
						Connection:  conn.Name,
						Tools:       tools,
						HiddenTools: hidden,
					})
				}
				continue
			}
			// Built-in single-instance toolkits (knowledge, memory, portal,
			// platform) report an empty Connection(), which the persona filter
			// always allows as platform-level (IsConnectionAllowed): not a
			// gateable connection, so it does not belong in this list.
			if tk.Connection() == "" {
				continue
			}
			conns = append(conns, connectionInfo{
				Kind:        tk.Kind(),
				Name:        tk.Name(),
				Connection:  tk.Connection(),
				Tools:       tools,
				HiddenTools: hidden,
			})
		}
	}
	if conns == nil {
		conns = []connectionInfo{}
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].Name < conns[j].Name })
	writeJSON(w, http.StatusOK, connectionListResponse{Connections: conns, Total: len(conns)})
}

// hiddenTools returns the subset of tools that are hidden by the global
// visibility filter (tools.allow / tools.deny config).
func hiddenTools(tools, allow, deny []string) []string {
	var hidden []string
	for _, name := range tools {
		if !middleware.IsToolVisible(name, allow, deny) {
			hidden = append(hidden, name)
		}
	}
	if hidden == nil {
		hidden = []string{}
	}
	return hidden
}
