package admin

import (
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// serveRefs rewrites an asset's stored content for the admin console the same
// way the portal rewrites it for the owner: every mcp:// URI the asset declared
// becomes the URL its reference is served under (#1474).
//
// The admin console is not a separate access model — an administrator is
// unrestricted by design, and an asset that renders for its owner and shows
// broken images for the operator reviewing it is a defect in the console, not
// a boundary. The reference URLs it produces are the same ones the owner's
// page uses, so nothing here widens what a reference already grants.
//
// A failure to read the references serves the content as stored rather than
// failing the read.
func (h *Handler) serveRefs(r *http.Request, assetID, contentType string, data []byte) []byte {
	if h.deps.ResourceRefs == nil || assetID == "" {
		return data
	}
	refs, err := h.deps.ResourceRefs.ListByAsset(r.Context(), assetID)
	if err != nil {
		slog.Warn("admin asset resource references: list failed, serving content as stored",
			"asset_id", logsan.SanitizeForLog(assetID),
			"error", logsan.SanitizeForLog(err.Error()))
		return data
	}
	return assetrefs.Rewrite(data, contentType,
		assetrefs.BaseURL(r, h.deps.PublicBaseURL), assetID, refs)
}
