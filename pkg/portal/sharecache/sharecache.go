// Package sharecache decides how a response served behind a portal share
// token may be cached.
//
// It is the caching counterpart of shareaccess: that package decides who may
// open a token, this one keeps the answer from being given again out of a
// cache. For every mode but public the verdict is a property of the caller's
// session or guest cookie, not of the URL, so a shared cache keyed on the URL
// alone would populate on the first authorized fetch and answer every later
// holder of the token without the gate ever running — the bearer-URL access
// the mode domain exists to end (#999, #1070).
//
// The decision lives next to the access decision rather than in the handlers
// for the same reason the access decision does: a per-handler directive leaves
// a route publicly cacheable by omission.
package sharecache

import (
	"fmt"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// Window is how long a thumbnail behind a share may be reused. It is what
// makes a collection page of thumbnails cheap to revisit, and it is the
// ceiling on how long a revoked public share's thumbnail can still be served
// from a shared cache that already stored it.
const Window = time.Hour

// PubliclyCacheable reports whether a shared cache may store responses served
// behind a share in mode. Only a fully public share qualifies: its verdict is
// the same for every caller, so the URL alone is a complete cache key.
//
// The empty mode — what rows written before the mode column carry — is not
// public, matching shareaccess.Authorize, which resolves it to its shape's
// default rather than to public. An unset value can never widen caching any
// more than it can widen access.
func PubliclyCacheable(mode shareaccess.Mode) bool {
	return mode == shareaccess.ModePublic
}

// Apply writes the caching floor for a response the share gate is about to
// produce, admitted or refused.
//
// Vary is unconditional: even on a public share the viewer page differs for a
// signed-in caller, a guest and an anonymous one. Cache-Control is written only
// for identity-gated shares, and only as a floor the handler behind the gate
// may narrow or — on a public share, for the thumbnail a link preview fetches —
// widen. A public share's other responses are left to blobserve's private
// default rather than opened up: the bytes are anonymous, but nothing needs a
// CDN holding a copy of every shared file.
//
// A caller authenticating with a header rather than a cookie is covered by the
// directive rather than by Vary: a shared cache must not store a `private`
// response whatever it varies on.
func Apply(w http.ResponseWriter, mode shareaccess.Mode) {
	w.Header().Set("Vary", "Cookie")
	if !PubliclyCacheable(mode) {
		w.Header().Set("Cache-Control", "private")
	}
}

// Refuse marks a gate refusal as never storable. A 410 is cacheable by
// default, and a stored refusal keyed on the URL would answer for the
// recipient the share was made for, turning a per-caller verdict into a
// per-URL one in the direction that denies access rather than grants it.
func Refuse(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// Thumbnail writes the directive for a thumbnail served behind a share that
// expires at expiresAt (nil for a share that does not expire). Only a fully
// public share's thumbnail is offered to shared caches; any other mode gets
// the browser that was authorized and nothing else.
func Thumbnail(w http.ResponseWriter, mode shareaccess.Mode, expiresAt *time.Time, now time.Time) {
	age := MaxAge(expiresAt, now)
	if PubliclyCacheable(mode) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(age.Seconds())))
		return
	}
	blobserve.CachePrivate(w, age)
}

// MaxAge returns how long a thumbnail may be reused: Window, or whatever is
// left of the share's life when that is shorter, so no cached copy outlives
// the token that authorized it. The gate refuses an expired share before a
// thumbnail is served, so the clamp only ever narrows a live window.
//
// Revocation has no equivalent clamp — it is not a time the response can be
// written against — so a copy already stored is served until it goes stale.
// That residue is why Window is an hour and not a day.
func MaxAge(expiresAt *time.Time, now time.Time) time.Duration {
	if expiresAt == nil {
		return Window
	}
	return max(0, min(Window, expiresAt.Sub(now)))
}
