// Package versionhttp exposes prompt version history, draft review actions,
// audit-derived usage stats (#1009), and prompt collection management (#1010)
// over REST. It serves both operator surfaces — the admin API (any prompt,
// approve/reject) and the portal API (visible prompts, read-only history plus
// usage for the caller's visible set, collection CRUD and assignment) — from
// one implementation. It lives beside pkg/prompt rather than inside pkg/admin
// or pkg/portal so those packages stay within the package-size budget; the
// composition root (internal/httpserver) mounts it under each surface's path
// prefix wrapped in that surface's own authentication middleware, and injects
// the identity accessors, so this package never imports either surface.
package versionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Registrar refreshes a prompt's name-keyed runtime metadata after an
// approved version changes the live row. The prompt layer satisfies it.
type Registrar interface {
	RegisterRuntimePrompt(p *prompt.Prompt)
	UnregisterRuntimePrompt(name string)
}

// PortalIdentity is the portal caller resolved by the injected accessor.
type PortalIdentity struct {
	UserID  string
	Email   string
	Persona string
	IsAdmin bool
}

// Deps carries the collaborators the version handlers need. Store and
// Versions are required; Usage is optional (audit disabled leaves usage
// empty); Registrar is optional; Collections is optional (nil skips the
// collection routes). AdminEmail and PortalUser are the surface identity
// accessors injected by the composition root — each Register* call requires
// its accessor.
type Deps struct {
	Store       prompt.Store
	Versions    prompt.VersionStore
	Usage       prompt.UsageReader
	Registrar   Registrar
	Collections prompt.CollectionStore

	// AdminEmail returns the authenticated admin's email for approval stamps.
	AdminEmail func(r *http.Request) string
	// PortalUser resolves the authenticated portal caller, or nil when the
	// request carries no user.
	PortalUser func(r *http.Request) *PortalIdentity
	// SharedPromptIDs returns the ids of prompts shared person-to-person with
	// the caller, so their usage is as visible as the prompts themselves.
	// Optional: nil when the deployment has no portal share store.
	SharedPromptIDs func(ctx context.Context, userID, email string) ([]string, error)
}

// Handler serves the prompt version and usage routes.
type Handler struct {
	deps Deps
}

// New builds the handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// Shared literals.
const (
	pathID        = "id"
	pathVersion   = "version"
	errPromptNot  = "prompt not found"
	errVersionNot = "version not found"
	errGetPrompt  = "failed to get prompt"
	errListVers   = "failed to list versions"
)

// RegisterAdmin mounts the admin version routes under prefix (the admin API
// path prefix, e.g. /api/v1/admin), each wrapped in the admin auth middleware.
// The composition root registers these on the top-level mux, where their
// literal patterns take precedence over the admin subtree mount.
func (h *Handler) RegisterAdmin(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/prompts/usage", wrap(http.HandlerFunc(h.adminUsage)))
	mux.Handle("GET "+prefix+"/prompts/{id}/versions", wrap(http.HandlerFunc(h.adminListVersions)))
	mux.Handle("GET "+prefix+"/prompts/{id}/versions/{version}", wrap(http.HandlerFunc(h.adminGetVersion)))
	mux.Handle("POST "+prefix+"/prompts/{id}/versions/{version}/approve", wrap(http.HandlerFunc(h.approveVersion)))
	mux.Handle("POST "+prefix+"/prompts/{id}/versions/{version}/reject", wrap(http.HandlerFunc(h.rejectVersion)))
	h.registerAdminCollections(mux, prefix, wrap)
}

// RegisterPortal mounts the portal version routes, wrapped in the portal auth
// middleware. Every portal handler goes through portalHandler, which resolves
// the caller identity and 401s unauthenticated requests in one place.
func (h *Handler) RegisterPortal(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/portal/prompts/usage", wrap(h.portalHandler(h.portalUsage)))
	mux.Handle("GET /api/v1/portal/prompts/{id}/versions", wrap(h.portalHandler(h.portalListVersions)))
	h.registerPortalCollections(mux, wrap)
}

// portalHandler adapts a portal handler by resolving the caller identity
// first, responding 401 when the request carries no user. Every portal route
// in this package registers through it.
func (h *Handler) portalHandler(fn func(w http.ResponseWriter, r *http.Request, user *PortalIdentity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := h.deps.PortalUser(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		fn(w, r, user)
	})
}

// versionListResponse is the version history payload.
type versionListResponse struct {
	Data  []prompt.Version `json:"data"`
	Total int              `json:"total" example:"4"`
}

// adminListVersions returns a prompt's full version history, newest first.
//
// @Summary      List prompt versions
// @Description  Returns every version of a prompt with full content and author, newest first.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {object}  versionListResponse
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id}/versions [get]
func (h *Handler) adminListVersions(w http.ResponseWriter, r *http.Request) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return
	}
	h.writeVersionList(r.Context(), w, pr.ID)
}

// writeVersionList writes the version history for a prompt id.
func (h *Handler) writeVersionList(ctx context.Context, w http.ResponseWriter, promptID string) {
	versions, err := h.deps.Versions.ListVersions(ctx, promptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errListVers)
		return
	}
	if versions == nil {
		versions = []prompt.Version{}
	}
	writeJSON(w, http.StatusOK, versionListResponse{Data: versions, Total: len(versions)})
}

// adminGetVersion returns one version with its full content.
//
// @Summary      Get prompt version
// @Description  Returns a single prompt version snapshot.
// @Tags         Prompts
// @Produce      json
// @Param        id       path  string  true  "Prompt ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {object}  prompt.Version
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id}/versions/{version} [get]
func (h *Handler) adminGetVersion(w http.ResponseWriter, r *http.Request) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	v, err := h.deps.Versions.GetVersion(r.Context(), pr.ID, n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get version")
		return
	}
	if v == nil {
		writeError(w, http.StatusNotFound, errVersionNot)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// approveVersion applies a pending draft version to the live prompt.
//
// @Summary      Approve prompt version
// @Description  Applies a pending draft version's snapshot to the live prompt, stamping the approval on that specific version. Other pending drafts are superseded.
// @Tags         Prompts
// @Produce      json
// @Param        id       path  string  true  "Prompt ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {object}  prompt.Prompt
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id}/versions/{version}/approve [post]
func (h *Handler) approveVersion(w http.ResponseWriter, r *http.Request) {
	pr, n, ok := h.loadDraft(w, r)
	if !ok {
		return
	}
	updated, err := h.deps.Versions.ApproveVersion(r.Context(), pr.ID, n, h.deps.AdminEmail(r))
	if err != nil {
		// The draft state was validated above, so a conflict here is a race
		// that changed it; anything else is an internal store failure whose
		// detail (possibly driver text) must not reach the client.
		writeVersionWriteError(w, err, "failed to approve version")
		return
	}
	if h.deps.Registrar != nil && updated.Scope != prompt.ScopePersonal {
		h.deps.Registrar.UnregisterRuntimePrompt(updated.Name)
		if updated.Enabled {
			h.deps.Registrar.RegisterRuntimePrompt(updated)
		}
	}
	writeJSON(w, http.StatusOK, updated)
}

// rejectVersion marks a pending draft version rejected.
//
// @Summary      Reject prompt version
// @Description  Rejects a pending draft version, leaving the live prompt unchanged.
// @Tags         Prompts
// @Produce      json
// @Param        id       path  string  true  "Prompt ID"
// @Param        version  path  int     true  "Version number"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id}/versions/{version}/reject [post]
func (h *Handler) rejectVersion(w http.ResponseWriter, r *http.Request) {
	pr, n, ok := h.loadDraft(w, r)
	if !ok {
		return
	}
	if err := h.deps.Versions.RejectVersion(r.Context(), pr.ID, n); err != nil {
		writeVersionWriteError(w, err, "failed to reject version")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// writeVersionWriteError maps a version-write failure: a state conflict
// (prompt.ErrVersionConflict) surfaces its message as 409, anything else is a
// 500 with a generic message so store/driver detail never reaches the client.
func writeVersionWriteError(w http.ResponseWriter, err error, public string) {
	if errors.Is(err, prompt.ErrVersionConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, public)
}

// loadDraft resolves the prompt and version path params and verifies the
// version is a pending draft, writing the error response otherwise.
func (h *Handler) loadDraft(w http.ResponseWriter, r *http.Request) (*prompt.Prompt, int, bool) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return nil, 0, false
	}
	n, ok := versionParam(w, r)
	if !ok {
		return nil, 0, false
	}
	v, err := h.deps.Versions.GetVersion(r.Context(), pr.ID, n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get version")
		return nil, 0, false
	}
	if v == nil {
		writeError(w, http.StatusNotFound, errVersionNot)
		return nil, 0, false
	}
	if v.Status != prompt.VersionStatusDraft {
		writeError(w, http.StatusConflict, "version "+strconv.Itoa(n)+" is "+v.Status+", not a pending draft")
		return nil, 0, false
	}
	return pr, n, true
}

// loadPrompt resolves the {id} path param to a stored prompt, writing the
// error response when it is missing.
func (h *Handler) loadPrompt(w http.ResponseWriter, r *http.Request) (*prompt.Prompt, bool) {
	pr, err := h.deps.Store.GetByID(r.Context(), r.PathValue(pathID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, errGetPrompt)
		return nil, false
	}
	if pr == nil {
		writeError(w, http.StatusNotFound, errPromptNot)
		return nil, false
	}
	return pr, true
}

// versionParam parses the {version} path param.
func versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue(pathVersion))
	if err != nil || n < 1 {
		writeError(w, http.StatusNotFound, errVersionNot)
		return 0, false
	}
	return n, true
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
