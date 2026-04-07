package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// personaSummary is a lightweight persona representation for list responses.
type personaSummary struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	ToolCount   int      `json:"tool_count"`
	Source      string   `json:"source,omitempty"` // "file", "database", or "both"
}

// personaContextDetail holds context override fields nested under "context" in JSON.
type personaContextDetail struct {
	DescriptionPrefix         string `json:"description_prefix,omitempty"`
	DescriptionOverride       string `json:"description_override,omitempty"`
	AgentInstructionsSuffix   string `json:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string `json:"agent_instructions_override,omitempty"`
}

// personaDetail includes resolved tool lists.
type personaDetail struct {
	Name             string                `json:"name"`
	DisplayName      string                `json:"display_name"`
	Description      string                `json:"description,omitempty"`
	Roles            []string              `json:"roles"`
	Priority         int                   `json:"priority"`
	AllowTools       []string              `json:"allow_tools"`
	DenyTools        []string              `json:"deny_tools"`
	AllowConnections []string              `json:"allow_connections,omitempty"`
	DenyConnections  []string              `json:"deny_connections,omitempty"`
	Tools            []string              `json:"tools"`
	Context          *personaContextDetail `json:"context,omitempty"`
	Source           string                `json:"source,omitempty"` // "file", "database", or "both"
}

// personaCreateRequest is the request body for creating/updating a persona.
type personaCreateRequest struct {
	Name                      string   `json:"name"`
	DisplayName               string   `json:"display_name"`
	Description               string   `json:"description,omitempty"`
	Roles                     []string `json:"roles"`
	AllowTools                []string `json:"allow_tools"`
	DenyTools                 []string `json:"deny_tools,omitempty"`
	AllowConnections          []string `json:"allow_connections,omitempty"`
	DenyConnections           []string `json:"deny_connections,omitempty"`
	Priority                  int      `json:"priority,omitempty"`
	DescriptionPrefix         string   `json:"description_prefix,omitempty"`
	DescriptionOverride       string   `json:"description_override,omitempty"`
	AgentInstructionsSuffix   string   `json:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string   `json:"agent_instructions_override,omitempty"`
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
			Source:      p.Source,
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

	writeJSON(w, http.StatusOK, toPersonaDetail(p, tools))
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
	p.Source = platform.SourceDatabase

	// Persist to database FIRST — if it fails, don't register in-memory.
	if h.deps.PersonaStore != nil {
		author := extractAuthor(r)
		def := platform.PersonaDefinitionFromPersona(p, author)
		if err := h.deps.PersonaStore.Set(r.Context(), def); err != nil {
			slog.Warn("failed to persist persona", logKeyName, p.Name, logKeyError, err)
			writeError(w, http.StatusInternalServerError, "failed to persist persona")
			return
		}
	}

	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register persona")
		return
	}

	writeJSON(w, http.StatusCreated, toPersonaDetail(p, []string{}))
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
	if h.deps.FilePersonaNames[name] {
		p.Source = platform.SourceBoth
	} else {
		p.Source = platform.SourceDatabase
	}

	// Persist to database FIRST — if it fails, don't update in-memory.
	if h.deps.PersonaStore != nil {
		author := extractAuthor(r)
		def := platform.PersonaDefinitionFromPersona(p, author)
		if err := h.deps.PersonaStore.Set(r.Context(), def); err != nil {
			slog.Warn("failed to persist persona update", logKeyName, p.Name, logKeyError, err)
			writeError(w, http.StatusInternalServerError, "failed to persist persona")
			return
		}
	}

	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update persona")
		return
	}

	writeJSON(w, http.StatusOK, toPersonaDetail(p, []string{}))
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

	// Block deletion of the admin persona.
	if h.deps.Config != nil && name == h.deps.Config.Admin.Persona {
		writeError(w, http.StatusConflict, "cannot delete the admin persona")
		return
	}

	// Block deletion of file-only personas — they would reappear on restart.
	if h.deps.FilePersonaNames[name] {
		existing, _ := h.deps.PersonaRegistry.Get(name)
		if existing != nil && existing.Source == platform.SourceFile {
			writeError(w, http.StatusConflict,
				"this persona is defined in the config file and cannot be deleted via the admin API")
			return
		}
	}

	// Delete from database FIRST — if it fails, don't remove from in-memory registry.
	if h.deps.PersonaStore != nil {
		if err := h.deps.PersonaStore.Delete(r.Context(), name); err != nil {
			slog.Warn("failed to delete persona from database", logKeyName, sanitizeLogValue(name), logKeyError, err) // #nosec G706 -- name is sanitized
			writeError(w, http.StatusInternalServerError, "failed to delete persona from database")
			return
		}
	}

	if err := h.deps.PersonaRegistry.Unregister(name); err != nil {
		writeError(w, http.StatusNotFound, "persona not found")
		return
	}

	// If the persona has a file fallback, re-register the file version so
	// it reverts immediately rather than disappearing until restart.
	if h.deps.FilePersonaNames[name] {
		h.revertToFilePersona(name)
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}

// revertToFilePersona re-registers the file-based version of a persona after
// its database override has been deleted.
func (h *Handler) revertToFilePersona(name string) {
	if h.deps.Config == nil {
		return
	}
	def, ok := h.deps.Config.Personas.Definitions[name]
	if !ok {
		return
	}
	p := &persona.Persona{
		Name:        name,
		DisplayName: def.DisplayName,
		Description: def.Description,
		Roles:       def.Roles,
		Tools: persona.ToolRules{
			Allow: def.Tools.Allow,
			Deny:  def.Tools.Deny,
		},
		Connections: persona.ConnectionRules{
			Allow: def.Connections.Allow,
			Deny:  def.Connections.Deny,
		},
		Context: persona.ContextOverrides{
			DescriptionPrefix:         def.Context.DescriptionPrefix,
			DescriptionOverride:       def.Context.DescriptionOverride,
			AgentInstructionsSuffix:   def.Context.AgentInstructionsSuffix,
			AgentInstructionsOverride: def.Context.AgentInstructionsOverride,
		},
		Priority: def.Priority,
		Source:   platform.SourceFile,
	}
	if err := h.deps.PersonaRegistry.Register(p); err != nil {
		slog.Warn("failed to revert persona to file version", logKeyName, sanitizeLogValue(name), logKeyError, err) // #nosec G706 -- name is sanitized
	}
}

// toPersonaDetail builds a personaDetail response from a persona and its resolved tool list.
func toPersonaDetail(p *persona.Persona, tools []string) personaDetail {
	ctx := &personaContextDetail{
		DescriptionPrefix:         p.Context.DescriptionPrefix,
		DescriptionOverride:       p.Context.DescriptionOverride,
		AgentInstructionsSuffix:   p.Context.AgentInstructionsSuffix,
		AgentInstructionsOverride: p.Context.AgentInstructionsOverride,
	}
	// Omit empty context object from JSON.
	if *ctx == (personaContextDetail{}) {
		ctx = nil
	}
	return personaDetail{
		Name:             p.Name,
		DisplayName:      p.DisplayName,
		Description:      p.Description,
		Roles:            p.Roles,
		Priority:         p.Priority,
		AllowTools:       p.Tools.Allow,
		DenyTools:        p.Tools.Deny,
		AllowConnections: p.Connections.Allow,
		DenyConnections:  p.Connections.Deny,
		Tools:            tools,
		Context:          ctx,
		Source:           p.Source,
	}
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

	allowConn := req.AllowConnections
	if allowConn == nil {
		allowConn = []string{}
	}
	denyConn := req.DenyConnections
	if denyConn == nil {
		denyConn = []string{}
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
		Connections: persona.ConnectionRules{
			Allow: allowConn,
			Deny:  denyConn,
		},
		Context: persona.ContextOverrides{
			DescriptionPrefix:         req.DescriptionPrefix,
			DescriptionOverride:       req.DescriptionOverride,
			AgentInstructionsSuffix:   req.AgentInstructionsSuffix,
			AgentInstructionsOverride: req.AgentInstructionsOverride,
		},
		Priority: req.Priority,
	}
}

// extractAuthor returns the author identifier from the request context.
// Returns "unknown" and logs a warning if no user is present.
func extractAuthor(r *http.Request) string {
	if user := GetUser(r.Context()); user != nil {
		if user.Email != "" {
			return user.Email
		}
		return user.UserID
	}
	slog.Warn("no user in request context for author extraction")
	return "unknown"
}
