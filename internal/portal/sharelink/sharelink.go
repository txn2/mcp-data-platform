// Package sharelink serves the two changes the share dialog makes to what
// already exists (#2291): an item's one general link, created, changed in
// place or removed, and a grant's permission changed without revoking it.
package sharelink

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// The routes.
const (
	assetLinkPath      = "/api/v1/portal/assets/{id}/link"
	collectionLinkPath = "/api/v1/portal/collections/{id}/link"
	sharePath          = "/api/v1/portal/shares/{id}"
)

const (
	maxBody      = 16 << 10
	errAuth      = "authentication required"
	errNotFound  = "not found"
	errForbidden = "only the owner can change how this is shared"
	errBody      = "invalid request body"
	errStore     = "failed to save the share"
)

// Config is what the routes are built from.
type Config struct {
	Shares      portaldomain.ShareStore
	Assets      portaldomain.AssetStore
	Collections portaldomain.CollectionStore
	Prompts     prompt.Store
	// Access is the portal's authorization core.
	Access *access.Checker
	// Sharing reports whether the caller's persona may share at all.
	Sharing func(user *access.User) bool
	// HasAccount reports whether the directory holds an address; nil treats
	// every address as one.
	HasAccount func(ctx context.Context, email string) bool
	// PublicBaseURL is the origin a share URL is written with; empty writes
	// none.
	PublicBaseURL string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Handler serves the routes.
type Handler struct{ cfg Config }

// New returns a Handler over cfg.
func New(cfg Config) *Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Handler{cfg: cfg}
}

// Register mounts the routes on mux. A collection's link is served only with a
// collection store.
func (h *Handler) Register(mux *http.ServeMux) {
	if h.cfg.Shares == nil {
		return
	}
	mux.HandleFunc("PUT "+assetLinkPath, h.putAssetLink)
	mux.HandleFunc("DELETE "+assetLinkPath, h.deleteAssetLink)
	if h.cfg.Collections != nil {
		mux.HandleFunc("PUT "+collectionLinkPath, h.putCollectionLink)
		mux.HandleFunc("DELETE "+collectionLinkPath, h.deleteCollectionLink)
	}
	mux.HandleFunc("PATCH "+sharePath, h.patchShare)
}

// LinkRequest is the general link an item is asked to have. An omitted field
// keeps what an existing link holds.
type LinkRequest struct {
	// AccessMode is "authenticated" (anyone who can sign in) or "public"
	// (anyone with the link).
	AccessMode string `json:"access_mode" example:"public"`
	// ExpiresIn is a Go duration, for a public link only: required to make a
	// link public, optional to keep one public.
	ExpiresIn string `json:"expires_in,omitempty" example:"24h"`
	// NoticeText is the banner on the shared page; "" hides it.
	NoticeText *string `json:"notice_text,omitempty" example:"Proprietary & Confidential"`
	// HideExpiration hides the date the link stops working from the viewer.
	HideExpiration *bool `json:"hide_expiration,omitempty" example:"false"`
}

// LinkResponse is the item's general link after the change.
type LinkResponse struct {
	Share    portaldomain.Share `json:"share"`
	ShareURL string             `json:"share_url,omitempty" example:"https://platform.example.com/portal/view/abc123"`
}

// PermissionRequest is a grant's new permission.
type PermissionRequest struct {
	// Permission is "viewer" or "editor".
	Permission string `json:"permission" example:"editor"`
}

// item is the thing a general link is for, once the caller is allowed to
// change how it is shared.
type item struct {
	target portaldomain.ShareTarget
	list   func(ctx context.Context) ([]portaldomain.Share, error)
}

// putAssetLink handles PUT /api/v1/portal/assets/{id}/link.
//
// @Summary      Set an asset's general link
// @Description  Creates the asset's one general link, or changes the one it has in place: its mode, expiry, banner and whether the expiry is shown (#2291). The token is kept, so a link already handed out follows the new rule. A link a post to a channel created stays its own. The owner or an admin.
// @Tags         Shares
// @Accept       json
// @Produce      json
// @Param        id    path  string       true  "Asset ID"
// @Param        body  body  LinkRequest  true  "The link"
// @Success      200  {object}  LinkResponse  "The link was changed"
// @Success      201  {object}  LinkResponse  "The link was created"
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/link [put]
func (h *Handler) putAssetLink(w http.ResponseWriter, r *http.Request) {
	if it, ok := h.asset(w, r, true); ok {
		h.putLink(w, r, it)
	}
}

// deleteAssetLink handles DELETE /api/v1/portal/assets/{id}/link.
//
// @Summary      Remove an asset's general link
// @Description  Revokes the asset's general link, leaving only the people and groups it is shared with and its posts to channels (#2291). An asset with no general link is unchanged. The owner or an admin.
// @Tags         Shares
// @Param        id  path  string  true  "Asset ID"
// @Success      204
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/assets/{id}/link [delete]
func (h *Handler) deleteAssetLink(w http.ResponseWriter, r *http.Request) {
	if it, ok := h.asset(w, r, false); ok {
		h.deleteLink(w, r, it)
	}
}

// putCollectionLink handles PUT /api/v1/portal/collections/{id}/link.
//
// @Summary      Set a collection's general link
// @Description  Creates the collection's one general link, or changes the one it has in place, keeping its token (#2291). The owner or an admin.
// @Tags         Shares
// @Accept       json
// @Produce      json
// @Param        id    path  string       true  "Collection ID"
// @Param        body  body  LinkRequest  true  "The link"
// @Success      200  {object}  LinkResponse  "The link was changed"
// @Success      201  {object}  LinkResponse  "The link was created"
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/link [put]
func (h *Handler) putCollectionLink(w http.ResponseWriter, r *http.Request) {
	if it, ok := h.collection(w, r, true); ok {
		h.putLink(w, r, it)
	}
}

// deleteCollectionLink handles DELETE /api/v1/portal/collections/{id}/link.
//
// @Summary      Remove a collection's general link
// @Description  Revokes the collection's general link (#2291). A collection with no general link is unchanged. The owner or an admin.
// @Tags         Shares
// @Param        id  path  string  true  "Collection ID"
// @Success      204
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/collections/{id}/link [delete]
func (h *Handler) deleteCollectionLink(w http.ResponseWriter, r *http.Request) {
	if it, ok := h.collection(w, r, false); ok {
		h.deleteLink(w, r, it)
	}
}

// asset resolves the asset in the path for a caller who may change how it is
// shared. creating also requires that the caller's persona may share.
func (h *Handler) asset(w http.ResponseWriter, r *http.Request, creating bool) (item, bool) {
	user, ok := h.user(w, r)
	if !ok {
		return item{}, false
	}
	a, err := h.cfg.Assets.Get(r.Context(), r.PathValue("id"))
	if err != nil || a == nil {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return item{}, false
	}
	manages := h.cfg.Access.CanManageAsset(a, user)
	if !manages || !h.mayShare(user, creating) {
		writeForbidden(w, manages)
		return item{}, false
	}
	return item{
		target: portaldomain.ShareTarget{AssetID: a.ID},
		list:   func(ctx context.Context) ([]portaldomain.Share, error) { return h.cfg.Shares.ListByAsset(ctx, a.ID) },
	}, true
}

// collection resolves the collection in the path, as asset does.
func (h *Handler) collection(w http.ResponseWriter, r *http.Request, creating bool) (item, bool) {
	user, ok := h.user(w, r)
	if !ok {
		return item{}, false
	}
	c, err := h.cfg.Collections.Get(r.Context(), r.PathValue("id"))
	if err != nil || c == nil {
		httpjson.WriteError(w, http.StatusNotFound, errNotFound)
		return item{}, false
	}
	manages := h.cfg.Access.CanManage(c.OwnerID, user)
	if !manages || !h.mayShare(user, creating) {
		writeForbidden(w, manages)
		return item{}, false
	}
	return item{
		target: portaldomain.ShareTarget{CollectionID: c.ID},
		list: func(ctx context.Context) ([]portaldomain.Share, error) {
			return h.cfg.Shares.ListByCollection(ctx, c.ID)
		},
	}, true
}

// user is the signed-in caller, or a 401.
func (*Handler) user(w http.ResponseWriter, r *http.Request) (*access.User, bool) {
	user := access.GetUser(r.Context())
	if user == nil {
		httpjson.WriteError(w, http.StatusUnauthorized, errAuth)
		return nil, false
	}
	return user, true
}

// mayShare reports whether the caller's persona may share, which creating or
// widening access requires and removing it does not.
func (h *Handler) mayShare(user *access.User, creating bool) bool {
	return !creating || h.cfg.Sharing == nil || h.cfg.Sharing(user)
}

// writeForbidden answers a caller who does not manage the item, or whose
// persona may not share.
func writeForbidden(w http.ResponseWriter, manages bool) {
	if !manages {
		httpjson.WriteError(w, http.StatusForbidden, errForbidden)
		return
	}
	httpjson.WriteError(w, http.StatusForbidden, access.ErrSharingNotAllowed)
}

// putLink creates the item's general link or changes the one it has.
func (h *Handler) putLink(w http.ResponseWriter, r *http.Request, it item) {
	var req LinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBody)
		return
	}
	shares, err := it.list(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the shares")
		return
	}
	now := h.cfg.Now()
	cur := portaldomain.GeneralLink(shares, now)
	if cur == nil {
		h.createLink(w, r, it, req)
		return
	}
	u, err := portaldomain.ApplyLinkSpec(*cur, linkSpec(req), now)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.cfg.Shares.UpdateLink(r.Context(), cur.ID, u); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	updated := *cur
	updated.AccessMode, updated.ExpiresAt, updated.NoticeText, updated.HideExpiration = u.AccessMode, u.ExpiresAt, u.NoticeText, u.HideExpiration
	httpjson.WriteJSON(w, http.StatusOK, h.response(updated))
}

// createLink mints the item's first general link.
func (h *Handler) createLink(w http.ResponseWriter, r *http.Request, it item, req LinkRequest) {
	if req.AccessMode == string(portaldomain.AccessModeRestricted) {
		httpjson.WriteError(w, http.StatusBadRequest, portaldomain.ErrLinkMode.Error())
		return
	}
	spec := portaldomain.ShareSpec{AccessMode: req.AccessMode, ExpiresIn: req.ExpiresIn, NoticeText: req.NoticeText}
	if req.HideExpiration != nil {
		spec.HideExpiration = *req.HideExpiration
	}
	share, err := portaldomain.BuildShare(it.target, access.GetUser(r.Context()).Email, spec)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.cfg.Shares.Insert(r.Context(), &share); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	httpjson.WriteJSON(w, http.StatusCreated, h.response(share))
}

// linkSpec is the request as the domain reads it.
func linkSpec(req LinkRequest) portaldomain.LinkSpec {
	return portaldomain.LinkSpec{
		AccessMode: req.AccessMode, ExpiresIn: req.ExpiresIn, NoticeText: req.NoticeText, HideExpiration: req.HideExpiration,
	}
}

// response is a link share with the URL it opens at.
func (h *Handler) response(s portaldomain.Share) LinkResponse {
	resp := LinkResponse{Share: s}
	if h.cfg.PublicBaseURL != "" {
		resp.ShareURL = fmt.Sprintf("%s/portal/view/%s", h.cfg.PublicBaseURL, s.Token)
	}
	return resp
}

// deleteLink revokes the item's general link, when it has one.
func (h *Handler) deleteLink(w http.ResponseWriter, r *http.Request, it item) {
	shares, err := it.list(r.Context())
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read the shares")
		return
	}
	if cur := portaldomain.GeneralLink(shares, h.cfg.Now()); cur != nil {
		if err := h.cfg.Shares.Revoke(r.Context(), cur.ID); err != nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "failed to remove the link")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// patchShare handles PATCH /api/v1/portal/shares/{id}.
//
// @Summary      Change a share's permission
// @Description  Changes the permission of a share addressed to a person or a group, in place (#2291). A link opens as viewer and has no permission to change, and an address with no account can only view. The owner of the shared item, or an admin.
// @Tags         Shares
// @Accept       json
// @Produce      json
// @Param        id    path  string             true  "Share ID"
// @Param        body  body  PermissionRequest  true  "The permission"
// @Success      200  {object}  portaldomain.Share
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      401  {object}  httpjson.ProblemDetail
// @Failure      403  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/shares/{id} [patch]
func (h *Handler) patchShare(w http.ResponseWriter, r *http.Request) {
	user, ok := h.user(w, r)
	if !ok {
		return
	}
	var req PermissionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, errBody)
		return
	}
	share, err := h.cfg.Shares.GetByID(r.Context(), r.PathValue("id"))
	if err != nil || share == nil || share.Revoked {
		httpjson.WriteError(w, http.StatusNotFound, "share not found")
		return
	}
	if status, msg := h.manages(r.Context(), share, user); status != 0 {
		httpjson.WriteError(w, status, msg)
		return
	}
	perm, err := portaldomain.GrantPermission(r.Context(), *share, req.Permission, h.cfg.HasAccount)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.cfg.Shares.SetPermission(r.Context(), share.ID, perm); err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, errStore)
		return
	}
	share.Permission = perm
	httpjson.WriteJSON(w, http.StatusOK, share)
}

// manages is 0 when the caller may change the shares of the item a share is
// for, else the status and message refusing them.
func (h *Handler) manages(ctx context.Context, share *portaldomain.Share, user *access.User) (status int, msg string) {
	found, may := h.owns(ctx, share, user)
	switch {
	case !found:
		return http.StatusNotFound, errNotFound
	case !may:
		return http.StatusForbidden, errForbidden
	}
	return 0, ""
}

// owns reports whether the item a share is for exists, and whether the caller
// holds owner authority over it.
func (h *Handler) owns(ctx context.Context, share *portaldomain.Share, user *access.User) (found, may bool) {
	switch {
	case share.PromptID != "":
		return h.ownsPrompt(ctx, share.PromptID, user)
	case share.CollectionID != "":
		return h.ownsCollection(ctx, share.CollectionID, user)
	default:
		a, err := h.cfg.Assets.Get(ctx, share.AssetID)
		if err != nil || a == nil {
			return false, false
		}
		return true, h.cfg.Access.CanManageAsset(a, user)
	}
}

func (h *Handler) ownsPrompt(ctx context.Context, id string, user *access.User) (found, may bool) {
	if h.cfg.Prompts == nil {
		return false, false
	}
	p, err := h.cfg.Prompts.GetByID(ctx, id)
	if err != nil || p == nil {
		return false, false
	}
	return true, h.cfg.Access.CanManageEmail(p.OwnerEmail, user)
}

func (h *Handler) ownsCollection(ctx context.Context, id string, user *access.User) (found, may bool) {
	if h.cfg.Collections == nil {
		return false, false
	}
	c, err := h.cfg.Collections.Get(ctx, id)
	if err != nil || c == nil {
		return false, false
	}
	return true, h.cfg.Access.CanManage(c.OwnerID, user)
}
