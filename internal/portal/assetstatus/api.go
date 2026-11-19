package assetstatus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// Refusals the routes answer with.
const (
	errAuthRequired = "authentication required"
	errNotFound     = "asset not found"
	errNoEdit       = "only the owner or an editor can change this asset's status"
	errBadBody      = "invalid request body"
	errStore        = "the status could not be saved"
	errHistory      = "the history could not be read"
)

// maxBody bounds a status request body.
const maxBody = 8 << 10

// Gate answers, for one request, whether the caller may read or change one
// asset, with the rule every portal route uses (internal/portal/access).
type Gate struct {
	Assets portaldomain.AssetStore
	Access *access.Checker
}

// Resolve reads the asset and checks the caller may view it, or edit it when
// edit is set. It writes the refusal itself and returns nil: 401 with no
// caller, 404 for an asset that does not exist or the caller cannot open (one
// answer, so an id the caller cannot open learns nothing), 403 when the caller
// can open it but not edit it.
func (g Gate) Resolve(w http.ResponseWriter, r *http.Request, edit bool, noEdit string) (*portaldomain.Asset, *access.User) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuthRequired)
		return nil, nil
	}
	id := r.PathValue("id")
	asset, err := g.Assets.Get(r.Context(), id)
	if err != nil || asset == nil || asset.DeletedAt != nil || !g.Access.CanViewAsset(r.Context(), id, asset, user) {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return nil, nil
	}
	if edit && !g.Access.CanEditAssetSilent(r.Context(), id, user) {
		httpjson.WriteError(w, http.StatusForbidden, noEdit)
		return nil, nil
	}
	return asset, user
}

// Actor is the identity a change is recorded under: the caller's address, or
// their id when they have none.
func Actor(user *access.User) string {
	if user.Email != "" {
		return user.Email
	}
	return user.UserID
}

// API serves an asset's status routes.
type API struct {
	store Store
	gate  Gate
}

// Mount returns the hook pkg/portal registers the status routes through.
func Mount(store Store, assets portaldomain.AssetStore) func(*http.ServeMux, *access.Checker) {
	return func(mux *http.ServeMux, checker *access.Checker) {
		api := &API{store: store, gate: Gate{Assets: assets, Access: checker}}
		mux.HandleFunc("PUT /api/v1/portal/assets/{id}/status", api.setStatus)
		mux.HandleFunc("DELETE /api/v1/portal/assets/{id}/status", api.clearStatus)
		mux.HandleFunc("GET /api/v1/portal/assets/{id}/status/history", api.history)
	}
}

// statusRequest is the body of a status change.
type statusRequest struct {
	// Status is draft, published or deprecated.
	Status string `json:"status" example:"deprecated"`
	// Note is shown with the status; a deprecated asset shows it to readers.
	Note string `json:"note,omitempty" example:"Replaced by the Q4 report"`
}

// historyResponse is an asset's recorded status and verification changes.
type historyResponse struct {
	Events []Event `json:"events"`
}

// setStatus sets an asset's status.
//
// @Summary      Set an asset's status
// @Description  Sets an asset's status to draft, published or deprecated, with an optional note a deprecated asset shows its readers (#2502). The status is informational and changes no access. The owner and editors may set it.
// @Tags         Portal
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "Asset ID"
// @Param        body  body  statusRequest  true  "Status"
// @Success      200  {object}  portaldomain.Asset
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/status [put]
func (a *API) setStatus(w http.ResponseWriter, r *http.Request) {
	asset, user := a.gate.Resolve(w, r, true, errNoEdit)
	if asset == nil {
		return
	}
	var req statusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	err := a.store.Set(r.Context(), asset.ID, Change{Status: req.Status, Note: req.Note, Actor: Actor(user)})
	if !answer(w, err) {
		return
	}
	a.writeAsset(r.Context(), w, asset.ID)
}

// clearStatus removes an asset's status.
//
// @Summary      Clear an asset's status
// @Description  Removes an asset's status, so it has none (#2502). The owner and editors may clear it.
// @Tags         Portal
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  portaldomain.Asset
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/status [delete]
func (a *API) clearStatus(w http.ResponseWriter, r *http.Request) {
	asset, user := a.gate.Resolve(w, r, true, errNoEdit)
	if asset == nil {
		return
	}
	_, err := a.store.Clear(r.Context(), asset.ID, Actor(user))
	if !answer(w, err) {
		return
	}
	a.writeAsset(r.Context(), w, asset.ID)
}

// history lists an asset's status and verification changes.
//
// @Summary      List an asset's status and verification changes
// @Description  Every change to an asset's status or Verified badge, newest first: who, when, from and to (#2502, #2503). Anyone who can open the asset may read it.
// @Tags         Portal
// @Produce      json
// @Param        id  path  string  true  "Asset ID"
// @Success      200  {object}  historyResponse
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/status/history [get]
func (a *API) history(w http.ResponseWriter, r *http.Request) {
	asset, _ := a.gate.Resolve(w, r, false, "")
	if asset == nil {
		return
	}
	events, err := a.store.History(r.Context(), asset.ID, maxHistory)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errHistory)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, historyResponse{Events: events})
}

// answer writes the refusal for a store error and reports whether the write
// succeeded.
func answer(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrInvalid):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
	}
	return false
}

// writeAsset answers with the asset as it now reads.
func (a *API) writeAsset(ctx context.Context, w http.ResponseWriter, id string) {
	asset, err := a.gate.Assets.Get(ctx, id)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, asset)
}
