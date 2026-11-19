package assetverify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// Refusals the routes answer with.
const (
	errNoPermission = "verifying an asset needs the Can verify assets permission"
	errNoRemove     = "only the person who verified it, a holder of the Can verify assets permission or an administrator can remove the verification"
	errBadBody      = "invalid request body"
	errStore        = "the verification could not be saved"
)

// maxBody bounds a verification request body.
const maxBody = 8 << 10

// Permitted reports whether the caller may verify assets: an administrator,
// or a person whose persona carries verify_assets. It is the one rule both
// the portal routes and the asset toolkit ask.
func Permitted(checker *access.Checker, user *access.User) bool {
	if checker == nil || user == nil {
		return false
	}
	if checker.IsAdmin(user) {
		return true
	}
	v := checker.Portal(user).VerifyAssets
	return v != nil && *v
}

// API serves an asset's verification routes.
type API struct {
	store Store
	gate  assetstatus.Gate
}

// Mount returns the hook pkg/portal registers the verification routes through.
func Mount(store Store, assets portaldomain.AssetStore) func(*http.ServeMux, *access.Checker) {
	return func(mux *http.ServeMux, checker *access.Checker) {
		api := &API{store: store, gate: assetstatus.Gate{Assets: assets, Access: checker}}
		mux.HandleFunc("PUT /api/v1/portal/assets/{id}/verification", api.verify)
		mux.HandleFunc("DELETE /api/v1/portal/assets/{id}/verification", api.unverify)
	}
}

// verifyRequest is the body of a verification.
type verifyRequest struct {
	// Note is shown with the badge.
	Note string `json:"note,omitempty" example:"Numbers checked against the ledger"`
	// ExpiresAt is when the badge is removed (RFC 3339); absent never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// verify marks an asset Verified.
//
// @Summary      Verify an asset
// @Description  Marks an asset Verified at its current version, with an optional note and expiry (#2503). Needs the Can verify assets permission (verify_assets) or an administrator, and the caller must be able to open the asset. Verifying again replaces the verification.
// @Tags         Portal
// @Accept       json
// @Produce      json
// @Param        id    path  string         true  "Asset ID"
// @Param        body  body  verifyRequest  true  "Verification"
// @Success      200  {object}  portaldomain.Asset
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/verification [put]
func (a *API) verify(w http.ResponseWriter, r *http.Request) {
	asset, user := a.gate.Resolve(w, r, false, "")
	if asset == nil {
		return
	}
	if !Permitted(a.gate.Access, user) {
		httpjson.WriteError(w, http.StatusForbidden, errNoPermission)
		return
	}
	var req verifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBadBody)
		return
	}
	err := a.store.Verify(r.Context(), asset.ID, Verification{
		Actor: assetstatus.Actor(user), Note: req.Note, Version: asset.CurrentVersion, ExpiresAt: req.ExpiresAt,
	})
	if errors.Is(err, ErrInvalid) {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	a.writeAsset(r.Context(), w, asset.ID)
}

// unverify removes an asset's verification.
//
// @Summary      Remove an asset's verification
// @Description  Removes the Verified badge (#2503). Open to the person who verified it, any holder of the Can verify assets permission and administrators.
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
// @Router       /portal/assets/{id}/verification [delete]
func (a *API) unverify(w http.ResponseWriter, r *http.Request) {
	asset, user := a.gate.Resolve(w, r, false, "")
	if asset == nil {
		return
	}
	if !MayRemove(r.Context(), a.store, a.gate.Access, user, asset.ID) {
		httpjson.WriteError(w, http.StatusForbidden, errNoRemove)
		return
	}
	if _, err := a.store.Unverify(r.Context(), asset.ID, assetstatus.Actor(user)); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	a.writeAsset(r.Context(), w, asset.ID)
}

// MayRemove reports whether the caller may remove the asset's verification:
// a holder of the permission, an administrator, or the person who verified it.
func MayRemove(ctx context.Context, store Store, checker *access.Checker, user *access.User, assetID string) bool {
	if Permitted(checker, user) {
		return true
	}
	by, err := store.VerifiedBy(ctx, assetID)
	if err != nil || by == "" {
		return false
	}
	return strings.EqualFold(by, user.Email) || by == user.UserID
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
