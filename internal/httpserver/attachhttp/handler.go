// Package attachhttp exposes prompt resource attachments (#1013) over REST for
// both operator surfaces: the portal (a prompt author manages the materials on
// their own prompts) and the admin API (any prompt). It lives beside pkg/prompt
// rather than inside pkg/portal or pkg/admin so those packages stay within the
// package-size budget; the composition root mounts it under each surface's path
// prefix wrapped in that surface's own authentication middleware and injects
// the identity accessors, so this package never imports either surface.
//
// The scope rule is enforced here at attach time, not only at serve time. The
// serve-time check protects the reader; this one protects the author, who would
// otherwise learn that a shared SOP has unreachable materials only from someone
// else's failed run.
package attachhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Shared response literals.
const (
	errPromptNotFound   = "prompt not found"
	errResourceNotFound = "resource not found"
)

// Identity is the authenticated caller resolved by the injected accessor. It is
// the resource package's own claims type rather than a local shape, so the read
// rule applied here is byte-for-byte the one the resources REST surface applies,
// including the caller's full persona membership.
type Identity = resource.Claims

// Deps carries the collaborators the attachment handlers need. Every field
// except Caller is required; a nil Store, Attachments, or Resources leaves the
// routes unmounted rather than serving a half-working surface.
type Deps struct {
	Store       prompt.Store
	Attachments prompt.AttachmentStore
	Resources   resource.Store

	// Caller resolves the authenticated caller's permission claims, or nil when
	// the request carries no user. Injected per surface by the composition root.
	Caller func(r *http.Request) *Identity
}

// Handler serves the prompt attachment routes.
type Handler struct {
	deps Deps
}

// New builds the handler. It returns nil when a required collaborator is
// missing, so the composition root can skip mounting without a second check.
func New(deps Deps) *Handler {
	if deps.Store == nil || deps.Attachments == nil || deps.Resources == nil || deps.Caller == nil {
		return nil
	}
	return &Handler{deps: deps}
}

// Register mounts the attachment routes under prefix, each wrapped in the
// surface's auth middleware. Both surfaces expose the same routes; they differ
// only in the identity the injected accessor resolves, which is what decides
// whether the caller may edit the prompt.
func (h *Handler) Register(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	if h == nil {
		return
	}
	mux.Handle("GET "+prefix+"/prompts/{id}/attachments", wrap(h.authed(h.list)))
	mux.Handle("POST "+prefix+"/prompts/{id}/attachments", wrap(h.authed(h.attach)))
	mux.Handle("PUT "+prefix+"/prompts/{id}/attachments", wrap(h.authed(h.reorder)))
	mux.Handle("DELETE "+prefix+"/prompts/{id}/attachments/{resourceID}", wrap(h.authed(h.detach)))
	mux.Handle("GET "+prefix+"/resources/{id}/prompts", wrap(h.authed(h.byResource)))
}

// authed resolves the caller first, responding 401 when the request carries no
// user. Every route in this package registers through it.
func (h *Handler) authed(fn func(w http.ResponseWriter, r *http.Request, who *Identity)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := h.deps.Caller(r)
		if who == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		fn(w, r, who)
	})
}

// AttachmentView is one attachment as the portal renders it: the link plus
// enough of the resource to show a row, and a broken flag when the resource
// behind the link no longer exists.
type AttachmentView struct {
	ResourceID  string `json:"resource_id"`
	Position    int    `json:"position"`
	AttachedBy  string `json:"attached_by,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	URI         string `json:"uri,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ScopeID     string `json:"scope_id,omitempty"`

	// Broken marks an attachment whose resource was deleted. The row stays in
	// the list so an author can see and remove the dangling link; without it a
	// deleted template would simply vanish from the editor while the served
	// prompt kept reporting a missing material.
	Broken bool `json:"broken,omitempty"`

	// Unreadable marks an attachment that exists but that this caller cannot
	// read. It carries the position (so the row renders in place) and nothing
	// else about the resource or its attacher, for the same reason the serving
	// path withholds them.
	Unreadable bool `json:"unreadable,omitempty"`
}

// listResponse is the attachment list payload.
type listResponse struct {
	Data  []AttachmentView `json:"data"`
	Total int              `json:"total"`
}

// list returns a prompt's attachments in authored order, to any caller who can
// see the prompt itself.
//
// @Summary      List prompt attachments
// @Description  Returns the reference material attached to a prompt, in authored order. Attachments whose resource was deleted are returned flagged as broken; attachments outside the caller's scope are flagged unreadable and carry no metadata.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Prompt ID"
// @Success      200  {object}  listResponse
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/attachments [get]
func (h *Handler) list(w http.ResponseWriter, r *http.Request, who *Identity) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return
	}
	// Visibility, not just existence: another user's personal prompt must not
	// disclose its attachment list, which names the material and its attacher.
	if !canViewPrompt(pr, who) {
		writeError(w, http.StatusNotFound, errPromptNotFound)
		return
	}
	views, err := h.views(r, pr.ID, who)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list attachments")
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Data: views, Total: len(views)})
}

// views resolves a prompt's attachment rows into renderable views.
func (h *Handler) views(r *http.Request, promptID string, who *Identity) ([]AttachmentView, error) {
	links, err := h.deps.Attachments.ListByPrompt(r.Context(), promptID)
	if err != nil {
		return nil, fmt.Errorf("listing attachments: %w", err)
	}
	out := make([]AttachmentView, 0, len(links))
	for _, link := range links {
		view := AttachmentView{
			ResourceID: link.ResourceID,
			Position:   link.Position,
			AttachedBy: link.AttachedBy,
		}
		res, getErr := h.deps.Resources.Get(r.Context(), link.ResourceID)
		switch {
		case resource.IsNotFound(getErr) || (getErr == nil && res == nil):
			// The link outlives its resource by design, so a deleted resource
			// is a flagged row rather than a failed request: this list is where
			// the author repairs it.
			view.Broken = true
		case getErr != nil:
			return nil, fmt.Errorf("reading attached resource: %w", getErr)
		case !resource.CanReadResource(*who, res):
			// Carry nothing but the position: the attacher's email is another
			// person's identity, and the caller has no business with it here.
			view.AttachedBy = ""
			view.Unreadable = true
		default:
			fillView(&view, res)
		}
		out = append(out, view)
	}
	return out, nil
}

// fillView copies the renderable resource fields onto a view.
func fillView(view *AttachmentView, res *resource.Resource) {
	view.DisplayName = res.DisplayName
	view.Description = res.Description
	view.Path = res.Path
	view.MIMEType = res.MIMEType
	view.SizeBytes = res.SizeBytes
	view.URI = res.URI
	view.Scope = string(res.Scope)
	view.ScopeID = res.ScopeID
}

// attachRequest is the POST body.
type attachRequest struct {
	ResourceID string `json:"resource_id"`
}

// attach links a resource to a prompt after checking that the caller may edit
// the prompt, may read the resource, and that the resource is at least as
// widely visible as the prompt.
//
// @Summary      Attach a resource to a prompt
// @Description  Links a managed resource to a prompt as reference material. The resource must be readable by the caller and at least as widely visible as the prompt; a narrower resource is refused with 409 and a message naming it. Returns the prompt's new attachment list.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id       path  string         true  "Prompt ID"
// @Param        request  body  attachRequest  true  "Resource to attach"
// @Success      200  {object}  listResponse
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/attachments [post]
func (h *Handler) attach(w http.ResponseWriter, r *http.Request, who *Identity) {
	pr, ok := h.loadEditablePrompt(w, r, who)
	if !ok {
		return
	}
	var req attachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ResourceID) == "" {
		writeError(w, http.StatusBadRequest, "resource_id is required")
		return
	}
	res, ok := h.loadReadableResource(w, r, who, req.ResourceID)
	if !ok {
		return
	}
	if !checkAttachAllowed(w, pr, who, res) {
		return
	}
	if err := h.deps.Attachments.Attach(r.Context(), prompt.Attachment{
		PromptID:   pr.ID,
		ResourceID: res.ID,
		AttachedBy: who.Email,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach resource")
		return
	}
	views, err := h.views(r, pr.ID, who)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list attachments")
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Data: views, Total: len(views)})
}

// loadReadableResource reads the resource being attached, writing the error
// response on failure. A resource the caller cannot read is reported as not
// found, not as forbidden: distinguishing the two would let a caller probe for
// the existence of resources outside their scope.
func (h *Handler) loadReadableResource(w http.ResponseWriter, r *http.Request, who *Identity, id string) (*resource.Resource, bool) {
	res, err := h.deps.Resources.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read resource")
		return nil, false
	}
	// Readable OR administrable: an operator who may write a persona's or a
	// user's resources may attach them, even though resource visibility itself
	// grants no cross-scope reads. Without this the documented persona path is
	// unreachable, since only admins may edit a shared prompt while
	// resource.VisibleScopes grants an admin no persona they do not belong to.
	if res == nil || (!resource.CanReadResource(*who, res) && !resource.CanWriteScope(*who, res.Scope, res.ScopeID)) {
		writeError(w, http.StatusNotFound, errResourceNotFound)
		return nil, false
	}
	return res, true
}

// checkAttachAllowed applies the two authoring-time rules, writing a conflict
// naming the resource when either refuses. Free function: the rules live in
// pkg/prompt and need no handler state.
func checkAttachAllowed(w http.ResponseWriter, pr *prompt.Prompt, who *Identity, res *resource.Resource) bool {
	scope := attachserve.ScopeOf(res)
	if err := prompt.CheckAttachScope(pr.Scope, pr.Personas, scope); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return false
	}
	if err := prompt.CheckAttachOwnership(who.Sub, who.Email, pr.OwnerEmail, scope); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return false
	}
	return true
}

// detach removes one link.
//
// @Summary      Detach a resource from a prompt
// @Description  Removes one attachment link. The resource itself is untouched.
// @Tags         Prompts
// @Produce      json
// @Param        id          path  string  true  "Prompt ID"
// @Param        resourceID  path  string  true  "Resource ID"
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/attachments/{resourceID} [delete]
func (h *Handler) detach(w http.ResponseWriter, r *http.Request, who *Identity) {
	pr, ok := h.loadEditablePrompt(w, r, who)
	if !ok {
		return
	}
	err := h.deps.Attachments.Detach(r.Context(), pr.ID, r.PathValue("resourceID"))
	switch {
	case errors.Is(err, prompt.ErrAttachmentNotFound):
		writeError(w, http.StatusNotFound, "attachment not found")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to detach resource")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "detached"})
	}
}

// reorderRequest is the PUT body: the prompt's attachments in the order the
// author wants them served.
type reorderRequest struct {
	ResourceIDs []string `json:"resource_ids"`
}

// reorder rewrites the authored order of a prompt's attachments.
//
// @Summary      Reorder prompt attachments
// @Description  Rewrites the authored order of a prompt's attachments. The order is what an agent receives them in. An id that is not already attached is refused; omitting a currently attached id detaches it.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id       path  string          true  "Prompt ID"
// @Param        request  body  reorderRequest  true  "Attachments in the desired order"
// @Success      200  {object}  listResponse
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/attachments [put]
func (h *Handler) reorder(w http.ResponseWriter, r *http.Request, who *Identity) {
	pr, ok := h.loadEditablePrompt(w, r, who)
	if !ok {
		return
	}
	var req reorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := h.deps.Attachments.Reorder(r.Context(), pr.ID, req.ResourceIDs)
	switch {
	case errors.Is(err, prompt.ErrAttachmentNotFound):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to reorder attachments")
		return
	}
	views, viewErr := h.views(r, pr.ID, who)
	if viewErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to list attachments")
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Data: views, Total: len(views)})
}

// promptRef names a prompt that attaches a resource.
type promptRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Scope       string `json:"scope"`
}

// byResource lists the prompts that attach a resource, so the resource detail
// view can answer "what depends on this file?" before someone deletes it.
// Prompts the caller cannot see are omitted rather than counted: the answer is
// scoped to the asker like every other read.
//
// @Summary      List prompts attaching a resource
// @Description  Returns the prompts that attach a resource, so the cost of editing or deleting it is visible first. Scoped to the caller: another user's personal prompt is never disclosed.
// @Tags         Resources
// @Produce      json
// @Param        id  path  string  true  "Resource ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/resources/{id}/prompts [get]
func (h *Handler) byResource(w http.ResponseWriter, r *http.Request, who *Identity) {
	res, err := h.deps.Resources.Get(r.Context(), r.PathValue("id"))
	if err != nil && !resource.IsNotFound(err) {
		writeError(w, http.StatusInternalServerError, "failed to read resource")
		return
	}
	if res == nil || !resource.CanReadResource(*who, res) {
		writeError(w, http.StatusNotFound, errResourceNotFound)
		return
	}
	ids, err := h.deps.Attachments.ListByResource(r.Context(), res.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list prompts")
		return
	}
	refs := make([]promptRef, 0, len(ids))
	for _, id := range ids {
		pr, getErr := h.deps.Store.GetByID(r.Context(), id)
		if getErr != nil || pr == nil || !canViewPrompt(pr, who) {
			continue
		}
		refs = append(refs, promptRef{ID: pr.ID, Name: pr.Name, DisplayName: pr.DisplayName, Scope: pr.Scope})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": refs, "total": len(refs)})
}

// loadPrompt reads the {id} prompt, writing the error response on failure.
func (h *Handler) loadPrompt(w http.ResponseWriter, r *http.Request) (*prompt.Prompt, bool) {
	pr, err := h.deps.Store.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get prompt")
		return nil, false
	}
	if pr == nil {
		writeError(w, http.StatusNotFound, errPromptNotFound)
		return nil, false
	}
	return pr, true
}

// loadEditablePrompt reads the {id} prompt and refuses callers who may not
// change it. Attaching material to a prompt is editing that prompt.
func (h *Handler) loadEditablePrompt(w http.ResponseWriter, r *http.Request, who *Identity) (*prompt.Prompt, bool) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return nil, false
	}
	if !canEditPrompt(pr, who) {
		writeError(w, http.StatusForbidden, "you can only change attachments on prompts you own")
		return nil, false
	}
	return pr, true
}

// canEditPrompt reports whether the caller may change a prompt's attachments:
// admins may change any, an owner may change their own personal prompt. Shared
// scopes are admin-only, matching who may edit their content.
func canEditPrompt(pr *prompt.Prompt, who *Identity) bool {
	if who.IsAdmin {
		return true
	}
	return pr.Scope == prompt.ScopePersonal && strings.EqualFold(pr.OwnerEmail, who.Email)
}

// canViewPrompt reports whether the caller may see a prompt exists: global and
// persona prompts are listable by everyone, a personal prompt only by its owner
// or an admin.
func canViewPrompt(pr *prompt.Prompt, who *Identity) bool {
	if who.IsAdmin || pr.Scope != prompt.ScopePersonal {
		return true
	}
	return strings.EqualFold(pr.OwnerEmail, who.Email)
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
