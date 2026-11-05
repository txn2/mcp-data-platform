// Package apichangewire assembles keeping API catalog specs current (#2469)
// from the platform: the refresher every check goes through, the scheduled
// loop, the upstream warnings recorder the outbound chain feeds, the admin
// routes, and the operator's private prefixes for a spec fetch. A package of
// its own because the composition root is at its size budget.
package apichangewire

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/apichangeapi"
	"github.com/txn2/mcp-data-platform/internal/admin/catalogapi"
	"github.com/txn2/mcp-data-platform/internal/apichange"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/upstreamwarn"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// configModeFile is the config store mode in which the admin API is read
// only, as pkg/admin reads it.
const configModeFile = "file"

// Feature is the assembled feature. A nil *Feature (no API catalog store)
// does nothing.
type Feature struct {
	refresher *apichange.Refresher
	loop      *apichange.Loop
	routes    apichangeapi.Config
}

// Build assembles the feature, and installs the operator's private prefixes
// as every spec fetch's default and, with a database, the upstream warnings
// recorder. It returns nil when there is no API catalog store.
func Build(p *platform.Platform, notify *notifydelivery.Handle) *Feature {
	if p == nil || p.APIGatewayCatalogStore() == nil {
		return nil
	}
	installPrefixes(p.Config().APIGateway.CatalogFetch)
	catalogs := p.APIGatewayCatalogStore()
	writer := catalogapi.Config{
		Catalogs: catalogs, Toolkits: p.ToolkitRegistry(), Reload: p,
		EmbedJobs: p.APIGatewayEmbedJobsStore(),
	}
	r := &apichange.Refresher{Catalogs: catalogs, Write: writer.WriteSpec, Prepare: catalogapi.PrepareSpec}
	f := &Feature{refresher: r, routes: apichangeapi.Config{
		Service: r, Catalogs: catalogs, CatalogOf: catalogOf(p.ToolkitRegistry()),
		Mutable: p.ConfigStore() != nil && p.ConfigStore().Mode() != configModeFile,
	}}
	if db := p.DB(); db != nil {
		f.withDatabase(db, p.Config().Portal.PublicBaseURL, notify)
	}
	return f
}

// withDatabase adds what keeps state: watches and reports, callers, warnings,
// where they are sent, and the schedule.
func (f *Feature) withDatabase(db *sql.DB, portalBase string, notify *notifydelivery.Handle) {
	store := &apichange.Store{DB: db}
	usage := &apichange.Usage{DB: db}
	settings := apichange.NewSettings(db)
	notifier := &apichange.ChannelNotifier{Settings: settings, PortalBase: portalBase}
	if s := notify.AlertChannels(); s != nil {
		notifier.Sender = s
	}
	f.refresher.Store, f.refresher.Usage, f.refresher.Notify = store, usage, notifier
	f.loop = &apichange.Loop{Refresher: f.refresher, DB: db}
	upstreamwarn.SetObserver(apichange.NewWarnings(db, notifier))

	f.routes.Store, f.routes.Callers = store, usage.Callers
	f.routes.Warnings = func(ctx context.Context, conn string) ([]apichange.Warning, error) {
		return apichange.ListWarnings(ctx, db, conn)
	}
	f.routes.ReadSettings = func(ctx context.Context) (apichange.Settings, error) {
		return apichange.ReadSettings(ctx, settings)
	}
	f.routes.SaveSettings = func(ctx context.Context, s apichange.Settings, by string) error {
		return settings.Set(ctx, s, by) //nolint:wrapcheck // the section names itself
	}
	if channels := notify.Channels(); channels != nil {
		f.routes.ChannelExists = func(ctx context.Context, name string) bool {
			ch, err := channels.Get(ctx, name)
			return err == nil && ch != nil
		}
	}
}

// installPrefixes makes the operator's private prefixes every spec fetch's
// default. A bad entry is logged and none is installed: the guard stays
// closed rather than opening to a prefix nobody wrote.
func installPrefixes(cfg apicatalog.FetchConfig) {
	if len(cfg.AllowPrivateCIDRs) == 0 {
		return
	}
	prefixes, err := apicatalog.ParseCIDRs(cfg.AllowPrivateCIDRs)
	if err != nil {
		slog.Warn("api catalogs: ignoring apigateway.catalog_fetch.allow_private_cidrs", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	apicatalog.SetAllowPrivateCIDRs(prefixes)
}

// catalogOf asks the API gateway toolkits which catalog a connection reads.
func catalogOf(reg *registry.Registry) func(ctx context.Context, connection string) string {
	return func(ctx context.Context, connection string) string {
		if reg == nil {
			return ""
		}
		for _, tk := range reg.All() {
			if c, ok := tk.(interface {
				CatalogOf(ctx context.Context, connection string) string
			}); ok {
				if id := c.CatalogOf(ctx, connection); id != "" {
					return id
				}
			}
		}
		return ""
	}
}

// Mount registers the admin routes behind auth, when the admin API is on.
func (f *Feature) Mount(mux *http.ServeMux, p *platform.Platform,
	auth func(*platform.Platform) func(http.Handler) http.Handler, author func(*http.Request) string,
) {
	if f == nil || !p.Config().Admin.IsEnabled() {
		return
	}
	f.routes.Author = author
	apichangeapi.Register(mux, auth(p), f.routes)
}

// Start begins the scheduled checks.
func (f *Feature) Start(ctx context.Context) {
	if f != nil {
		f.loop.Start(ctx)
	}
}

// Stop ends the scheduled checks.
func (f *Feature) Stop() {
	if f != nil {
		f.loop.Stop()
	}
}
