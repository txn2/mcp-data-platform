package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// errAdminCollectionNotFound is the error message for missing collections.
const errAdminCollectionNotFound = "collection not found"

// errAdminCollectionDeleted is the error message for soft-deleted collections.
const errAdminCollectionDeleted = "collection has been deleted"

// registerCollectionRoutes registers the admin asset-collection routes when a
// collection store is available.
//
// Collections had only the owner-scoped portal surface, so a collection owned
// by another principal was unreachable: nobody could list it, open it, or hand
// it on. That bites hardest for a non-human owner — an API-key session owns
// what it creates under an identity nobody can sign in as — because the assets
// it grouped stayed visible in the admin asset list while the grouping itself
// did not (#1292). These routes are the collection half of the admin surface
// assets have had all along.
func (h *Handler) registerCollectionRoutes() {
	if h.deps.CollectionStore == nil {
		return
	}
	h.mux.HandleFunc("GET /api/v1/admin/collections", h.listAllCollections)
	h.mux.HandleFunc("GET /api/v1/admin/collections/{id}", h.getAdminCollection)
	h.mux.HandleFunc("PUT /api/v1/admin/collections/{id}", h.updateAdminCollection)
	h.mux.HandleFunc("DELETE /api/v1/admin/collections/{id}", h.deleteAdminCollection)
}

// adminCollectionListResponse is the paginated response for admin collection listing.
type adminCollectionListResponse struct {
	Data           []portal.Collection            `json:"data"`
	Total          int                            `json:"total" example:"10"`
	Limit          int                            `json:"limit" example:"20"`
	Offset         int                            `json:"offset" example:"0"`
	ShareSummaries map[string]portal.ShareSummary `json:"share_summaries,omitempty"`
}

// listAllCollections returns all asset collections (no owner filter).
//
// @Summary      List all collections
// @Description  Returns all asset collections without owner restriction, with active share summaries.
// @Tags         Collections
// @Produce      json
// @Param        search  query  string  false  "Search term matching name, description, or owner email"
// @Param        limit   query  int     false  "Page size"
// @Param        offset  query  int     false  "Page offset"
// @Param        sort    query  string  false  "Sort column (default: updated_at)"  Enums(updated_at, created_at, name)
// @Param        dir     query  string  false  "Sort direction (default: desc)"     Enums(asc, desc)
// @Success      200  {object}  adminCollectionListResponse
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/collections [get]
func (h *Handler) listAllCollections(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := portal.CollectionFilter{
		Search: q.Get("search"),
		Limit:  limit,
		Offset: offset,
		// Sent as given: the store resolves both against its own allowlist,
		// so an unknown column orders by the default rather than failing.
		SortBy:  q.Get("sort"),
		SortDir: strings.ToUpper(q.Get("dir")),
	}

	collections, total, err := h.deps.CollectionStore.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list collections")
		return
	}
	if collections == nil {
		collections = []portal.Collection{}
	}

	var summaries map[string]portal.ShareSummary
	if len(collections) > 0 && h.deps.ShareStore != nil {
		ids := make([]string, len(collections))
		for i, c := range collections {
			ids[i] = c.ID
		}
		summaries, _ = h.deps.ShareStore.ListActiveCollectionShareSummaries(r.Context(), ids)
	}

	writeJSON(w, http.StatusOK, adminCollectionListResponse{
		Data:           collections,
		Total:          total,
		Limit:          filter.EffectiveLimit(),
		Offset:         filter.Offset,
		ShareSummaries: summaries,
	})
}

// getAdminCollection returns a single collection without owner restriction.
//
// @Summary      Get collection
// @Description  Returns a single asset collection by ID, with its sections and items, without owner restriction.
// @Tags         Collections
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  portaldomain.Collection
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/collections/{id} [get]
func (h *Handler) getAdminCollection(w http.ResponseWriter, r *http.Request) {
	coll := h.loadAdminCollection(w, r)
	if coll == nil {
		return
	}
	writeJSON(w, http.StatusOK, normalizeCollectionSections(coll))
}

// adminUpdateCollectionRequest is the request body for admin collection updates.
type adminUpdateCollectionRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// updateAdminCollection updates any collection's metadata without owner restriction.
//
// @Summary      Update collection
// @Description  Updates an asset collection's name and/or description without owner restriction.
// @Tags         Collections
// @Accept       json
// @Produce      json
// @Param        id    path  string                        true  "Collection ID"
// @Param        body  body  adminUpdateCollectionRequest  true  "Collection metadata to update"
// @Success      200  {object}  portaldomain.Collection
// @Failure      400  {object}  problemDetail
// @Failure      404  {object}  problemDetail
// @Failure      410  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/collections/{id} [put]
func (h *Handler) updateAdminCollection(w http.ResponseWriter, r *http.Request) {
	coll := h.loadAdminCollection(w, r)
	if coll == nil {
		return
	}

	var req adminUpdateCollectionRequest
	if err := decodeStrict(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The same merge-and-validate the portal route applies, so an admin edit
	// cannot write a name or description the owner's own edit would refuse.
	name, description, valErr := portal.ResolveCollectionUpdate(coll, req.Name, req.Description)
	if valErr != nil {
		writeError(w, http.StatusBadRequest, valErr.Error())
		return
	}

	id := r.PathValue(pathValueID)
	if err := h.deps.CollectionStore.Update(r.Context(), id, name, description); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update collection")
		return
	}

	updated, err := h.deps.CollectionStore.Get(r.Context(), id)
	if err != nil || updated == nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch updated collection")
		return
	}
	writeJSON(w, http.StatusOK, normalizeCollectionSections(updated))
}

// deleteAdminCollection soft-deletes any collection without owner restriction.
//
// @Summary      Delete collection
// @Description  Soft-deletes an asset collection without owner restriction. The assets it holds are not deleted.
// @Tags         Collections
// @Produce      json
// @Param        id  path  string  true  "Collection ID"
// @Success      200  {object}  statusResponse
// @Failure      404  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/collections/{id} [delete]
func (h *Handler) deleteAdminCollection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(pathValueID)
	if err := h.deps.CollectionStore.SoftDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "collection not found or already deleted")
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: statusDeleted})
}

// loadAdminCollection fetches the collection named by the request path, writing
// the 404 or 410 itself and returning nil when it cannot be served. A
// soft-deleted collection answers 410 as it does on the portal route: the admin
// list never shows one, so reaching it means a stale link rather than a record
// the admin still has something to do with.
func (h *Handler) loadAdminCollection(w http.ResponseWriter, r *http.Request) *portal.Collection {
	coll, err := h.deps.CollectionStore.Get(r.Context(), r.PathValue(pathValueID))
	if err != nil || coll == nil {
		writeError(w, http.StatusNotFound, errAdminCollectionNotFound)
		return nil
	}
	if coll.DeletedAt != nil {
		writeError(w, http.StatusGone, errAdminCollectionDeleted)
		return nil
	}
	return coll
}

// normalizeCollectionSections replaces nil section and item slices with empty
// ones so the response carries [] rather than null, matching the portal route
// the UI already reads.
func normalizeCollectionSections(coll *portal.Collection) *portal.Collection {
	if coll.Sections == nil {
		coll.Sections = []portal.CollectionSection{}
	}
	for i := range coll.Sections {
		if coll.Sections[i].Items == nil {
			coll.Sections[i].Items = []portal.CollectionItem{}
		}
	}
	return coll
}
