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

// errMsgAuthRequired is the standard error message for unauthenticated portal requests.
const errMsgAuthRequired = "authentication required"

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
	Name        string            `json:"name" example:"my-analysis-prompt"`
	DisplayName string            `json:"display_name" example:"My Analysis Prompt"`
	Description string            `json:"description" example:"A prompt for data analysis workflows"`
	Content     string            `json:"content" example:"Analyze the following data: {{data}}"`
	Arguments   []prompt.Argument `json:"arguments"`
	Category    string            `json:"category" example:"analysis"`
}

// listMyPrompts handles GET /api/v1/portal/prompts.
//
// @Summary      List my prompts
// @Description  Returns the user's personal prompts plus available global, persona, and system prompts.
// @Tags         Prompts
// @Produce      json
// @Success      200  {object}  portalPromptListResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts [get]
func (h *Handler) listMyPrompts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
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

// createMyPrompt handles POST /api/v1/portal/prompts.
//
// @Summary      Create personal prompt
// @Description  Creates a new personal prompt for the current user.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        body  body  portalPromptCreateRequest  true  "Prompt details"
// @Success      201  {object}  prompt.Prompt
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts [post]
func (h *Handler) createMyPrompt(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
		return
	}

	var req portalPromptCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := prompt.ValidateName(req.Name); err != nil {
		writePortalError(w, http.StatusBadRequest, err.Error())
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

// updateMyPrompt handles PUT /api/v1/portal/prompts/{id}.
//
// @Summary      Update personal prompt
// @Description  Updates a personal prompt owned by the current user. Admins can update any prompt.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id    path  string                     true  "Prompt ID"
// @Param        body  body  portalPromptCreateRequest  true  "Updated prompt fields"
// @Success      200  {object}  prompt.Prompt
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id} [put]
func (h *Handler) updateMyPrompt(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
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

	if errMsg := checkPortalUpdatePermission(user, existing, h.deps.AdminRoles); errMsg != "" {
		writePortalError(w, http.StatusForbidden, errMsg)
		return
	}

	var req portalPromptCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	oldName := existing.Name
	status, msg := h.applyAndValidatePortalUpdate(r.Context(), existing, req, oldName)
	if status != 0 {
		writePortalError(w, status, msg)
		return
	}

	if err := h.deps.PromptStore.Update(r.Context(), existing); err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to update prompt")
		return
	}

	if refreshed, rErr := h.deps.PromptStore.GetByID(r.Context(), existing.ID); rErr == nil && refreshed != nil {
		existing = refreshed
	}

	reregisterPrompt(h.deps.PromptRegistrar, oldName, existing)
	writePortalJSON(w, http.StatusOK, existing)
}

// reregisterPrompt unregisters the old name and re-registers if the prompt is enabled.
func reregisterPrompt(reg PromptRegistrar, oldName string, p *prompt.Prompt) {
	if reg == nil {
		return
	}
	reg.UnregisterRuntimePrompt(oldName)
	if p.Enabled {
		reg.RegisterRuntimePrompt(p)
	}
}

// checkPortalUpdatePermission checks whether the user may update the given prompt.
// Returns a non-empty error message if denied.
func checkPortalUpdatePermission(user *User, existing *prompt.Prompt, adminRoles []string) string {
	if hasAnyRole(user.Roles, adminRoles) {
		return ""
	}
	if existing.Scope != prompt.ScopePersonal {
		return "non-admins can only manage personal prompts"
	}
	if existing.OwnerEmail != user.Email {
		return "you can only update your own prompts"
	}
	return ""
}

// applyAndValidatePortalUpdate applies field updates and checks for name conflicts.
// oldName is the prompt's name before any mutations were applied.
// Returns (0, "") on success, or (httpStatus, errorMessage) on failure.
func (h *Handler) applyAndValidatePortalUpdate(ctx context.Context, existing *prompt.Prompt, req portalPromptCreateRequest, oldName string) (httpStatus int, errMsg string) {
	if errMsg := applyPortalPromptFields(existing, req); errMsg != "" {
		return http.StatusBadRequest, errMsg
	}
	if existing.Name != oldName {
		dup, _ := h.deps.PromptStore.Get(ctx, existing.Name)
		if dup != nil {
			return http.StatusConflict, "prompt name already exists"
		}
	}
	return 0, ""
}

// applyPortalPromptFields applies non-empty fields from the portal update request to the prompt.
// Returns a non-empty error message on validation failure.
func applyPortalPromptFields(existing *prompt.Prompt, req portalPromptCreateRequest) string {
	if req.Name != "" && req.Name != existing.Name {
		if err := prompt.ValidateName(req.Name); err != nil {
			return err.Error()
		}
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
	return ""
}

// deleteMyPrompt handles DELETE /api/v1/portal/prompts/{id}.
//
// @Summary      Delete personal prompt
// @Description  Deletes a personal prompt owned by the current user. Admins can delete any prompt.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id} [delete]
func (h *Handler) deleteMyPrompt(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
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
	if !isAdmin {
		if existing.Scope != prompt.ScopePersonal {
			writePortalError(w, http.StatusForbidden, "non-admins can only manage personal prompts")
			return
		}
		if existing.OwnerEmail != user.Email {
			writePortalError(w, http.StatusForbidden, "you can only delete your own prompts")
			return
		}
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
			Source:      "system",
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
