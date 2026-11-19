package assetlinks

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// linksResponse is what an asset links to and what links to it.
type linksResponse struct {
	// Links are the ids the asset's current content links to. The portal
	// follows a link from the asset's frame only to one of these.
	Links []string `json:"links"`
	// LinkedFrom are the assets whose current content links to this one,
	// limited to those the caller can open.
	LinkedFrom []Ref `json:"linked_from"`
}

// API serves an asset's links.
type API struct {
	store *Store
	gate  assetstatus.Gate
}

// Mount returns the hook pkg/portal registers the links route through.
func Mount(store *Store, assets portaldomain.AssetStore) func(*http.ServeMux, *access.Checker) {
	return func(mux *http.ServeMux, checker *access.Checker) {
		api := &API{store: store, gate: assetstatus.Gate{Assets: assets, Access: checker}}
		mux.HandleFunc("GET /api/v1/portal/assets/{id}/links", api.links)
	}
}

// links lists what an asset links to and the assets linking to it.
//
// @Summary      List an asset's links
// @Description  The asset ids this asset's current content links to (mcp:asset:<id> link targets), and the assets whose current content links to this one, limited to the ones the caller can open: an asset the caller cannot open is neither listed nor counted (#2432). A link grants nothing.
// @Tags         Portal
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  linksResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/links [get]
func (a *API) links(w http.ResponseWriter, r *http.Request) {
	asset, user := a.gate.Resolve(w, r, false, "")
	if asset == nil {
		return
	}
	links, err := a.store.LinksFrom(r.Context(), asset.ID)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the links could not be read")
		return
	}
	reach := portaldomain.NewReach(user.UserID, user.Email, portaldomain.CallerPersona(r.Context()), portaldomain.ScopeAll)
	from, err := a.store.LinkedFrom(r.Context(), asset.ID, reach)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "the links could not be read")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, linksResponse{Links: links, LinkedFrom: from})
}
