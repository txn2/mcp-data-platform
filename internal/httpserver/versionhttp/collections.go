package versionhttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// This file serves the prompt collection routes (#1010): collection CRUD and
// per-prompt assignment. Collections are org-visible named groups; any portal
// user can create one and read them all, mutation of a collection is limited
// to its creator or an admin, and assignment follows the prompt's own
// mutation rule (owner for a personal prompt, admin for shared, system rows
// read-only).

const errCollectionNot = "collection not found"

// maxCollectionBodyBytes bounds the JSON bodies the collection routes decode,
// mirroring the admin surface's request cap.
const maxCollectionBodyBytes = 1 << 20 // 1 MiB

// registerAdminCollections mounts the admin collection routes under prefix
// when the store has the collection capability.
func (h *Handler) registerAdminCollections(mux *http.ServeMux, prefix string, wrap func(http.Handler) http.Handler) {
	if h.deps.Collections == nil {
		return
	}
	mux.Handle("GET "+prefix+"/prompt-collections", wrap(http.HandlerFunc(h.listCollections)))
	mux.Handle("POST "+prefix+"/prompt-collections", wrap(http.HandlerFunc(h.adminCreateCollection)))
	mux.Handle("PUT "+prefix+"/prompt-collections/{id}", wrap(http.HandlerFunc(h.adminUpdateCollection)))
	mux.Handle("DELETE "+prefix+"/prompt-collections/{id}", wrap(http.HandlerFunc(h.adminDeleteCollection)))
	mux.Handle("PUT "+prefix+"/prompts/{id}/collection", wrap(http.HandlerFunc(h.adminAssignCollection)))
}

// registerPortalCollections mounts the portal collection routes when the
// store has the collection capability.
func (h *Handler) registerPortalCollections(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if h.deps.Collections == nil {
		return
	}
	mux.Handle("GET /api/v1/portal/prompt-collections", wrap(h.portalHandler(h.portalListCollections)))
	mux.Handle("POST /api/v1/portal/prompt-collections", wrap(h.portalHandler(h.portalCreateCollection)))
	mux.Handle("PUT /api/v1/portal/prompt-collections/{id}", wrap(h.portalHandler(h.portalUpdateCollection)))
	mux.Handle("DELETE /api/v1/portal/prompt-collections/{id}", wrap(h.portalHandler(h.portalDeleteCollection)))
	mux.Handle("PUT /api/v1/portal/prompts/{id}/collection", wrap(h.portalHandler(h.portalAssignCollection)))
}

// collectionListResponse is the collection listing payload.
type collectionListResponse struct {
	Data  []prompt.Collection `json:"data"`
	Total int                 `json:"total" example:"3"`
}

// collectionRequest is the create/update request body.
type collectionRequest struct {
	Name        string `json:"name" example:"Sales Reporting"`
	Description string `json:"description" example:"Weekly and daily sales SOPs"`
}

// assignCollectionRequest is the prompt assignment request body. An empty
// collection_id clears the assignment.
type assignCollectionRequest struct {
	CollectionID string `json:"collection_id" example:"col_a1b2c3d4"`
}

// listCollections returns every collection with its prompt count. Shared by
// the admin route directly and the portal route via portalListCollections:
// collections are org-visible, so both surfaces serve the same listing.
//
// @Summary      List prompt collections
// @Description  Returns every prompt collection with its member prompt count, ordered by name.
// @Tags         Prompts
// @Produce      json
// @Success      200  {object}  collectionListResponse
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompt-collections [get]
func (h *Handler) listCollections(w http.ResponseWriter, r *http.Request) {
	cols, err := h.deps.Collections.ListCollections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list collections")
		return
	}
	writeJSON(w, http.StatusOK, collectionListResponse{Data: cols, Total: len(cols)})
}

// portalListCollections returns the org-visible collection listing.
//
// @Summary      List prompt collections (portal)
// @Description  Returns every prompt collection with its member prompt count, ordered by name.
// @Tags         Prompts
// @Produce      json
// @Success      200  {object}  collectionListResponse
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompt-collections [get]
func (h *Handler) portalListCollections(w http.ResponseWriter, r *http.Request, _ *PortalIdentity) {
	h.listCollections(w, r)
}

// createCollection validates the request body and persists a new collection
// attributed to creator, writing the response.
func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request, creator string) {
	req, ok := decodeCollectionRequest(w, r)
	if !ok {
		return
	}
	col := &prompt.Collection{Name: req.Name, Description: req.Description, CreatedBy: creator}
	if err := h.deps.Collections.CreateCollection(r.Context(), col); err != nil {
		writeCollectionWriteError(w, err, "failed to create collection")
		return
	}
	writeJSON(w, http.StatusCreated, col)
}

// adminCreateCollection creates a collection attributed to the admin.
//
// @Summary      Create prompt collection
// @Description  Creates a named collection organizing the prompt library. Names are unique case-insensitively.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        request  body  collectionRequest  true  "Collection"
// @Success      201  {object}  prompt.Collection
// @Failure      400  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompt-collections [post]
func (h *Handler) adminCreateCollection(w http.ResponseWriter, r *http.Request) {
	h.createCollection(w, r, h.deps.AdminEmail(r))
}

// portalCreateCollection creates a collection attributed to the caller. Any
// portal user can create a collection (prompt owners organize their own
// prompts); mutating an existing one is creator-or-admin.
//
// @Summary      Create prompt collection (portal)
// @Description  Creates a named collection organizing the prompt library. Names are unique case-insensitively.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        request  body  collectionRequest  true  "Collection"
// @Success      201  {object}  prompt.Collection
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompt-collections [post]
func (h *Handler) portalCreateCollection(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	h.createCollection(w, r, user.Email)
}

// updateCollection renames or re-describes an existing collection after the
// caller-specific permission check has passed.
func (h *Handler) updateCollection(w http.ResponseWriter, r *http.Request, col *prompt.Collection) {
	req, ok := decodeCollectionRequest(w, r)
	if !ok {
		return
	}
	if err := h.deps.Collections.UpdateCollection(r.Context(), col.ID, req.Name, req.Description); err != nil {
		writeCollectionWriteError(w, err, "failed to update collection")
		return
	}
	col.Name = req.Name
	col.Description = req.Description
	writeJSON(w, http.StatusOK, col)
}

// adminUpdateCollection renames or re-describes any collection.
//
// @Summary      Update prompt collection
// @Description  Renames or re-describes a collection.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id       path  string             true  "Collection ID"
// @Param        request  body  collectionRequest  true  "Collection"
// @Success      200  {object}  prompt.Collection
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompt-collections/{id} [put]
func (h *Handler) adminUpdateCollection(w http.ResponseWriter, r *http.Request) {
	col, ok := h.loadCollection(w, r)
	if !ok {
		return
	}
	h.updateCollection(w, r, col)
}

// portalUpdateCollection renames or re-describes a collection the caller
// created; admins may update any.
//
// @Summary      Update prompt collection (portal)
// @Description  Renames or re-describes a collection the caller created; admins may update any collection.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id       path  string             true  "Collection ID"
// @Param        request  body  collectionRequest  true  "Collection"
// @Success      200  {object}  prompt.Collection
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompt-collections/{id} [put]
func (h *Handler) portalUpdateCollection(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	col, ok := h.loadOwnedCollection(w, r, user)
	if !ok {
		return
	}
	h.updateCollection(w, r, col)
}

// adminDeleteCollection deletes any collection, releasing its prompts.
//
// @Summary      Delete prompt collection
// @Description  Deletes a collection; member prompts are released to the default (uncollected) group.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompt-collections/{id} [delete]
func (h *Handler) adminDeleteCollection(w http.ResponseWriter, r *http.Request) {
	col, ok := h.loadCollection(w, r)
	if !ok {
		return
	}
	h.deleteCollection(w, r, col)
}

// portalDeleteCollection deletes a collection the caller created; admins may
// delete any.
//
// @Summary      Delete prompt collection (portal)
// @Description  Deletes a collection the caller created; admins may delete any collection. Member prompts are released to the default (uncollected) group.
// @Tags         Prompts
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompt-collections/{id} [delete]
func (h *Handler) portalDeleteCollection(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	col, ok := h.loadOwnedCollection(w, r, user)
	if !ok {
		return
	}
	h.deleteCollection(w, r, col)
}

// deleteCollection removes the collection after permission checks.
func (h *Handler) deleteCollection(w http.ResponseWriter, r *http.Request, col *prompt.Collection) {
	if err := h.deps.Collections.DeleteCollection(r.Context(), col.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete collection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// adminAssignCollection assigns any non-system prompt to a collection.
//
// @Summary      Assign prompt to collection
// @Description  Places a prompt in a collection, or clears the assignment with an empty collection_id. Placement is organizational metadata: no version is produced and no review is triggered.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id       path  string                   true  "Prompt ID"
// @Param        request  body  assignCollectionRequest  true  "Assignment"
// @Success      200  {object}  prompt.Prompt
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/prompts/{id}/collection [put]
func (h *Handler) adminAssignCollection(w http.ResponseWriter, r *http.Request) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return
	}
	h.assignCollection(w, r, pr)
}

// portalAssignCollection assigns a prompt the caller may organize: their own
// personal prompt, or any shared prompt when the caller is an admin.
//
// @Summary      Assign prompt to collection (portal)
// @Description  Places a prompt in a collection, or clears the assignment with an empty collection_id. Owners organize their own prompts; admins organize shared prompts.
// @Tags         Prompts
// @Accept       json
// @Produce      json
// @Param        id       path  string                   true  "Prompt ID"
// @Param        request  body  assignCollectionRequest  true  "Assignment"
// @Success      200  {object}  prompt.Prompt
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/prompts/{id}/collection [put]
func (h *Handler) portalAssignCollection(w http.ResponseWriter, r *http.Request, user *PortalIdentity) {
	pr, ok := h.loadPrompt(w, r)
	if !ok {
		return
	}
	if !user.IsAdmin && (pr.Scope != prompt.ScopePersonal || pr.OwnerEmail != user.Email) {
		writeError(w, http.StatusForbidden, "only the owner or an admin can organize this prompt")
		return
	}
	h.assignCollection(w, r, pr)
}

// assignCollection applies the assignment body to the prompt and returns the
// updated prompt. System rows are read-only config mirrors on every surface,
// so the guard lives here in the shared body.
func (h *Handler) assignCollection(w http.ResponseWriter, r *http.Request, pr *prompt.Prompt) {
	if pr.Source == prompt.SourceSystem {
		writeError(w, http.StatusForbidden, "system prompts are read-only")
		return
	}
	var req assignCollectionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.deps.Collections.SetPromptCollection(r.Context(), pr.ID, req.CollectionID); err != nil {
		if errors.Is(err, prompt.ErrCollectionNotFound) {
			writeError(w, http.StatusNotFound, errCollectionNot)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to assign collection")
		return
	}
	pr.CollectionID = req.CollectionID
	writeJSON(w, http.StatusOK, pr)
}

// decodeCollectionRequest parses and validates a create/update body.
func decodeCollectionRequest(w http.ResponseWriter, r *http.Request) (collectionRequest, bool) {
	var req collectionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCollectionBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, false
	}
	if err := prompt.ValidateCollectionName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return req, false
	}
	if err := prompt.ValidateCollectionDescription(req.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return req, false
	}
	return req, true
}

// loadCollection resolves the {id} path param to a collection, writing the
// error response when it is missing.
func (h *Handler) loadCollection(w http.ResponseWriter, r *http.Request) (*prompt.Collection, bool) {
	col, err := h.deps.Collections.GetCollection(r.Context(), r.PathValue(pathID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get collection")
		return nil, false
	}
	if col == nil {
		writeError(w, http.StatusNotFound, errCollectionNot)
		return nil, false
	}
	return col, true
}

// loadOwnedCollection resolves the collection and enforces the portal
// mutation rule: the creator or an admin.
func (h *Handler) loadOwnedCollection(w http.ResponseWriter, r *http.Request, user *PortalIdentity) (*prompt.Collection, bool) {
	col, ok := h.loadCollection(w, r)
	if !ok {
		return nil, false
	}
	if !user.IsAdmin && col.CreatedBy != user.Email {
		writeError(w, http.StatusForbidden, "only the collection's creator or an admin can modify it")
		return nil, false
	}
	return col, true
}

// writeCollectionWriteError maps a collection write failure: a name collision
// surfaces as 409, anything else is a 500 with a generic message.
func writeCollectionWriteError(w http.ResponseWriter, err error, public string) {
	if errors.Is(err, prompt.ErrCollectionExists) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, public)
}
