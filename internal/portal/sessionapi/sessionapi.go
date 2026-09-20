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
//
// It also serves /api/v1/portal/events/{id}, the drill-down behind one row of
// that timeline. It lives here rather than on a surface of its own because it
// exists for the timeline and is scoped by the same rule: a timeline entry
// carries no parameters and no error text, and the call catalog is not the
// answer -- a call record is written only for the sql, api and graphql kinds,
// so most of a session's rows would open nothing (#1797).
package sessionapi

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
	"github.com/txn2/mcp-data-platform/pkg/audit"
)

// Store reads sessions. Aliased to the read model's own declaration rather
// than restated so the two cannot drift.
type Store = sessionview.Store

// Events reads the audit log. It is the one method the event drill-down needs,
// declared here rather than taking pkg/audit's whole store so the seam states
// what it uses.
type Events interface {
	Query(ctx context.Context, filter audit.QueryFilter) ([]audit.Event, error)
}

// Config carries what the routes need. A nil Sessions leaves them
// unregistered: without a database there is no audit history to derive a
// session from.
type Config struct {
	// Sessions is the session read model over the audit log.
	Sessions Store
	// Events reads one call in full. A nil Events leaves the drill-down
	// route unregistered and the timeline still lists; the rows simply have
	// nothing to open.
	Events Events
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
	if cfg.Events != nil {
		mux.HandleFunc("GET /api/v1/portal/events/{id}", h.getEvent)
	}
}
