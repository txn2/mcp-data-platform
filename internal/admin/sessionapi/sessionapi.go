// Package sessionapi serves the /api/v1/admin/sessions surface: the sessions
// the audit log has recorded, and one session opened in full — its summary,
// the assets and insights it produced, and the ordered record of its calls.
//
// It is a decomposition seam of pkg/admin (which sits at its package size
// budget); the parent registers it on the admin mux, so every route here is
// already behind the admin persona gate. The rows carry the caller's identity
// and the purpose stated for each call, because that is what an operator
// reading a session after the fact needs.
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

// Register mounts the session routes on mux. Every route is read-only.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Sessions == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/sessions", h.listSessions)
	mux.HandleFunc("GET /api/v1/admin/sessions/{id}", h.getSession)
}
