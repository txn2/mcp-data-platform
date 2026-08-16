// Package sessionapi serves the /api/v1/portal/sessions surface: the sessions
// the calling user ran, and one of them opened in full.
//
// It is the caller-scoped face of the same read model the operator surface in
// internal/admin/sessionapi reads (internal/platform/sessionview), not a second
// derivation of it. The only difference between the two is the scope: every
// read here carries the caller's user id, so a session id belonging to someone
// else is answered as not-found rather than as a refusal, and the answer is the
// same one an id that was never used gets.
//
// A user reading their own sessions is reading their own audit history, which
// the portal already exposes in aggregate on the activity dashboard. What this
// adds is the individual session: the calls it made, the reason stated for
// each, and what it left behind.
package sessionapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
)

// Store reads sessions. Aliased to the read model's own declaration rather
// than restated so the two cannot drift.
type Store = sessionview.Store

// Config carries what the routes need. A nil Sessions leaves them
// unregistered: without a database there is no audit history to derive a
// session from.
type Config struct {
	// Sessions is the session read model over the audit log.
	Sessions Store
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the caller-scoped session routes on mux. Every route is
// read-only, and every one of them is scoped to the authenticated caller.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Sessions == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/portal/sessions", h.listSessions)
	mux.HandleFunc("GET /api/v1/portal/sessions/{id}", h.getSession)
}
