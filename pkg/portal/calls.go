package portal

import (
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/portal/callapi"
)

// CallCatalog reads the recorded data-access calls. Aliased to the seam's own
// declaration rather than restated so the two cannot drift, and identical to
// the operator surface's alias: there is one catalog, and the portal's face of
// it differs only in being scoped to the caller.
type CallCatalog = callapi.Store

// CallPromoter publishes a record its owner chose to publish.
type CallPromoter = callrecord.Promoter

// registerCallRoutes mounts the caller's own calls, implemented in the
// internal/portal/callapi seam. A call record is derived from audit history, so
// with no CallCatalog wired there is nothing to read and the seam registers
// nothing.
func (h *Handler) registerCallRoutes() {
	callapi.Register(h.mux, callapi.Config{
		Calls:    h.deps.CallCatalog,
		Promoter: h.deps.CallPromoter,
	})
}
