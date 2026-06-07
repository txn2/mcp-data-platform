package portal

import (
	"net/http"
	"strings"
)

// searchMyAssets handles GET /api/v1/portal/assets/search.
//
// @Summary      Search my assets
// @Description  Ranks the current user's saved assets by relevance to q. Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Always scoped server-side to the requesting user's own assets.
// @Tags         Assets
// @Produce      json
// @Param        q      query  string   true   "Search query"
// @Param        limit  query  integer  false  "Max results (default: 20)"
// @Success      200  {object}  paginatedResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/search [get]
func (h *Handler) searchMyAssets(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	// owner_id is the sole server-side scoping key — the same key the asset
	// library and ownership checks use, so search returns exactly the assets the
	// caller sees in the library. A blank id would scope to the shared "" owner
	// bucket, so fail closed rather than run an unscoped search.
	if strings.TrimSpace(user.UserID) == "" {
		writeError(w, http.StatusForbidden, errSearchScopeRequired)
		return
	}

	searcher, ok := h.deps.AssetStore.(AssetSearcher)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "asset search is unavailable")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit := intParam(r, paramLimit, DefaultSearchLimit)
	scored, err := searcher.SearchAssets(r.Context(), AssetSearchQuery{
		Embedding: h.embedSearchQuery(r.Context(), query),
		QueryText: query,
		OwnerID:   user.UserID,
		Limit:     limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search assets")
		return
	}
	if scored == nil {
		scored = []ScoredAsset{}
	}

	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: scored, Total: len(scored), Limit: limit, Offset: 0,
	})
}

// searchMyCollections handles GET /api/v1/portal/collections/search.
//
// @Summary      Search my collections
// @Description  Ranks the current user's collections by relevance to q (matching name, description, and section titles/descriptions). Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Always scoped server-side to the requesting user's own collections.
// @Tags         Collections
// @Produce      json
// @Param        q      query  string   true   "Search query"
// @Param        limit  query  integer  false  "Max results (default: 20)"
// @Success      200  {object}  paginatedResponse
// @Failure      400  {object}  problemDetail
// @Failure      401  {object}  problemDetail
// @Failure      403  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Failure      503  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/search [get]
func (h *Handler) searchMyCollections(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	if strings.TrimSpace(user.UserID) == "" {
		writeError(w, http.StatusForbidden, errSearchScopeRequired)
		return
	}

	searcher, ok := h.deps.CollectionStore.(CollectionSearcher)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "collection search is unavailable")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit := intParam(r, paramLimit, DefaultSearchLimit)
	scored, err := searcher.SearchCollections(r.Context(), CollectionSearchQuery{
		Embedding: h.embedSearchQuery(r.Context(), query),
		QueryText: query,
		OwnerID:   user.UserID,
		Limit:     limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search collections")
		return
	}
	if scored == nil {
		scored = []ScoredCollection{}
	}

	writeJSON(w, http.StatusOK, paginatedResponse{
		Data: scored, Total: len(scored), Limit: limit, Offset: 0,
	})
}
