package admin

import (
	"net/http"
	"sort"

	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
)

// systemInfoResponse is returned by GET /system/info.
type systemInfoResponse struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Description  string         `json:"description"`
	Transport    string         `json:"transport"`
	ConfigMode   string         `json:"config_mode"`
	PortalTitle  string         `json:"portal_title"`
	Features     systemFeatures `json:"features"`
	ToolkitCount int            `json:"toolkit_count"`
	PersonaCount int            `json:"persona_count"`
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
		ConfigMode: configModeFile,
	}
	if cfg != nil {
		resp.Name = cfg.Server.Name
		resp.Description = cfg.Server.Description
		resp.Transport = cfg.Server.Transport
		resp.PortalTitle = cfg.Admin.PortalTitle
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
// @Description  Returns all registered tools across all toolkits.
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
	if tools == nil {
		tools = []toolInfo{}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	writeJSON(w, http.StatusOK, toolListResponse{Tools: tools, Total: len(tools)})
}

// connectionInfo describes a toolkit connection.
type connectionInfo struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Connection string   `json:"connection"`
	Tools      []string `json:"tools"`
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
	var conns []connectionInfo
	if h.deps.ToolkitRegistry != nil {
		for _, tk := range h.deps.ToolkitRegistry.All() {
			conns = append(conns, connectionInfo{
				Kind:       tk.Kind(),
				Name:       tk.Name(),
				Connection: tk.Connection(),
				Tools:      tk.Tools(),
			})
		}
	}
	if conns == nil {
		conns = []connectionInfo{}
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].Name < conns[j].Name })
	writeJSON(w, http.StatusOK, connectionListResponse{Connections: conns, Total: len(conns)})
}
