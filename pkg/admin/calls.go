package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/callapi"
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
)

// CallCatalog reads the recorded data-access calls. Aliased to the seam's own
// declaration rather than restated so the two cannot drift.
type CallCatalog = callapi.Store

// CallPromoter publishes a reviewed record.
type CallPromoter = callrecord.Promoter

// registerCallRoutes mounts the call catalog and its review actions,
// implemented in the callapi subpackage. The actor is supplied from here
// because the authenticated identity is the admin handler's to know.
func (h *Handler) registerCallRoutes() {
	callapi.Register(h.mux, callapi.Config{
		Calls:    h.deps.CallCatalog,
		Promoter: h.deps.CallPromoter,
		Actor:    adminUserEmail,
	})
}
