// Package appdbwire assembles the operation of application databases from
// the platform (#2406, #2409, #2410, #2427): the health and drift loop and
// its settings, the alert engine posting to notification channels, the gauges,
// the admin routes, and what manage_schema's status and describe add to their
// answers.
package appdbwire

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/admin/appdbapi"
	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/appdbhealth"
	"github.com/txn2/mcp-data-platform/internal/appdbwatch"
	"github.com/txn2/mcp-data-platform/internal/httpserver/storagewire"
	"github.com/txn2/mcp-data-platform/internal/platform/callcatchup"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// pagePath opens the admin connection page an alert links to.
const pagePath = "/portal/admin/connections/appdb/"

// Admin is the admin API the routes mount behind: its authentication, and
// who made a request.
type Admin struct {
	Auth  func() func(http.Handler) http.Handler
	Actor func(*http.Request) string
}

// Mount assembles everything, mounts the admin routes when the admin API is
// on, and starts the loop, which runs until ctx ends. Without a database it
// does nothing.
func Mount(ctx context.Context, mux *http.ServeMux, p *platform.Platform, notify *notifydelivery.Handle, admin Admin) {
	if p == nil || p.DB() == nil {
		return
	}
	var wrap func(http.Handler) http.Handler
	if p.Config().Admin.IsEnabled() {
		wrap = admin.Auth()
	}
	// A connection another replica saved is taken on from the store, as the
	// admin connection test takes it on (#1888).
	catchUp := callcatchup.New(p.ToolkitRegistry(), callcatchup.Reader(p.ConnectionStore(), platform.ErrConnectionNotFound,
		func(inst *platform.ConnectionInstance) map[string]any { return inst.Config }))
	start(ctx, mux, deps{
		db: p.DB(), sender: storagewire.AlertSender(notify), base: p.Config().Portal.PublicBaseURL,
		all: p.ToolkitRegistry().All, audit: p.Audit().Logger(), gauges: p.Metrics().RegisterAppDBHealth, wrap: wrap,
		actor: admin.Actor, takeOn: catchUp.TakeOn,
	})
}

// deps is what start reads from the platform.
type deps struct {
	db     *sql.DB
	sender levelalert.Sender
	base   string
	all    func() []registry.Toolkit
	audit  middleware.AuditLogger
	// gauges installs what the health gauges read.
	gauges func(func(ctx context.Context) []observability.AppDBHealthSample)
	// wrap is the admin API's authentication, nil when the admin API is off.
	wrap func(http.Handler) http.Handler
	// actor names the administrator making a request.
	actor func(*http.Request) string
	// takeOn puts a connection another replica saved into service here.
	takeOn func(ctx context.Context, kind, name string)
}

// start assembles the service, installs the gauges and the schema checks,
// mounts the routes when there is an admin API, and starts the loop.
func start(ctx context.Context, mux *http.ServeMux, d deps) func() {
	svc, cfg := assemble(d.db, d.sender, d.base, d.all)
	cfg.Audit, cfg.Actor, cfg.TakeOn = d.audit, d.actor, d.takeOn
	d.gauges(svc.Gauges)
	Install(d.all(), cfg.Store, svc)
	if d.wrap != nil {
		appdbapi.Register(mux, d.wrap, cfg)
	}
	svc.Start(ctx)
	return svc.Stop
}

// assemble builds the service and the routes' configuration over db.
func assemble(db *sql.DB, sender levelalert.Sender, base string, all func() []registry.Toolkit) (*appdbwatch.Service, appdbapi.Config) {
	store := migrationstore.New(db)
	svc := appdbwatch.New(appdbwatch.Deps{
		DB: db, Settings: appdbwatch.NewSettingsStore(db), Migrations: store,
		Engine: levelalert.New(levelalert.NewPostgresStore(db), sender),
		Kit:    watchKit(all), PageURL: pageURL(base),
	})
	return svc, appdbapi.Config{Store: store, Watch: svc, Kit: apiKit(all)}
}

// watchKit is the live postgres toolkit as the loop reads it: nil, never a
// typed nil, when none is registered.
func watchKit(all func() []registry.Toolkit) func() appdbwatch.Kit {
	return func() appdbwatch.Kit {
		pg := live(all)
		if pg == nil {
			return nil // an interface holding a nil *Toolkit is not nil
		}
		return pg
	}
}

// apiKit is the live postgres toolkit as the admin routes use it.
func apiKit(all func() []registry.Toolkit) func() appdbapi.Kit {
	return func() appdbapi.Kit {
		if k := live(all); k != nil {
			return appdbapi.Wrap(k)
		}
		return nil
	}
}

// pageURL is the connection page an alert links to under base, none without
// one.
func pageURL(base string) func(connection string) string {
	base = strings.TrimRight(base, "/")
	return func(connection string) string {
		if base == "" {
			return ""
		}
		return base + pagePath + url.PathEscape(connection)
	}
}

// live is the registered postgres toolkit, or nil.
func live(all func() []registry.Toolkit) *postgreskit.Toolkit {
	for _, tk := range all() {
		if pg, ok := tk.(*postgreskit.Toolkit); ok {
			return pg
		}
	}
	return nil
}

// Install gives each postgres toolkit what manage_schema's status and
// describe add (#2406, #2409): status reports the schema's drift, and
// describe the findings a build can act on (an index never scanned, indexes
// that index the same thing, a table whose dead tuples pass the dead tuple
// threshold). Neither carries statement text.
func Install(toolkits []registry.Toolkit, store appdbdrift.Migrations, svc *appdbwatch.Service) {
	for _, tk := range toolkits {
		if pg, ok := tk.(*postgreskit.Toolkit); ok {
			pg.SetSchemaChecks(driftCheck(pg, store), findings(pg, svc))
		}
	}
}

// driftCheck is what manage_schema status adds: the schema's drift.
func driftCheck(pg *postgreskit.Toolkit, store appdbdrift.Migrations) func(ctx context.Context, connection, schema string) any {
	return func(ctx context.Context, connection, schema string) any {
		return appdbdrift.Check(ctx, pg, store, connection, schema)
	}
}

// findings is what manage_schema describe adds, judged on the dead tuple
// warning the health settings hold.
func findings(q appdbhealth.Querier, svc *appdbwatch.Service) func(ctx context.Context, connection, schema string, d *postgreskit.Description) any {
	return func(ctx context.Context, connection, schema string, _ *postgreskit.Description) any {
		ratio := appdbwatch.DefaultDeadTuplePercent.Warning
		if view, err := svc.Settings(ctx); err == nil && view.DeadTuplePercent.Warning > 0 {
			ratio = view.DeadTuplePercent.Warning
		}
		return appdbhealth.Findings(ctx, q, connection, schema, ratio/percent)
	}
}

// percent scales a percent setting to a ratio.
const percent = 100
