package portal

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// gateCtxKey carries what publicShareGate resolved to the handler behind it.
type gateCtxKey struct{}

// gateResult is the gate's verdict for an admitted request: the share the
// token names, and the authenticated viewer (nil when anonymous, which only a
// public share admits).
type gateResult struct {
	Share  *Share
	Viewer *User
}

// shareFromRequest returns the share the public gate resolved for this
// request. A handler reached without the gate gets an empty share, which
// matches no asset and no collection, so a missing gate reads as 404 rather
// than as unrestricted access.
func shareFromRequest(r *http.Request) *Share {
	if g, ok := r.Context().Value(gateCtxKey{}).(gateResult); ok {
		return g.Share
	}
	return &Share{}
}

// publicShareGate resolves the {token} path value, rejects revoked, expired,
// and unauthorized requests, and passes the share to next through the request
// context. Every route on publicMux is wrapped by it: the public surface is
// seven routes and several read S3 without going through the page handler, so
// enforcing per-handler would leave a route open by omission (#999).
func (h *Handler) publicShareGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue(pathKeyToken)
		if token == "" {
			http.NotFound(w, r)
			return
		}
		share, err := h.deps.ShareStore.GetByToken(r.Context(), token)
		if err != nil || share == nil {
			http.NotFound(w, r)
			return
		}
		if msg := shareaccess.Availability(share.Revoked, share.ExpiresAt, time.Now()); msg != "" {
			http.Error(w, msg, http.StatusGone)
			return
		}

		viewer := h.resolvePublicViewer(r)
		target := shareaccess.Share{
			Mode:            share.AccessMode,
			RecipientUserID: share.SharedWithUserID,
			RecipientEmail:  share.SharedWithEmail,
			CreatorEmail:    share.CreatedBy,
		}
		var caller *shareaccess.Viewer
		if viewer != nil {
			caller = &shareaccess.Viewer{UserID: viewer.UserID, Email: viewer.Email}
		}
		if msg, ok := shareaccess.Authorize(target, caller); !ok {
			http.Error(w, msg, http.StatusForbidden)
			return
		}

		ctx := context.WithValue(r.Context(), gateCtxKey{}, gateResult{Share: share, Viewer: viewer})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
