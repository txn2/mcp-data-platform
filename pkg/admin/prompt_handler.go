package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	Total int             `json:"total" example:"12"`
}

// promptScopeSystem identifies prompts contributed by built-in providers
// rather than stored in the database.
const promptScopeSystem = "system"

// adminPromptCreateRequest is the request body for creating a prompt.
type adminPromptCreateRequest struct {
	Name        string            `json:"name" example:"daily-sales-report"`
	DisplayName string            `json:"display_name" example:"Daily Sales Report"`
	Description string            `json:"description" example:"Generate a daily sales summary by region"`
	Content     string            `json:"content" example:"Analyze sales data for {date} grouped by region."`
	Arguments   []prompt.Argument `json:"arguments"`
	Category    string            `json:"category" example:"analysis"`
	Scope       string            `json:"scope" example:"persona"`
	Personas    []string          `json:"personas" example:"analyst,data-engineer"`
	Tags        []string          `json:"tags" example:"sales,reporting"`
	OwnerEmail  string            `json:"owner_email" example:"admin@example.com"`
	Source      string            `json:"source" example:"database"`
	Enabled     *bool             `json:"enabled" example:"true"`
}

// adminPromptUpdateRequest is the request body for updating a prompt.
type adminPromptUpdateRequest struct {
	Name         *string           `json:"name"`
	DisplayName  *string           `json:"display_name"`
	Description  *string           `json:"description"`
	Content      *string           `json:"content"`
	Arguments    []prompt.Argument `json:"arguments"`
	Category     *string           `json:"category"`
	Scope        *string           `json:"scope"`
	Personas     []string          `json:"personas"`
	Tags         []string          `json:"tags"`
	Status       *string           `json:"status"`
	SupersededBy *string           `json:"superseded_by"`
	OwnerEmail   *string           `json:"owner_email"`
	Source       *string           `json:"source"`
	Enabled      *bool             `json:"enabled"`
}

// listPrompts returns all prompts across all scopes, including system prompts.
//
// @Summary      List prompts
// @Description  Returns all prompts across all scopes, including system-registered prompts. Supports scope, search, and owner_email filters.
// @Tags         Prompts
// @Produce      json
// @Param        scope        query  string  false  "Filter by scope"
// @Param        search       query  string  false  "Search term"
// @Param        owner_email  query  string  false  "Filter by owner email"
// @Success      200  {object}  adminPromptListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts [get]
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

	prompts = h.mergeSystemPrompts(prompts, filter)

	total, countErr := h.deps.PromptStore.Count(r.Context(), filter)
	if countErr != nil {
		slog.Warn("failed to count prompts", "error", countErr)
	}

	writeJSON(w, http.StatusOK, adminPromptListResponse{
		Data:  prompts,
		Total: total,
	})
}

// mergeSystemPrompts appends system-registered prompts (from MCP server, not DB)
// to the provided list, filtering by scope and search as appropriate.
func (h *Handler) mergeSystemPrompts(prompts []prompt.Prompt, filter prompt.ListFilter) []prompt.Prompt {
	if h.deps.PromptInfoProvider == nil || filter.OwnerEmail != "" {
		return prompts
	}

	seen := make(map[string]bool, len(prompts))
	for _, p := range prompts {
		seen[p.Name] = true
	}

	search := strings.ToLower(filter.Search)
	for _, info := range h.deps.PromptInfoProvider.AllPromptInfos() {
		if seen[info.Name] {
			continue
		}
		seen[info.Name] = true
		if filter.Scope != "" && filter.Scope != promptScopeSystem {
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
			Scope:       promptScopeSystem,
			Source:      promptScopeSystem,
			Enabled:     true,
		})
	}
	return prompts
}

// matchesSearch checks if a PromptInfo matches a search query.
func matchesSearch(info registry.PromptInfo, query string) bool {
	return strings.Contains(strings.ToLower(info.Name), query) ||
		strings.Contains(strings.ToLower(info.Description), query) ||
		strings.Contains(strings.ToLower(info.Content), query)
}

// getPrompt returns a single prompt by ID.
//
// @Summary      Get prompt
// @Description  Returns a single prompt by ID.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {object}  prompt.Prompt
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id} [get]
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
//
// @Summary      Create prompt
// @Description  Creates a new prompt and registers it with the live MCP server when enabled.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        body  body  adminPromptCreateRequest  true  "Prompt definition"
// @Success      201  {object}  prompt.Prompt
// @Failure      400  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts [post]
func (h *Handler) createPrompt(w http.ResponseWriter, r *http.Request) {
	var req adminPromptCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	p, errMsg := buildPromptFromCreateRequest(req)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
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

// buildPromptFromCreateRequest validates and builds a Prompt from the create request.
// Returns a non-empty error message on validation failure.
func buildPromptFromCreateRequest(req adminPromptCreateRequest) (result *prompt.Prompt, errMsg string) {
	if err := prompt.ValidateName(req.Name); err != nil {
		return nil, err.Error()
	}
	if req.Content == "" {
		return nil, "content is required"
	}

	scope := req.Scope
	if scope == "" {
		scope = prompt.ScopePersonal
	}
	if err := prompt.ValidateScope(scope); err != nil {
		return nil, err.Error()
	}
	if err := prompt.ValidateTags(req.Tags); err != nil {
		return nil, err.Error()
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
		Tags:        req.Tags,
		OwnerEmail:  req.OwnerEmail,
		Source:      source,
		Enabled:     enabled,
	}
	if p.Personas == nil {
		p.Personas = []string{}
	}
	if p.Arguments == nil {
		p.Arguments = []prompt.Argument{}
	}
	return p, ""
}

// updatePrompt updates an existing prompt.
//
// @Summary      Update prompt
// @Description  Updates an existing prompt by ID and re-registers it with the live MCP server.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id    path  string                    true  "Prompt ID"
// @Param        body  body  adminPromptUpdateRequest  true  "Prompt fields to update"
// @Success      200  {object}  prompt.Prompt
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      409  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id} [put]
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
	oldScope := existing.Scope

	status, msg := h.applyAdminPromptUpdate(r.Context(), existing, req, adminUserEmail(r))
	if status != 0 {
		writeError(w, status, msg)
		return
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

	// Re-register the name-keyed runtime metadata. Personal prompts are not
	// tracked there (their names collide across owners), so unregister the old
	// name only for shared scopes; RegisterRuntimePrompt self-skips personal.
	if h.deps.PromptRegistrar != nil {
		if oldScope != prompt.ScopePersonal {
			h.deps.PromptRegistrar.UnregisterRuntimePrompt(oldName)
		}
		if existing.Enabled {
			h.deps.PromptRegistrar.RegisterRuntimePrompt(existing)
		}
	}

	writeJSON(w, http.StatusOK, existing)
}

// applyAdminPromptUpdate validates name rename and applies field updates.
// Returns (0, "") on success, or (httpStatus, errorMessage) on failure.
func (h *Handler) applyAdminPromptUpdate(ctx context.Context, existing *prompt.Prompt, req adminPromptUpdateRequest, actorEmail string) (status int, errMsg string) {
	if status, errMsg := h.applyAdminPromptRename(ctx, existing, req); status != 0 {
		return status, errMsg
	}
	if errMsg := applyAdminPromptUpdateFields(existing, req); errMsg != "" {
		return http.StatusBadRequest, errMsg
	}
	if errMsg := applyAdminPromptStatus(existing, req, actorEmail); errMsg != "" {
		return http.StatusBadRequest, errMsg
	}
	return 0, ""
}

// applyAdminPromptRename validates and applies a name change, detecting a
// collision in the prompt's own name namespace. Returns (0, "") when there is no
// rename or it succeeds.
func (h *Handler) applyAdminPromptRename(ctx context.Context, existing *prompt.Prompt, req adminPromptUpdateRequest) (status int, errMsg string) {
	if req.Name == nil || *req.Name == existing.Name {
		return 0, ""
	}
	if err := prompt.ValidateName(*req.Name); err != nil {
		return http.StatusBadRequest, err.Error()
	}
	// Names are scoped: personal names are unique per owner, shared
	// (global/persona) names are globally unique. Check the namespace the prompt
	// actually lives in so a personal-prompt rename detects a same-owner
	// collision instead of surfacing an opaque DB error.
	var dup *prompt.Prompt
	if existing.Scope == prompt.ScopePersonal {
		dup, _ = h.deps.PromptStore.GetPersonal(ctx, existing.OwnerEmail, *req.Name)
	} else {
		dup, _ = h.deps.PromptStore.Get(ctx, *req.Name)
	}
	if dup != nil && dup.ID != existing.ID {
		return http.StatusConflict, "prompt name already exists"
	}
	existing.Name = *req.Name
	return 0, ""
}

// applyAdminPromptStatus applies a lifecycle status transition if requested.
// Admin API callers are admins, so approval is permitted. Returns a non-empty
// error message on an invalid or unauthorized transition.
func applyAdminPromptStatus(existing *prompt.Prompt, req adminPromptUpdateRequest, actorEmail string) string {
	if req.Status == nil {
		return ""
	}
	supersededBy := ""
	if req.SupersededBy != nil {
		supersededBy = *req.SupersededBy
	}
	if err := existing.ApplyStatusTransition(*req.Status, supersededBy, actorEmail, true, time.Now().UTC()); err != nil {
		return err.Error()
	}
	return ""
}

// applyAdminPromptUpdateFields applies non-nil fields from the update request to the prompt.
// Returns a non-empty error message on validation failure.
func applyAdminPromptUpdateFields(existing *prompt.Prompt, req adminPromptUpdateRequest) string {
	applyAdminPromptContentFields(existing, req)
	if req.Scope != nil {
		if err := prompt.ValidateScope(*req.Scope); err != nil {
			return err.Error()
		}
		existing.Scope = *req.Scope
	}
	if req.Tags != nil {
		if err := prompt.ValidateTags(req.Tags); err != nil {
			return err.Error()
		}
	}
	applyAdminPromptMetaFields(existing, req)
	return ""
}

// applyAdminPromptContentFields applies content-related fields from the update request.
func applyAdminPromptContentFields(existing *prompt.Prompt, req adminPromptUpdateRequest) {
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
}

// applyAdminPromptMetaFields applies metadata fields from the update request.
func applyAdminPromptMetaFields(existing *prompt.Prompt, req adminPromptUpdateRequest) {
	if req.Personas != nil {
		existing.Personas = req.Personas
	}
	if req.Tags != nil {
		existing.Tags = req.Tags
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
}

// deletePrompt deletes a prompt by ID.
//
// @Summary      Delete prompt
// @Description  Deletes a prompt by ID and unregisters it from the live MCP server.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id} [delete]
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

	// Unregister the name-keyed runtime metadata. Personal prompts are not
	// tracked there (names collide across owners), so skip them.
	if h.deps.PromptRegistrar != nil && existing.Scope != prompt.ScopePersonal {
		h.deps.PromptRegistrar.UnregisterRuntimePrompt(existing.Name)
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: statusDeleted})
}
