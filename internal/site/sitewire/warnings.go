package sitewire

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/configwarn"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// siteLister lists every site.
type siteLister interface {
	List(ctx context.Context) ([]site.Site, error)
}

// warnReservedLabels logs a configuration warning naming each site whose
// <site>--<env> addresses are labels certificate authorities refuse: a
// two-character name, which a new site can no longer take (#2611). Nothing
// with no pages domain, where no site has such an address.
func warnReservedLabels(ctx context.Context, sites siteLister, pagesDomain string) {
	if pagesDomain == "" {
		return
	}
	list, err := sites.List(ctx)
	if err != nil {
		slog.WarnContext(ctx, "sites: listing sites for the startup checks failed", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	for i := range list {
		note := site.ReservedLabelNote(list[i].Name, pagesDomain)
		if note == "" {
			continue
		}
		configwarn.Warn(ctx, configwarn.CodeSiteReservedLabel, "a site's environments have no address on the pages domain",
			"site", logsan.SanitizeForLog(list[i].Name), "reason", note)
	}
}
