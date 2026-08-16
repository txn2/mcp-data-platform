package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/sessionapi"
)

// SessionViewer reads the audit-derived session record. Aliased to the seam's
// declaration rather than restated so the two cannot drift.
type SessionViewer = sessionapi.Store

// registerSessionRoutes mounts the sessions surface, implemented in the
// sessionapi subpackage. A session is derived from audit history, so the
// routes stay unregistered wherever the audit event surface is: the 409
// fallback registerAuditRoutes mounts on /api/v1/admin/audit/ is about audit
// being configured without a database, and sessions have no separate
// configuration to be inconsistent with.
func (h *Handler) registerSessionRoutes() {
	sessionapi.Register(h.mux, sessionapi.Config{Sessions: h.deps.SessionViewer})
}
