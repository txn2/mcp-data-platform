package portal

import (
	"github.com/txn2/mcp-data-platform/internal/portal/sessionapi"
)

// SessionViewer reads the audit-derived session record. Aliased to the seam's
// declaration rather than restated so the two cannot drift, and identical to
// the operator surface's alias: there is one read model, and the portal's face
// of it differs only in being scoped to the caller.
type SessionViewer = sessionapi.Store

// registerSessionRoutes mounts the caller's own sessions, implemented in the
// internal/portal/sessionapi seam. A session is derived from audit history, so
// with no SessionViewer wired there is nothing to derive one from and the seam
// registers nothing.
func (h *Handler) registerSessionRoutes() {
	sessionapi.Register(h.mux, sessionapi.Config{Sessions: h.deps.SessionViewer})
}
