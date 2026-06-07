package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// SharedPrompt is a prompt shared with the current user, with share metadata,
// for the "Shared With Me" listing.
type SharedPrompt struct {
	Prompt     prompt.Prompt   `json:"prompt"`
	ShareID    string          `json:"share_id"`
	SharedBy   string          `json:"shared_by"`
	SharedAt   time.Time       `json:"shared_at"`
	Permission SharePermission `json:"permission"`
}

// PromptStore provides prompt persistence for the portal.
type PromptStore interface {
	Create(ctx context.Context, p *prompt.Prompt) error
	Get(ctx context.Context, name string) (*prompt.Prompt, error)
	GetPersonal(ctx context.Context, ownerEmail, name string) (*prompt.Prompt, error)
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

// Shared messages/keys for the prompt portal handlers.
const (
	errMsgAuthRequired   = "authentication required"
	errMsgGetPrompt      = "failed to get prompt"
	errMsgPromptNotFound = "prompt not found"
)

// registerPromptRoutes registers user-facing prompt routes if the store is available.
func (h *Handler) registerPromptRoutes() {
	if h.deps.PromptStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/portal/prompts", h.listMyPrompts)
	h.mux.HandleFunc("GET /api/v1/portal/prompts/search", h.searchMyPrompts)
	h.mux.HandleFunc("POST /api/v1/portal/prompts", h.createMyPrompt)
	h.mux.HandleFunc("PUT /api/v1/portal/prompts/{id}", h.updateMyPrompt)
	h.mux.HandleFunc("DELETE /api/v1/portal/prompts/{id}", h.deleteMyPrompt)
	if h.deps.ShareStore != nil {
		h.mux.HandleFunc("POST /api/v1/portal/prompts/{id}/shares", h.createPromptShare)
		h.mux.HandleFunc("GET /api/v1/portal/prompts/{id}/shares", h.listPromptShares)
		h.mux.HandleFunc("GET /api/v1/portal/shared-prompts", h.listSharedPrompts)
	}
}

// portalPromptListResponse is the response for user prompt listing.
type portalPromptListResponse struct {
	Personal  []prompt.Prompt `json:"personal"`
	Available []prompt.Prompt `json:"available"`
}

// portalPromptCreateRequest is the request body for creating or updating a
// personal prompt. On update it can also carry a promotion request: setting
// requested_scope flags the prompt for the admin review queue.
type portalPromptCreateRequest struct {
	Name        string            `json:"name" example:"my-analysis-prompt"`
	DisplayName string            `json:"display_name" example:"My Analysis Prompt"`
	Description string            `json:"description" example:"A prompt for data analysis workflows"`
	Content     string            `json:"content" example:"Analyze the following data: {{data}}"`
	Arguments   []prompt.Argument `json:"arguments"`
	Category    string            `json:"category" example:"analysis"`
	Tags        []string          `json:"tags" example:"analysis,reporting"`

	// Promotion request (update only). RequestedScope of "persona" or "global"
	// flags the prompt for the admin queue; "" leaves any existing request as is.
	RequestedScope    string   `json:"requested_scope,omitempty" example:"persona"`
	RequestedPersonas []string `json:"requested_personas,omitempty" example:"analyst"`
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

// searchMyPrompts handles GET /api/v1/portal/prompts/search.
//
// @Summary      Search my prompts
// @Description  Ranks approved prompts visible to the caller by relevance to q. Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Visibility (global, matching-persona, and own personal prompts; all approved prompts for admins) is applied before ranking.
// @Tags         Prompts
// @Produce      json
// @Param        q      query  string   true   "Search query"
// @Param        limit  query  integer  false  "Max results (default: 20)"
// @Success      200  {object}  paginatedResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/search [get]
func (h *Handler) searchMyPrompts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
		return
	}

	searcher, ok := h.deps.PromptStore.(prompt.Searcher)
	if !ok {
		writePortalError(w, http.StatusServiceUnavailable, "prompt search is unavailable")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writePortalError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	isAdmin := hasAnyRole(user.Roles, h.deps.AdminRoles)
	persona := ""
	if pi := h.resolveUserPersona(user); pi != nil {
		persona = pi.Name
	}
	limit := intParam(r, paramLimit, prompt.DefaultSearchLimit)

	scored, err := searcher.Search(r.Context(), prompt.SearchQuery{
		Embedding:  h.embedSearchQuery(r.Context(), query),
		QueryText:  query,
		OwnerEmail: user.Email,
		Persona:    persona,
		IsAdmin:    isAdmin,
		Limit:      limit,
	})
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to search prompts")
		return
	}
	if scored == nil {
		scored = []prompt.ScoredPrompt{}
	}

	writePortalJSON(w, http.StatusOK, paginatedResponse{
		Data: scored, Total: len(scored), Limit: limit, Offset: 0,
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
	if err := prompt.ValidateTags(req.Tags); err != nil {
		writePortalError(w, http.StatusBadRequest, err.Error())
		return
	}

	p := &prompt.Prompt{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Content:     req.Content,
		Arguments:   req.Arguments,
		Category:    req.Category,
		Tags:        req.Tags,
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

	id := r.PathValue(pathKeyID)
	existing, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, errMsgGetPrompt)
		return
	}
	if existing == nil {
		writePortalError(w, http.StatusNotFound, errMsgPromptNotFound)
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

// reregisterPrompt unregisters the old name and re-registers if the prompt is
// enabled. Personal prompts are not tracked in the name-keyed runtime metadata
// (their names collide across owners), so unregistering by name could drop an
// unrelated global/persona entry; skip them entirely.
func reregisterPrompt(reg PromptRegistrar, oldName string, p *prompt.Prompt) {
	if reg == nil || p.Scope == prompt.ScopePersonal {
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
		// Portal prompts are personal; names are unique per owner.
		dup, _ := h.deps.PromptStore.GetPersonal(ctx, existing.OwnerEmail, existing.Name)
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
	if req.Tags != nil {
		if err := prompt.ValidateTags(req.Tags); err != nil {
			return err.Error()
		}
		existing.Tags = req.Tags
	}
	if req.RequestedScope != "" {
		if err := existing.ApplyPromotionRequest(req.RequestedScope, req.RequestedPersonas); err != nil {
			return err.Error()
		}
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

	id := r.PathValue(pathKeyID)
	existing, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, errMsgGetPrompt)
		return
	}
	if existing == nil {
		writePortalError(w, http.StatusNotFound, errMsgPromptNotFound)
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

	writePortalJSON(w, http.StatusOK, map[string]string{"status": statusDeleted})
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

// createPromptShare shares a personal prompt with another user. Only the prompt
// owner may share it. The recipient gets a real, runnable prompt (served over
// MCP as shared-<name>), not a markdown asset snapshot.
//
// @Summary      Share a prompt
// @Description  Shares the caller's personal prompt with another user by email or user id.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id    path  string              true  "Prompt ID"
// @Param        body  body  createShareRequest  true  "Share details"
// @Success      201  {object}  shareResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/shares [post]
// validatePromptRecipient checks the recipient email for a prompt share.
// Prompt shares are user-to-user by email only (no public-link/token view for
// prompts), and the MCP serving path matches recipients by email. It returns a
// user-facing error message, or "" when the recipient is valid.
func validatePromptRecipient(sharedWithEmail, ownerEmail string) string {
	recipient := strings.TrimSpace(sharedWithEmail)
	if recipient == "" {
		return "a recipient email is required to share a prompt"
	}
	if strings.EqualFold(recipient, ownerEmail) {
		return "you already own this prompt"
	}
	return ""
}

func (h *Handler) createPromptShare(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
		return
	}
	id := r.PathValue(pathKeyID)
	pr, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, errMsgGetPrompt)
		return
	}
	if pr == nil {
		writePortalError(w, http.StatusNotFound, errMsgPromptNotFound)
		return
	}
	// Only the owner of a personal prompt may share it.
	if pr.Scope != prompt.ScopePersonal || pr.OwnerEmail != user.Email {
		writePortalError(w, http.StatusForbidden, "only the owner can share this prompt")
		return
	}

	var req createShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePortalError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := validatePromptRecipient(req.SharedWithEmail, user.Email); msg != "" {
		writePortalError(w, http.StatusBadRequest, msg)
		return
	}
	req.SharedWithUserID = "" // prompt shares resolve by email
	share, buildErr := buildShare(shareTarget{PromptID: id}, user.Email, req)
	if buildErr != nil {
		writePortalError(w, http.StatusBadRequest, buildErr.Error())
		return
	}
	if err := h.deps.ShareStore.Insert(r.Context(), share); err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to create share")
		return
	}
	writePortalJSON(w, http.StatusCreated, shareResponse{Share: share})
}

// listPromptShares lists the shares an owner created for a prompt.
//
// @Summary      List prompt shares
// @Description  Returns all shares for a prompt. Only the owner can view them.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {array}   Share
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/shares [get]
func (h *Handler) listPromptShares(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
		return
	}
	id := r.PathValue(pathKeyID)
	pr, err := h.deps.PromptStore.GetByID(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, errMsgGetPrompt)
		return
	}
	if pr == nil {
		writePortalError(w, http.StatusNotFound, errMsgPromptNotFound)
		return
	}
	if pr.OwnerEmail != user.Email {
		writePortalError(w, http.StatusForbidden, "only the owner can view shares")
		return
	}
	shares, err := h.deps.ShareStore.ListByPrompt(r.Context(), id)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to list shares")
		return
	}
	if shares == nil {
		shares = []Share{}
	}
	writePortalJSON(w, http.StatusOK, shares)
}

// listSharedPrompts lists prompts shared with the current user.
//
// @Summary      List prompts shared with me
// @Description  Returns prompts other users have shared with the current user.
// @Tags         Prompts
// @Produce      json
// @Success      200  {array}   SharedPrompt
// @Failure      401  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/shared-prompts [get]
func (h *Handler) listSharedPrompts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writePortalError(w, http.StatusUnauthorized, errMsgAuthRequired)
		return
	}
	refs, err := h.deps.ShareStore.ListSharedPromptsWithUser(r.Context(), user.UserID, user.Email)
	if err != nil {
		writePortalError(w, http.StatusInternalServerError, "failed to list shared prompts")
		return
	}
	out := make([]SharedPrompt, 0, len(refs))
	for _, ref := range refs {
		pr, err := h.deps.PromptStore.GetByID(r.Context(), ref.PromptID)
		if err != nil || pr == nil {
			continue
		}
		out = append(out, SharedPrompt{
			Prompt:     *pr,
			ShareID:    ref.ShareID,
			SharedBy:   ref.SharedBy,
			SharedAt:   ref.SharedAt,
			Permission: ref.Permission,
		})
	}
	writePortalJSON(w, http.StatusOK, out)
}
