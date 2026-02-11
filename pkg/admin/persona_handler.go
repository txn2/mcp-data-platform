package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// personaSummary is a lightweight persona representation for list responses.
type personaSummary struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	ToolCount   int      `json:"tool_count"`
}

// personaDetail includes resolved tool lists.
type personaDetail struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	Priority    int      `json:"priority"`
	AllowTools  []string `json:"allow_tools"`
	DenyTools   []string `json:"deny_tools"`
	Tools       []string `json:"tools"`
}

// personaCreateRequest is the request body for creating/updating a persona.
type personaCreateRequest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	AllowTools  []string `json:"allow_tools"`
	DenyTools   []string `json:"deny_tools,omitempty"`
	Priority    int      `json:"priority,omitempty"`
}

// personaListResponse wraps a list of personas.
type personaListResponse struct {
	Personas []personaSummary `json:"personas"`
	Total    int              `json:"total"`
}

// listPersonas handles GET /api/v1/admin/personas.
//
// @Summary      List personas
// @Description  Returns all configured personas with tool counts.
// @Tags         Personas
// @Produce      json
// @Success      200  {object}  personaListResponse
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /personas [get]
func (h *Handler) listPersonas(w http.ResponseWriter, _ *http.Request) {
	all := h.deps.PersonaRegistry.All()
	summaries := make([]personaSummary, 0, len(all))

	filter := persona.NewToolFilter(nil)
	for _, p := range all {
		toolCount := 0
		if h.deps.ToolkitRegistry != nil {
			for _, t := range h.deps.ToolkitRegistry.AllTools() {
				if filter.IsAllowed(p, t) {
					toolCount++
				}
			}
		}
		summaries = append(summaries, personaSummary{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Description: p.Description,
			Roles:       p.Roles,
			ToolCount:   toolCount,
		})
	}

	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	writeJSON(w, http.StatusOK, personaListResponse{Personas: summaries, Total: len(summaries)})
}

// getPersona handles GET /api/v1/admin/personas/{name}.
//
// @Summary      Get persona
// @Description  Returns a single persona with resolved tool list.
// @Tags         Personas
// @Produce      json
// @Param        name  path  string  true  "Persona name"
// @Success      200  {object}  personaDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /personas/{name} [get]
func (h *Handler) getPersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := h.deps.PersonaRegistry.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}

	// Resolve allowed tools
	filter := persona.NewToolFilter(nil)
	var tools []string
	if h.deps.ToolkitRegistry != nil {
		for _, t := range h.deps.ToolkitRegistry.AllTools() {
			if filter.IsAllowed(p, t) {
				tools = append(tools, t)
			}
		}
	}
	if tools == nil {
		tools = []string{}
	}
	sort.Strings(tools)

	writeJSON(w, http.StatusOK, personaDetail{
		Name:        p.Name,
		DisplayName: p.DisplayName,
		Description: p.Description,
		Roles:       p.Roles,
		Priority:    p.Priority,
		AllowTools:  p.Tools.Allow,
		DenyTools:   p.Tools.Deny,
		Tools:       tools,
	})
}

// createPersona handles POST /api/v1/admin/personas.
//
// @Summary      Create persona
// @Description  Creates a new persona. Only available in database config mode.
// @Tags         Personas
// @Accept       json
// @Produce      json
// @Param        body  body  personaCreateRequest  true  "Persona definition"
// @Success      201  {object}  personaDetail
// @Failure      400  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /personas [post]
func (h *Handler) createPersona(w http.ResponseWriter, r *http.Request) {
	var req personaCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	// Check for existing persona with same name
	if _, exists := h.deps.PersonaRegistry.Get(req.Name); exists {
		writeError(w, http.StatusConflict, "persona already exists")
		return
	}

	p := buildPersonaFromRequest(req)
	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register persona")
		return
	}

	h.syncConfig(r, fmt.Sprintf("create persona %s", p.Name))

	writeJSON(w, http.StatusCreated, personaDetail{
		Name:        p.Name,
		DisplayName: p.DisplayName,
		Description: p.Description,
		Roles:       p.Roles,
		Priority:    p.Priority,
		AllowTools:  p.Tools.Allow,
		DenyTools:   p.Tools.Deny,
		Tools:       []string{},
	})
}

// updatePersona handles PUT /api/v1/admin/personas/{name}.
//
// @Summary      Update persona
// @Description  Updates an existing persona. Only available in database config mode.
// @Tags         Personas
// @Accept       json
// @Produce      json
// @Param        name  path  string                true  "Persona name"
// @Param        body  body  personaCreateRequest  true  "Persona definition"
// @Success      200  {object}  personaDetail
// @Failure      400  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /personas/{name} [put]
func (h *Handler) updatePersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req personaCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}

	// Override name from path
	req.Name = name
	p := buildPersonaFromRequest(req)
	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update persona")
		return
	}

	h.syncConfig(r, fmt.Sprintf("update persona %s", p.Name))

	writeJSON(w, http.StatusOK, personaDetail{
		Name:        p.Name,
		DisplayName: p.DisplayName,
		Description: p.Description,
		Roles:       p.Roles,
		Priority:    p.Priority,
		AllowTools:  p.Tools.Allow,
		DenyTools:   p.Tools.Deny,
		Tools:       []string{},
	})
}

// deletePersona handles DELETE /api/v1/admin/personas/{name}.
//
// @Summary      Delete persona
// @Description  Deletes a persona. Only available in database config mode. Cannot delete the admin persona.
// @Tags         Personas
// @Produce      json
// @Param        name  path  string  true  "Persona name"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /personas/{name} [delete]
func (h *Handler) deletePersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	// Block deletion of the admin persona
	if h.deps.Config != nil && name == h.deps.Config.Admin.Persona {
		writeError(w, http.StatusConflict, "cannot delete the admin persona")
		return
	}

	if err := h.deps.PersonaRegistry.Unregister(name); err != nil {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}

	h.syncConfig(r, fmt.Sprintf("delete persona %s", name))

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}

// buildPersonaFromRequest converts a create request into a persona.
func buildPersonaFromRequest(req personaCreateRequest) *persona.Persona {
	allow := req.AllowTools
	if allow == nil {
		allow = []string{}
	}
	deny := req.DenyTools
	if deny == nil {
		deny = []string{}
	}

	return &persona.Persona{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Roles:       req.Roles,
		Tools: persona.ToolRules{
			Allow: allow,
			Deny:  deny,
		},
		Priority: req.Priority,
	}
}
