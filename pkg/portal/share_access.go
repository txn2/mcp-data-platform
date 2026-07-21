package portal

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
)

// gateCtxKey carries what publicShareGate resolved to the handler behind it.
type gateCtxKey struct{}

// gateResult is the gate's verdict for an admitted request: the share the
// token names, the authenticated viewer (nil when anonymous, which only a
// public share admits), and whether the caller was admitted as a guest
// through a one-time email link (#1001). A guest is never a viewer: the two
// fields are mutually exclusive.
type gateResult struct {
	Share  *Share
	Viewer *User
	Guest  bool
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

// isGuestRequest reports whether the public gate admitted this request as a
// guest, so the viewer can suppress portal affordances and render the guest
// indicator.
func isGuestRequest(r *http.Request) bool {
	g, ok := r.Context().Value(gateCtxKey{}).(gateResult)
	return ok && g.Guest
}

// publicShareGate resolves the {token} path value, rejects revoked, expired,
// and unauthorized requests, and passes the share to next through the request
// context. Every route on publicMux is wrapped by it: the public surface is
// seven routes and several read S3 without going through the page handler, so
// enforcing per-handler would leave a route open by omission (#999).
//
// A refusal is rendered by denyShare: a branded page for browser navigations,
// plain text otherwise. Before refusing an anonymous caller the gate consults
// the guest service for a valid one-time-link session scoped to this share
// (#1001); share availability is checked first, so revoking a share refuses
// its guests immediately, without waiting for their cookies to expire.
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
			h.denyShare(w, r, share, shareguest.Denial{Status: http.StatusGone, Message: msg})
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
			if viewer == nil && h.deps.ShareGuest.Admit(r, share.ID) != nil {
				ctx := context.WithValue(r.Context(), gateCtxKey{}, gateResult{Share: share, Guest: true})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			h.denyShare(w, r, share, shareguest.Denial{
				Status:        http.StatusForbidden,
				Message:       msg,
				SignedInEmail: viewerEmail(viewer),
			})
			return
		}

		ctx := context.WithValue(r.Context(), gateCtxKey{}, gateResult{Share: share, Viewer: viewer})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// viewerEmail returns the signed-in caller's email, or "" for anonymous.
func viewerEmail(viewer *User) string {
	if viewer == nil {
		return ""
	}
	return viewer.Email
}

// denyShare writes the gate's refusal, filling the denial's share fields.
// With a guest service wired, browser navigations get a branded landing page
// offering a way in (sign-in, and a one-time link for email shares); without
// one, the refusal stays the plain text it was before #1001.
func (h *Handler) denyShare(w http.ResponseWriter, r *http.Request, share *Share, d shareguest.Denial) {
	if h.deps.ShareGuest == nil {
		http.Error(w, d.Message, d.Status)
		return
	}
	d.Token = share.Token
	d.RecipientEmail = share.SharedWithEmail
	h.deps.ShareGuest.Deny(w, r, d)
}
