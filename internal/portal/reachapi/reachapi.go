// Package reachapi serves the portal's search and facets over a person's
// reach (#2500): GET /api/v1/portal/assets/search, /assets/facets,
// /collections/search and /collections/facets, and the per-row description
// (share summaries, permissions) the portal's listings attach to a page.
//
// Every route asks the store for what the caller can open, narrowed to the
// scope the request names; the store applies the reach in SQL
// (internal/portal/reachstore), so a row the caller cannot open is never
// returned, ranked or counted. It is built only by pkg/portal, which mounts it
// and calls its row description from the listing routes it still owns.
package reachapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// Query parameters the reach routes read.
const (
	paramScope  = "scope"
	paramOwner  = "owner"
	paramQuery  = "q"
	paramLimit  = "limit"
	paramOffset = "offset"
)

// Refusals the routes answer with.
const (
	errAuthRequired        = "authentication required"
	errSearchScopeRequired = "a user identity (email) is required to scope search results"
)

// Config is what the routes read: the stores (each used for the capabilities
// it implements), the authorization core and the query embedder.
type Config struct {
	Assets      any
	Collections any
	Shares      portaldomain.ShareStore
	Access      *access.Checker
	// Embed turns a query into a vector, nil when no provider is
	// configured, which ranks lexically.
	Embed func(ctx context.Context, query string) []float32
}

// Handler serves the reach routes.
type Handler struct{ cfg Config }

// New builds the reach routes.
func New(cfg Config) *Handler { return &Handler{cfg: cfg} }

// Register mounts the routes the configured stores support.
func (h *Handler) Register(mux *http.ServeMux) {
	if _, ok := h.cfg.Assets.(portaldomain.ReachSearcher); ok {
		mux.HandleFunc("GET /api/v1/portal/assets/search", h.searchAssets)
	}
	mux.HandleFunc("GET /api/v1/portal/assets/facets", h.assetFacets)
	if h.cfg.Collections == nil {
		return
	}
	if _, ok := h.cfg.Collections.(portaldomain.CollectionReachSearcher); ok {
		mux.HandleFunc("GET /api/v1/portal/collections/search", h.searchCollections)
	}
	mux.HandleFunc("GET /api/v1/portal/collections/facets", h.collectionFacets)
}

// Reach is the caller's reach for a listing or search: everything they can
// open, narrowed to the scope the request names (fallback when it names
// none). An administrator's reach is their own; the admin listing is the
// route that spans every owner.
func Reach(r *http.Request, user *access.User, fallback portaldomain.Scope) *portaldomain.Reach {
	scope := portaldomain.ParseScope(r.URL.Query().Get(paramScope), fallback)
	return portaldomain.NewReach(user.UserID, user.Email, portaldomain.CallerPersona(r.Context()), scope)
}

// Owners is the author filter: owner addresses, repeated or comma-separated.
func Owners(r *http.Request) []string {
	return portaldomain.NormalizeOwners(r.URL.Query()[paramOwner])
}

// Rows is what a page of assets reports beside its rows: the share summaries
// of the rows the caller owns, and the caller's permission on each row they
// do not. A share summary says whether a row has a public link, which is its
// owner's to know, so a row shared with the caller carries none.
type Rows struct {
	Summaries   map[string]portaldomain.ShareSummary
	Permissions map[string]portaldomain.SharePermission
}

// DescribeAssets describes a page of assets for the caller.
func (h *Handler) DescribeAssets(ctx context.Context, assets []portaldomain.Asset, reach *portaldomain.Reach, user *access.User) Rows {
	var owned, shared []string
	for i := range assets {
		if access.OwnsAsset(&assets[i], user) {
			owned = append(owned, assets[i].ID)
			continue
		}
		shared = append(shared, assets[i].ID)
	}
	out := Rows{}
	if len(owned) > 0 {
		out.Summaries, _ = h.cfg.Shares.ListActiveShareSummaries(ctx, owned)
	}
	if resolver, ok := h.cfg.Assets.(portaldomain.ReachPermissions); ok && len(shared) > 0 {
		if perms, err := resolver.ReachPermissions(ctx, shared, reach); err == nil {
			out.Permissions = h.capForExternal(perms, user)
		}
	}
	return out
}

// capForExternal holds a person of an external persona to viewer, whatever a
// share grants: the cap access.ResolveAssetPermission applies (#2092).
func (h *Handler) capForExternal(perms map[string]portaldomain.SharePermission, user *access.User) map[string]portaldomain.SharePermission {
	if !h.cfg.Access.Portal(user).External {
		return perms
	}
	for id, p := range perms {
		if p == portaldomain.PermissionEditor {
			perms[id] = portaldomain.PermissionViewer
		}
	}
	return perms
}

// CollectionPermissions is the caller's share permission on each listed
// collection they do not own.
func (h *Handler) CollectionPermissions(ctx context.Context, collections []portaldomain.Collection, user *access.User) map[string]portaldomain.SharePermission {
	var out map[string]portaldomain.SharePermission
	for i := range collections {
		if collections[i].OwnerID == user.UserID {
			continue
		}
		perm := h.cfg.Access.CollectionSharePermission(ctx, collections[i].ID, user)
		if perm == "" {
			continue
		}
		if out == nil {
			out = map[string]portaldomain.SharePermission{}
		}
		out[collections[i].ID] = perm
	}
	return out
}

// OwnedCollectionIDs is the ids of the collections the caller owns, the rows
// whose share summaries are theirs to see.
func OwnedCollectionIDs(collections []portaldomain.Collection, user *access.User) []string {
	ids := make([]string, 0, len(collections))
	for i := range collections {
		if collections[i].OwnerID == user.UserID {
			ids = append(ids, collections[i].ID)
		}
	}
	return ids
}

// page is a ranked page of results.
type page struct {
	Data           any                                     `json:"data"`
	Total          int                                     `json:"total" example:"42"`
	Limit          int                                     `json:"limit" example:"20"`
	Offset         int                                     `json:"offset" example:"0"`
	ShareSummaries map[string]portaldomain.ShareSummary    `json:"share_summaries,omitempty"`
	Permissions    map[string]portaldomain.SharePermission `json:"permissions,omitempty"`
}

// searchCall is what every search route needs from its request.
type searchCall struct {
	user  *access.User
	reach *portaldomain.Reach
	query string
}

// searchRequest reads a search call, writing the refusal and returning ok
// false when the request cannot be served.
func searchRequest(w http.ResponseWriter, r *http.Request) (searchCall, bool) {
	c := searchCall{user: access.GetUser(r.Context())}
	if c.user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return c, false
	}
	// The reach is the sole scoping key: the per-row access rule in SQL. A
	// reach naming nobody would match nothing, and is refused.
	c.reach = Reach(r, c.user, portaldomain.ScopeMine)
	if !c.reach.Identified() {
		httpjson.WriteError(w, http.StatusForbidden, errSearchScopeRequired)
		return c, false
	}
	c.query = strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if c.query == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return c, false
	}
	return c, true
}

// intParam reads a non-negative integer parameter, fallback when absent or
// not one.
func intParam(r *http.Request, name string, fallback int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func (h *Handler) embed(ctx context.Context, query string) []float32 {
	if h.cfg.Embed == nil {
		return nil
	}
	return h.cfg.Embed(ctx, query)
}

// searchAssets handles GET /api/v1/portal/assets/search.
//
// @Summary      Search assets
// @Description  Ranks the assets the current user can open in the given scope by relevance to q: mine (the default), shared, or all. Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Every asset whose name, description or tags match is a result, and so is one the embedding model places near the query; results page with limit and offset, and total counts them all. The scope, content type, tag and owner filters are applied before ranking, so an asset the caller cannot open is never ranked or counted. permissions gives the caller's permission on each result they do not own.
// @Tags         Assets
// @Produce      json
// @Param        q             query  string   true   "Search query"
// @Param        scope         query  string   false  "mine, shared or all (default: mine)"  Enums(mine, shared, all)
// @Param        content_type  query  string   false  "Filter by content type"
// @Param        tag           query  []string false  "Filter by tag; repeat it to require every one"  collectionFormat(multi)
// @Param        owner         query  []string false  "Filter by owner address; repeat it for several"  collectionFormat(multi)
// @Param        limit         query  integer  false  "Results per page (default: 20, max: 100)"
// @Param        offset        query  integer  false  "Offset into the ranked results (default: 0)"
// @Success      200  {object}  page
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/search [get]
func (h *Handler) searchAssets(w http.ResponseWriter, r *http.Request) {
	searcher, ok := h.cfg.Assets.(portaldomain.ReachSearcher)
	if !ok {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "asset search is unavailable")
		return
	}
	c, ok := searchRequest(w, r)
	if !ok {
		return
	}
	user, reach, query := c.user, c.reach, c.query
	limit, offset := portaldomain.RankedPage(intParam(r, paramLimit, portaldomain.DefaultSearchLimit), intParam(r, paramOffset, 0))
	scored, total, err := searcher.SearchReach(r.Context(), portaldomain.RankedAssetQuery{
		Embedding:   h.embed(r.Context(), query),
		QueryText:   query,
		Reach:       reach,
		ContentType: r.URL.Query().Get("content_type"),
		Tags:        r.URL.Query()["tag"],
		Owners:      Owners(r),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to search assets")
		return
	}
	if scored == nil {
		scored = []portaldomain.ScoredAsset{}
	}
	assets := make([]portaldomain.Asset, len(scored))
	for i := range scored {
		assets[i] = scored[i].Asset
	}
	rows := h.DescribeAssets(r.Context(), assets, reach, user)
	httpjson.WriteJSON(w, http.StatusOK, page{
		Data: scored, Total: total, Limit: limit, Offset: offset,
		ShareSummaries: rows.Summaries, Permissions: rows.Permissions,
	})
}

// searchCollections handles GET /api/v1/portal/collections/search.
//
// @Summary      Search collections
// @Description  Ranks the collections the current user can open in the given scope by relevance to q (matching name, description, and section titles/descriptions): mine (the default), shared, or all. Uses hybrid (semantic + lexical) ranking when an embedding provider is configured, falling back to lexical-only otherwise. Results page with limit and offset; total counts them all. A collection the caller cannot open is never ranked or counted.
// @Tags         Collections
// @Produce      json
// @Param        q       query  string   true   "Search query"
// @Param        scope   query  string   false  "mine, shared or all (default: mine)"  Enums(mine, shared, all)
// @Param        owner   query  []string false  "Filter by owner address; repeat it for several"  collectionFormat(multi)
// @Param        limit   query  integer  false  "Results per page (default: 20, max: 100)"
// @Param        offset  query  integer  false  "Offset into the ranked results (default: 0)"
// @Success      200  {object}  page
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Failure      503  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/search [get]
func (h *Handler) searchCollections(w http.ResponseWriter, r *http.Request) {
	searcher, ok := h.cfg.Collections.(portaldomain.CollectionReachSearcher)
	if !ok {
		httpjson.WriteError(w, http.StatusServiceUnavailable, "collection search is unavailable")
		return
	}
	c, ok := searchRequest(w, r)
	if !ok {
		return
	}
	user, reach, query := c.user, c.reach, c.query
	limit, offset := portaldomain.RankedPage(intParam(r, paramLimit, portaldomain.DefaultSearchLimit), intParam(r, paramOffset, 0))
	scored, total, err := searcher.SearchCollectionReach(r.Context(), portaldomain.RankedCollectionQuery{
		Embedding: h.embed(r.Context(), query),
		QueryText: query,
		Reach:     reach,
		Owners:    Owners(r),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to search collections")
		return
	}
	if scored == nil {
		scored = []portaldomain.ScoredCollection{}
	}
	collections := make([]portaldomain.Collection, len(scored))
	for i := range scored {
		collections[i] = scored[i].Collection
	}
	httpjson.WriteJSON(w, http.StatusOK, page{
		Data: scored, Total: total, Limit: limit, Offset: offset,
		Permissions: h.CollectionPermissions(r.Context(), collections, user),
	})
}

// assetFacetsResponse is the response for GET /api/v1/portal/assets/facets.
type assetFacetsResponse struct {
	// Tags are the tags on the assets the scope and the other filters cover,
	// with counts, most used first.
	Tags []portaldomain.FacetCount `json:"tags"`
	// Authors are the owner addresses of those assets, with counts.
	Authors []portaldomain.FacetCount `json:"authors"`
}

// assetFacets handles GET /api/v1/portal/assets/facets.
//
// @Summary      Asset tag and author facets
// @Description  Lists the tags and the authors (owner addresses) on the assets the caller can open in the given scope, each with how many of those assets carry it. The tag facet ignores the tag filter and the author facet the owner filter, so each lists the values a picker can switch to. An asset the caller cannot open contributes no value and no count.
// @Tags         Assets
// @Produce      json
// @Param        scope         query  string   false  "mine, shared or all (default: all)"  Enums(mine, shared, all)
// @Param        content_type  query  string   false  "Filter by content type"
// @Param        tag           query  []string false  "Filter by tag; repeat it to require every one"  collectionFormat(multi)
// @Param        owner         query  []string false  "Filter by owner address; repeat it for several"  collectionFormat(multi)
// @Success      200  {object}  assetFacetsResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/facets [get]
func (h *Handler) assetFacets(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	resp := assetFacetsResponse{Tags: []portaldomain.FacetCount{}, Authors: []portaldomain.FacetCount{}}
	faceter, ok := h.cfg.Assets.(portaldomain.AssetFaceter)
	if !ok {
		httpjson.WriteJSON(w, http.StatusOK, resp)
		return
	}
	facets, err := faceter.AssetFacets(r.Context(), portaldomain.AssetFilter{
		Reach:       Reach(r, user, portaldomain.ScopeAll),
		Owners:      Owners(r),
		ContentType: r.URL.Query().Get("content_type"),
		Tags:        r.URL.Query()["tag"],
	})
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to compute asset facets")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, assetFacetsResponse(facets))
}

// collectionFacetsResponse is the response for GET
// /api/v1/portal/collections/facets.
type collectionFacetsResponse struct {
	// Authors are the owner addresses of the collections the scope covers,
	// with counts.
	Authors []portaldomain.FacetCount `json:"authors"`
}

// collectionFacets handles GET /api/v1/portal/collections/facets.
//
// @Summary      Collection author facet
// @Description  Lists the authors (owner addresses) of the collections the caller can open in the given scope, each with how many of those collections they own. A collection the caller cannot open contributes nothing.
// @Tags         Collections
// @Produce      json
// @Param        scope  query  string  false  "mine, shared or all (default: all)"  Enums(mine, shared, all)
// @Success      200  {object}  collectionFacetsResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/facets [get]
func (h *Handler) collectionFacets(w http.ResponseWriter, r *http.Request) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}
	resp := collectionFacetsResponse{Authors: []portaldomain.FacetCount{}}
	faceter, ok := h.cfg.Collections.(portaldomain.CollectionAuthorFaceter)
	if !ok {
		httpjson.WriteJSON(w, http.StatusOK, resp)
		return
	}
	authors, err := faceter.CollectionAuthors(r.Context(), Reach(r, user, portaldomain.ScopeAll))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to compute collection facets")
		return
	}
	resp.Authors = authors
	httpjson.WriteJSON(w, http.StatusOK, resp)
}
