package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// registerPromptRoutes registers prompt management routes if the store is available.
func (h *Handler) registerPromptRoutes() {
	if h.deps.PromptStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/prompts", h.listPrompts)
	h.mux.HandleFunc("GET /api/v1/admin/prompts/{id}", h.getPrompt)
	h.mux.HandleFunc("POST /api/v1/admin/prompts", h.createPrompt)
	h.mux.HandleFunc("PUT /api/v1/admin/prompts/{id}", h.updatePrompt)
	h.mux.HandleFunc("DELETE /api/v1/admin/prompts/{id}", h.deletePrompt)
}

// adminPromptListResponse is the paginated response for prompt listing.
type adminPromptListResponse struct {
	Data  []prompt.Prompt `json:"data"`
	Total int             `json:"total"`
}

// adminPromptCreateRequest is the request body for creating a prompt.
type adminPromptCreateRequest struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Content     string            `json:"content"`
	Arguments   []prompt.Argument `json:"arguments"`
	Category    string            `json:"category"`
	Scope       string            `json:"scope"`
	Personas    []string          `json:"personas"`
	OwnerEmail  string            `json:"owner_email"`
	Source      string            `json:"source"`
	Enabled     *bool             `json:"enabled"`
}

// adminPromptUpdateRequest is the request body for updating a prompt.
type adminPromptUpdateRequest struct {
	Name        *string           `json:"name"`
	DisplayName *string           `json:"display_name"`
	Description *string           `json:"description"`
	Content     *string           `json:"content"`
	Arguments   []prompt.Argument `json:"arguments"`
	Category    *string           `json:"category"`
	Scope       *string           `json:"scope"`
	Personas    []string          `json:"personas"`
	OwnerEmail  *string           `json:"owner_email"`
	Source      *string           `json:"source"`
	Enabled     *bool             `json:"enabled"`
}

// listPrompts returns all prompts across all scopes, including system prompts.
func (h *Handler) listPrompts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := prompt.ListFilter{
		Scope:  q.Get("scope"),
		Search: q.Get("search"),
	}
	if owner := q.Get("owner_email"); owner != "" {
		filter.OwnerEmail = owner
	}

	prompts, err := h.deps.PromptStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list prompts")
		return
	}
	if prompts == nil {
		prompts = []prompt.Prompt{}
	}

	// Build a set of known names to avoid duplicates when merging system prompts.
	seen := make(map[string]bool, len(prompts))
	for _, p := range prompts {
		seen[p.Name] = true
	}

	// Merge system prompts (registered on the MCP server but not in DB).
	if h.deps.PromptInfoProvider != nil && filter.OwnerEmail == "" {
		search := strings.ToLower(filter.Search)
		for _, info := range h.deps.PromptInfoProvider.AllPromptInfos() {
			if seen[info.Name] {
				continue
			}
			seen[info.Name] = true
			if filter.Scope != "" && filter.Scope != "system" {
				continue
			}
			if search != "" && !matchesSearch(info, search) {
				continue
			}
			prompts = append(prompts, prompt.Prompt{
				ID:          "system:" + info.Name,
				Name:        info.Name,
				DisplayName: info.Name,
				Description: info.Description,
				Content:     info.Content,
				Category:    info.Category,
				Scope:       "system",
				Source:       "system",
				Enabled:     true,
			})
		}
	}

	total, countErr := h.deps.PromptStore.Count(r.Context(), filter)
	if countErr != nil {
		slog.Warn("failed to count prompts", "error", countErr)
	}

	writeJSON(w, http.StatusOK, adminPromptListResponse{
		Data:  prompts,
		Total: total,
	})
}

// matchesSearch checks if a PromptInfo matches a search query.
func matchesSearch(info registry.PromptInfo, query string) bool {
	return strings.Contains(strings.ToLower(info.Name), query) ||
		strings.Contains(strings.ToLower(info.Description), query) ||
		strings.Contains(strings.ToLower(info.Content), query)
}

// getPrompt returns a single prompt by ID.
func (h *Handler) getPrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get prompt")
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// createPrompt creates a new prompt.
func (h *Handler) createPrompt(w http.ResponseWriter, r *http.Request) {
	var req adminPromptCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := prompt.ValidateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	scope := req.Scope
	if scope == "" {
		scope = prompt.ScopePersonal
	}
	if err := prompt.ValidateScope(scope); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	source := req.Source
	if source == "" {
		source = prompt.SourceOperator
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	p := &prompt.Prompt{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Content:     req.Content,
		Arguments:   req.Arguments,
		Category:    req.Category,
		Scope:       scope,
		Personas:    req.Personas,
		OwnerEmail:  req.OwnerEmail,
		Source:       source,
		Enabled:     enabled,
	}
	if p.Personas == nil {
		p.Personas = []string{}
	}
	if p.Arguments == nil {
		p.Arguments = []prompt.Argument{}
	}

	if err := h.deps.PromptStore.Create(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create prompt")
		return
	}

	// Register with live MCP server
	if h.deps.PromptRegistrar != nil && p.Enabled {
		h.deps.PromptRegistrar.RegisterRuntimePrompt(p)
	}

	writeJSON(w, http.StatusCreated, p)
}

// updatePrompt updates an existing prompt.
func (h *Handler) updatePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get prompt")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	var req adminPromptUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	oldName := existing.Name

	if req.Name != nil && *req.Name != oldName {
		if err := prompt.ValidateName(*req.Name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		dup, _ := h.deps.PromptStore.Get(r.Context(), *req.Name)
		if dup != nil {
			writeError(w, http.StatusConflict, "prompt name already exists")
			return
		}
		existing.Name = *req.Name
	}
	if req.DisplayName != nil {
		existing.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Content != nil {
		existing.Content = *req.Content
	}
	if req.Arguments != nil {
		existing.Arguments = req.Arguments
	}
	if req.Category != nil {
		existing.Category = *req.Category
	}
	if req.Scope != nil {
		if err := prompt.ValidateScope(*req.Scope); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.Scope = *req.Scope
	}
	if req.Personas != nil {
		existing.Personas = req.Personas
	}
	if req.OwnerEmail != nil {
		existing.OwnerEmail = *req.OwnerEmail
	}
	if req.Source != nil {
		existing.Source = *req.Source
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := h.deps.PromptStore.Update(r.Context(), existing); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update prompt")
		return
	}

	// Re-read to get DB-refreshed updated_at.
	refreshed, _ := h.deps.PromptStore.GetByID(r.Context(), existing.ID)
	if refreshed != nil {
		existing = refreshed
	}

	// Re-register with live MCP server
	if h.deps.PromptRegistrar != nil {
		h.deps.PromptRegistrar.UnregisterRuntimePrompt(oldName)
		if existing.Enabled {
			h.deps.PromptRegistrar.RegisterRuntimePrompt(existing)
		}
	}

	writeJSON(w, http.StatusOK, existing)
}

// deletePrompt deletes a prompt by ID.
func (h *Handler) deletePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get prompt")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	if err := h.deps.PromptStore.DeleteByID(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete prompt")
		return
	}

	// Unregister from live MCP server
	if h.deps.PromptRegistrar != nil {
		h.deps.PromptRegistrar.UnregisterRuntimePrompt(existing.Name)
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "deleted"})
}
