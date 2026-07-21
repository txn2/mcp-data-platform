package platform

import (
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/utilconn"
)

// wireUtilConnection registers the built-in util connection seed
// (issue #1005). It reads the catalog store and embed-jobs queue that
// WireGatewayIntegrations wires, so WireRuntime sequences it after
// that step. Deliberately a free function composing the
// internal/platform/utilconn seam, not a Platform method: the
// god-object budget (godobject_budget_test.go) is a frozen standing
// invariant.
func wireUtilConnection(p *Platform) {
	tk := p.firstAPIGatewayToolkit()
	catalogStore := p.APIGatewayCatalogStore()
	prereqsMet := tk != nil && catalogStore != nil
	utilCfg := p.config.APIGateway.UtilConnection
	if !utilCfg.UtilConnectionEnabled(prereqsMet) {
		if !prereqsMet {
			slog.Debug("util connection: prerequisites not met; skipping",
				"have_toolkit", tk != nil, "have_catalog_store", catalogStore != nil)
		}
		return
	}

	// Pass the enqueuer as a nil interface (not a typed nil) when the
	// embed queue is unwired, so the seed's nil check works correctly.
	var enqueuer utilconn.Enqueuer
	if store := p.APIGatewayEmbedJobsStore(); store != nil {
		enqueuer = store
	}

	if err := utilconn.Register(utilconn.Deps{
		Toolkit:           tk,
		Catalog:           catalogStore,
		Enqueuer:          enqueuer,
		OnStart:           p.lifecycle.OnStart,
		AllowPrivateCIDRs: utilCfg.AllowPrivateCIDRs,
	}); err != nil {
		// Non-fatal: a bad allow_private_cidrs entry must not block
		// startup. The connection simply does not register; the error
		// tells the operator which entry to fix.
		slog.Warn("util connection: registration failed", "error", err)
	}
}
