package admin

import (
	"github.com/txn2/mcp-data-platform/internal/admin/catalogapi"
)

// APICatalogStore is the subset of apigateway/catalog.Store that the admin
// API needs. Aliased to the seam's declaration rather than restated so the
// two cannot drift; see internal/admin/catalogapi for the rationale behind
// depending on a narrowed interface instead of the concrete store.
type APICatalogStore = catalogapi.CatalogStore

// registerCatalogRoutes mounts the API-catalog surface, implemented in the
// catalogapi subpackage.
func (h *Handler) registerCatalogRoutes() {
	catalogapi.Register(h.mux, catalogapi.Config{
		Catalogs:    h.deps.APICatalogStore,
		EmbedJobs:   h.deps.EmbedJobs,
		Reload:      h.deps.ReloadNotifier,
		Toolkits:    h.deps.ToolkitRegistry,
		Mutable:     h.isMutable(),
		Author:      requestAuthor,
		Decode:      decodeStrict,
		DecodeLimit: decodeStrictLimit,
	})
}
