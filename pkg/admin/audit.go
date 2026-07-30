package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/auditapi"
)

// AuditQuerier queries audit events. Aliased to the seam's declaration rather
// than restated so the two cannot drift.
type AuditQuerier = auditapi.EventQuerier

// AuditMetricsQuerier provides aggregate audit metrics. Aliased to the seam's
// declaration for the same reason.
type AuditMetricsQuerier = auditapi.MetricsQuerier

// registerAuditRoutes mounts the audit surface, implemented in the auditapi
// subpackage, or a 409 fallback when audit is enabled in config but no
// database is available. The fallback stays here because it is a statement
// about platform configuration rather than about the audit routes.
func (h *Handler) registerAuditRoutes() {
	auditapi.Register(h.mux, auditapi.Config{
		Events:  h.deps.AuditQuerier,
		Metrics: h.deps.AuditMetricsQuerier,
	})
	if h.deps.AuditQuerier == nil && h.deps.Config != nil &&
		(h.deps.Config.Audit.Enabled == nil || *h.deps.Config.Audit.Enabled) {
		h.mux.HandleFunc("/api/v1/admin/audit/", h.featureUnavailable("audit", "database"))
	}
}
