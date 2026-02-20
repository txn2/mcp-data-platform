package admin

import (
	"net/http"
	"sort"

	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// systemInfoResponse is returned by GET /system/info.
type systemInfoResponse struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Commit          string         `json:"commit"`
	BuildDate       string         `json:"build_date"`
	Description     string         `json:"description"`
	Transport       string         `json:"transport"`
	ConfigMode      string         `json:"config_mode"`
	PortalTitle     string         `json:"portal_title"`
	PortalLogo      string         `json:"portal_logo"`
	PortalLogoLight string         `json:"portal_logo_light"`
	PortalLogoDark  string         `json:"portal_logo_dark"`
	Features        systemFeatures `json:"features"`
	ToolkitCount    int            `json:"toolkit_count"`
	PersonaCount    int            `json:"persona_count"`
}

// systemFeatures lists platform features based on runtime availability.
type systemFeatures struct {
	Audit     bool `json:"audit"`
	OAuth     bool `json:"oauth"`
	Knowledge bool `json:"knowledge"`
	Admin     bool `json:"admin"`
	Database  bool `json:"database"`
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
// @Router       /system/info [get]
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
		resp.PortalTitle = cfg.Admin.PortalTitle
		resp.PortalLogo = cfg.Admin.PortalLogo
		resp.PortalLogoLight = cfg.Admin.PortalLogoLight
		resp.PortalLogoDark = cfg.Admin.PortalLogoDark
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
	Name            string `json:"name"`
	PortalTitle     string `json:"portal_title"`
	PortalLogo      string `json:"portal_logo"`
	PortalLogoLight string `json:"portal_logo_light"`
	PortalLogoDark  string `json:"portal_logo_dark"`
}

// getPublicBranding handles GET /api/v1/admin/public/branding.
// This endpoint is unauthenticated and returns only non-sensitive display info.
func (h *Handler) getPublicBranding(w http.ResponseWriter, _ *http.Request) {
	resp := publicBrandingResponse{}
	if h.deps.Config != nil {
		resp.Name = h.deps.Config.Server.Name
		resp.PortalTitle = h.deps.Config.Admin.PortalTitle
		resp.PortalLogo = h.deps.Config.Admin.PortalLogo
		resp.PortalLogoLight = h.deps.Config.Admin.PortalLogoLight
		resp.PortalLogoDark = h.deps.Config.Admin.PortalLogoDark
	}
	writeJSON(w, http.StatusOK, resp)
}

// toolInfo describes a single tool and its owning toolkit.
type toolInfo struct {
	Name       string `json:"name"`
	Toolkit    string `json:"toolkit"`
	Kind       string `json:"kind"`
	Connection string `json:"connection"`
}

// toolListResponse wraps a list of tools.
type toolListResponse struct {
	Tools []toolInfo `json:"tools"`
	Total int        `json:"total"`
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
// @Router       /tools [get]
func (h *Handler) listTools(w http.ResponseWriter, _ *http.Request) {
	var tools []toolInfo
	if h.deps.ToolkitRegistry != nil {
		for _, tk := range h.deps.ToolkitRegistry.All() {
			for _, name := range tk.Tools() {
				tools = append(tools, toolInfo{
					Name:       name,
					Toolkit:    tk.Name(),
					Kind:       tk.Kind(),
					Connection: tk.Connection(),
				})
			}
		}
	}
	for _, pt := range h.deps.PlatformTools {
		tools = append(tools, toolInfo{
			Name: pt.Name,
			Kind: pt.Kind,
		})
	}
	if tools == nil {
		tools = []toolInfo{}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	writeJSON(w, http.StatusOK, toolListResponse{Tools: tools, Total: len(tools)})
}

// connectionInfo describes a toolkit connection.
type connectionInfo struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Connection  string   `json:"connection"`
	Tools       []string `json:"tools"`
	HiddenTools []string `json:"hidden_tools"`
}

// connectionListResponse wraps a list of connections.
type connectionListResponse struct {
	Connections []connectionInfo `json:"connections"`
	Total       int              `json:"total"`
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
// @Router       /connections [get]
func (h *Handler) listConnections(w http.ResponseWriter, _ *http.Request) {
	var allow, deny []string
	if h.deps.Config != nil {
		allow = h.deps.Config.Tools.Allow
		deny = h.deps.Config.Tools.Deny
	}

	var conns []connectionInfo
	if h.deps.ToolkitRegistry != nil {
		for _, tk := range h.deps.ToolkitRegistry.All() {
			tools := tk.Tools()
			hidden := hiddenTools(tools, allow, deny)
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
