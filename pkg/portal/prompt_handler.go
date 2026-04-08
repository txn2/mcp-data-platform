package portal

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// PromptStore provides prompt persistence for the portal.
type PromptStore interface {
	Create(ctx context.Context, p *prompt.Prompt) error
	Get(ctx context.Context, name string) (*prompt.Prompt, error)
	GetByID(ctx context.Context, id string) (*prompt.Prompt, error)
	Update(ctx context.Context, p *prompt.Prompt) error
	Delete(ctx context.Context, name string) error
	DeleteByID(ctx context.Context, id string) error
	List(ctx context.Context, filter prompt.ListFilter) ([]prompt.Prompt, error)
	Count(ctx context.Context, filter prompt.ListFilter) (int, error)
}

// PromptInfoProvider returns metadata about system-registered prompts.
type PromptInfoProvider interface {
	AllPromptInfos() []registry.PromptInfo
}

// PromptRegistrar registers/unregisters prompts with the live MCP server.
type PromptRegistrar interface {
	RegisterRuntimePrompt(p *prompt.Prompt)
	UnregisterRuntimePrompt(name string)
}

// registerPromptRoutes registers user-facing prompt routes if the store is available.
func (h *Handler) registerPromptRoutes() {
	if h.deps.PromptStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/portal/prompts", h.listMyPrompts)
	h.mux.HandleFunc("POST /api/v1/portal/prompts", h.createMyPrompt)
	h.mux.HandleFunc("PUT /api/v1/portal/prompts/{id}", h.updateMyPrompt)
	h.mux.HandleFunc("DELETE /api/v1/portal/prompts/{id}", h.deleteMyPrompt)
}

// portalPromptListResponse is the response for user prompt listing.
type portalPromptListResponse struct {
	Personal  []prompt.Prompt `json:"personal"`
	Available []prompt.Prompt `json:"available"`
}

// portalPromptCreateRequest is the request body for creating a personal prompt.
type portalPromptCreateRequest struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Content     string            `json:"content"`
	Arguments   []prompt.Argument `json:"arguments"`
	Category    string            `json:"category"`
}

// listMyPrompts returns the user's personal prompts plus available global/persona prompts.
func (h *Handler) listMyPrompts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	enabled := true

	// Get personal prompts
	personal, err := h.deps.PromptStore.List(r.Context(), prompt.ListFilter{
		Scope:      prompt.ScopePersonal,
		OwnerEmail: user.Email,
		Enabled:    &enabled,
	})
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to list prompts")
		return
	}
	if personal == nil {
		personal = []prompt.Prompt{}
	}

	// Get global prompts
	available, err := h.deps.PromptStore.List(r.Context(), prompt.ListFilter{
		Scope:   prompt.ScopeGlobal,
		Enabled: &enabled,
	})
	if err != nil {
		available = []prompt.Prompt{}
	}

	// Get persona-scoped prompts matching user's resolved persona
	if personaInfo := h.resolveUserPersona(user); personaInfo != nil {
		personaPrompts, err := h.deps.PromptStore.List(r.Context(), prompt.ListFilter{
			Scope:    prompt.ScopePersona,
			Personas: []string{personaInfo.Name},
			Enabled:  &enabled,
		})
		if err == nil {
			available = append(available, personaPrompts...)
		}
	}

	// Include system prompts (registered on MCP server, not in DB)
	available = append(available, h.systemPrompts(available)...)

	writePortalJSON(w, http.StatusOK, portalPromptListResponse{
		Personal:  personal,
		Available: available,
	})
}

// createMyPrompt creates a personal prompt for the current user.
func (h *Handler) createMyPrompt(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req portalPromptCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writePortalError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Content == "" {
		writePortalError(w, http.StatusBadRequest, "content is required")
		return
	}

	p := &prompt.Prompt{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Content:     req.Content,
		Arguments:   req.Arguments,
		Category:    req.Category,
		Scope:       prompt.ScopePersonal,
		Personas:    []string{},
		OwnerEmail:  user.Email,
		Source:      prompt.SourceOperator,
		Enabled:     true,
	}
	if p.Arguments == nil {
		p.Arguments = []prompt.Argument{}
	}

	if err := h.deps.PromptStore.Create(r.Context(), p); err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to create prompt")
		return
	}

	if h.deps.PromptRegistrar != nil {
		h.deps.PromptRegistrar.RegisterRuntimePrompt(p)
	}

	writePortalJSON(w, http.StatusCreated, p)
}

// updateMyPrompt updates a personal prompt owned by the current user.
func (h *Handler) updateMyPrompt(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := r.PathValue("id")
	existing, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to get prompt")
		return
	}
	if existing == nil {
		writePortalError(w, http.StatusNotFound, "prompt not found")
		return
	}

	isAdmin := hasAnyRole(user.Roles, h.deps.AdminRoles)
	if !isAdmin && existing.OwnerEmail != user.Email {
		writePortalError(w, http.StatusForbidden, "you can only update your own prompts")
		return
	}

	var req portalPromptCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	oldName := existing.Name
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.DisplayName != "" {
		existing.DisplayName = req.DisplayName
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Content != "" {
		existing.Content = req.Content
	}
	if req.Arguments != nil {
		existing.Arguments = req.Arguments
	}
	if req.Category != "" {
		existing.Category = req.Category
	}

	if err := h.deps.PromptStore.Update(r.Context(), existing); err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to update prompt")
		return
	}

	if h.deps.PromptRegistrar != nil {
		h.deps.PromptRegistrar.UnregisterRuntimePrompt(oldName)
		if existing.Enabled {
			h.deps.PromptRegistrar.RegisterRuntimePrompt(existing)
		}
	}

	writePortalJSON(w, http.StatusOK, existing)
}

// deleteMyPrompt deletes a personal prompt owned by the current user.
func (h *Handler) deleteMyPrompt(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	id := r.PathValue("id")
	existing, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to get prompt")
		return
	}
	if existing == nil {
		writePortalError(w, http.StatusNotFound, "prompt not found")
		return
	}

	isAdmin := hasAnyRole(user.Roles, h.deps.AdminRoles)
	if !isAdmin && existing.OwnerEmail != user.Email {
		writePortalError(w, http.StatusForbidden, "you can only delete your own prompts")
		return
	}

	if err := h.deps.PromptStore.DeleteByID(r.Context(), id); err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to delete prompt")
		return
	}

	if h.deps.PromptRegistrar != nil {
		h.deps.PromptRegistrar.UnregisterRuntimePrompt(existing.Name)
	}

	writePortalJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// systemPrompts returns system-registered prompts as Prompt structs, excluding
// any that already appear in the existing list (by name).
func (h *Handler) systemPrompts(existing []prompt.Prompt) []prompt.Prompt {
	if h.deps.PromptInfoProvider == nil {
		return nil
	}
	names := make(map[string]bool, len(existing))
	for _, p := range existing {
		names[p.Name] = true
	}
	var result []prompt.Prompt
	for _, info := range h.deps.PromptInfoProvider.AllPromptInfos() {
		if names[info.Name] {
			continue
		}
		result = append(result, prompt.Prompt{
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
	return result
}

// resolveUserPersona resolves the user's persona using the configured resolver.
func (h *Handler) resolveUserPersona(user *User) *PersonaInfo {
	if h.deps.PersonaResolver == nil {
		return nil
	}
	return h.deps.PersonaResolver(user.Roles)
}

// writePortalJSON writes a JSON response.
func writePortalJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writePortalError writes a JSON error response.
func writePortalError(w http.ResponseWriter, status int, msg string) {
	writePortalJSON(w, status, map[string]string{"error": msg})
}
