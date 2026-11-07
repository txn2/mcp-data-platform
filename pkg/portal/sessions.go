package portal

import (
	"github.com/txn2/mcp-data-platform/internal/portal/peopleapi"
	"github.com/txn2/mcp-data-platform/internal/portal/sessionapi"
)

// SessionViewer reads the audit-derived session record. Aliased to the seam's
// declaration rather than restated so the two cannot drift, and identical to
// the operator surface's alias: there is one read model, and the portal's face
// of it differs only in being scoped to the caller.
type SessionViewer = sessionapi.Store

// AuditEvents reads one of the caller's own calls in full, for the drill-down
// behind a session timeline row. Aliased to the seam's declaration for the same
// reason SessionViewer is.
type AuditEvents = sessionapi.Events

// registerSessionRoutes mounts the caller's own sessions, implemented in the
// internal/portal/sessionapi seam. A session is derived from audit history, so
// with no SessionViewer wired there is nothing to derive one from and the seam
// registers nothing.
func (h *Handler) registerSessionRoutes() {
	sessionapi.Register(h.mux, sessionapi.Config{
		Sessions: h.deps.SessionViewer,
		Events:   h.deps.AuditEvents,
	})
}

// registerPeopleRoutes mounts who is behind an asset and whom it is shared
// with (#2531), served by internal/portal/peopleapi over the portal's stores
// and its authorization core.
func (h *Handler) registerPeopleRoutes() {
	cfg := peopleapi.Config{Access: h.access}
	if h.deps.AssetStore != nil {
		cfg.Assets = h.deps.AssetStore
	}
	if h.deps.VersionStore != nil {
		cfg.Versions = h.deps.VersionStore
	}
	if h.deps.ShareStore != nil {
		cfg.Shares = h.deps.ShareStore
	}
	peopleapi.Register(h.mux, cfg)
}
