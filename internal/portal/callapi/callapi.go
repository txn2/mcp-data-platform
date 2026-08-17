// Package callapi serves the /api/v1/portal/calls surface: the data-access
// calls the calling user made, and one of them opened in full.
//
// It is the caller-scoped face of the same catalog the operator surface in
// internal/admin/callapi reads (internal/platform/callrecord), not a second
// derivation of it. The only difference between the two is the scope: every
// read here carries the caller's user id, so a record belonging to someone else
// is answered as not-found rather than as a refusal, and the answer is the same
// one an id that was never used gets.
//
// A user reading their own calls is reading their own work: the queries they
// ran, why they said they were running them, which ones produced something, and
// which of those are worth publishing to the catalog.
package callapi

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
)

// Store reads call records. Aliased to the catalog's own declaration rather
// than restated so the two cannot drift.
type Store = callrecord.Store

// Config carries what the routes need. A nil Calls leaves them unregistered:
// without a database there is no catalog to read.
type Config struct {
	// Calls is the call catalog.
	Calls Store
	// Promoter publishes a record the owner chose to publish. Nil leaves the
	// two action routes unregistered, which is what a deployment with no
	// promotion target gets.
	Promoter *callrecord.Promoter
}

// handler binds the routes to their dependencies.
type handler struct {
	cfg Config
}

// Register mounts the caller-scoped call routes on mux. Every route is scoped
// to the authenticated caller, reads included.
func Register(mux *http.ServeMux, cfg Config) {
	if cfg.Calls == nil {
		return
	}
	h := &handler{cfg: cfg}
	mux.HandleFunc("GET /api/v1/portal/calls", h.listCalls)
	mux.HandleFunc("GET /api/v1/portal/calls/{id}", h.getCall)
	if cfg.Promoter == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/portal/calls/{id}/promote", h.promoteCall)
	mux.HandleFunc("POST /api/v1/portal/calls/{id}/reject", h.rejectCall)
}
