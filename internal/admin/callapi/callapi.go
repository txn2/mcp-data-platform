// Package callapi serves the /api/v1/admin/calls surface: every data-access
// call the platform recorded, across every caller, and the review queue of the
// ones worth publishing.
//
// It is the operator face of the catalog the portal surface in
// internal/portal/callapi reads (internal/platform/callrecord). The read model
// is one; what differs is the scope, and one facet the portal deliberately does
// not offer: an operator can narrow the list to one person's calls, because the
// operator surface is unrestricted by design and a user list that carried a
// user facet would be a filter whose value is always overwritten anyway.
package callapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
)

// Store reads call records. Aliased to the catalog's own declaration rather
// than restated so the two cannot drift.
type Store = callrecord.Store

// Config carries what the routes need.
type Config struct {
	// Calls is the call catalog. Nil leaves the routes unregistered.
	Calls Store
	// Promoter publishes a reviewed record. Nil leaves the two action routes
	// unregistered.
	Promoter *callrecord.Promoter
	// Actor names the operator performing an action, for the record of who
	// promoted or declined it. Supplied by the admin handler, which owns the
	// authenticated identity.
	Actor func(*http.Request) string
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the operator call routes on mux.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Calls == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/admin/calls", h.listCalls)
	mux.HandleFunc("GET /api/v1/admin/calls/{id}", h.getCall)
	if cfg.Promoter == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/admin/calls/{id}/promote", h.promoteCall)
	mux.HandleFunc("POST /api/v1/admin/calls/{id}/reject", h.rejectCall)
}

// actor names the operator taking an action, or "" when the handler was wired
// without an identity source.
func (h *handler) actor(r *http.Request) string {
	if h.cfg.Actor == nil {
		return ""
	}
	return h.cfg.Actor(r)
}
