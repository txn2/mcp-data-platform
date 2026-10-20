// Package taskwiring assembles the MCP Tasks extension (#2009) for the platform
// facade: the `tasks:` configuration section, the capability it advertises,
// the receiving middleware that turns a declaring client's run_script and
// export calls into tasks, the tasks/* methods and the caller each is answered
// for, the runner that executes an export apart from its request, and the
// task Sources those methods answer from. The facade calls Offered and
// Install and holds nothing for it; the extension lives here because the
// facade is at its size budget.
package taskwiring

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/exportasync"
	"github.com/txn2/mcp-data-platform/internal/mcptask"
	"github.com/txn2/mcp-data-platform/internal/platform/calltask"
	"github.com/txn2/mcp-data-platform/internal/platform/iam"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/internal/platform/tooloutput"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// Config is the `tasks:` section. A client that declares the extension is
// answered with a task for run_script and the export tools, and follows the
// work with tasks/get and tasks/cancel. Enabled by default (nil = enabled)
// wherever there is a database; enabled: false answers every call directly,
// as for a client without the extension.
type Config struct {
	Enabled *bool `yaml:"enabled"`
	// RetentionDays is how long an export run as a task is kept, for its
	// caller to read the result; default 7. A run_script task is the run and
	// is kept as long as scripts.run_retention_days keeps the run.
	RetentionDays int `yaml:"retention_days"`
}

// defaultRetentionDays is how long a task kept for an export lives.
const defaultRetentionDays = 7

// day is one day of retention_days.
const day = 24 * time.Hour

// IsEnabled reports whether the extension is offered, defaulting to true.
func (c Config) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Retention returns how long an export task is kept.
func (c Config) Retention() time.Duration {
	days := c.RetentionDays
	if days <= 0 {
		days = defaultRetentionDays
	}
	return time.Duration(days) * day
}

// Offered reports whether the extension is offered: enabled, and a database
// to keep its tasks in.
func Offered(cfg Config, db *sql.DB) bool {
	return cfg.IsEnabled() && db != nil
}

// Lifecycle is where the export runner's start, stop and shutdown drain are
// registered.
type Lifecycle interface {
	OnComponent(start, stop func(context.Context) error)
	OnDrain(drain func(context.Context) error)
}

// Deps is what the extension is assembled from.
type Deps struct {
	DB        *sql.DB
	Config    Config
	Server    *mcp.Server
	Lifecycle Lifecycle
	// Authenticator, Personas and AdminPersona identify the caller of a
	// tasks/* request, which passes none of the tools/call layers that would,
	// the way a completion request is identified.
	Authenticator middleware.Authenticator
	Personas      *persona.Registry
	AdminPersona  string
	// Runs is the script layer's runs as tasks. The script layer is
	// assembled after the server, so it is read when a request arrives.
	Runs func() *scriptlayer.RunTasks
}

// Install assembles the extension where it is offered: it registers the
// tasks/* methods on the server and the runner on the lifecycle, and returns
// the receiving middleware, nil where the extension is not offered. It also
// returns the background exports (#2212), which every deployment with a
// database has, the extension offered or not: they are the same work for a
// client that does not declare it.
func Install(d Deps) (mcp.Middleware, exportasync.Runner) {
	h := New(d)
	if h == nil {
		return nil, background(d)
	}
	if err := h.Register(d.Server); err != nil {
		// Only a method name the SDK already defines refuses: a programming
		// error, surfaced at startup as the chain's order is.
		panic(fmt.Sprintf("mcp-data-platform: %v", err))
	}
	d.Lifecycle.OnComponent(h.Start, h.Stop)
	d.Lifecycle.OnDrain(h.Drain)
	return h.Middleware(), h.runner.Exports()
}

// background builds the runner for background exports alone, where the
// extension is off but there is a database to keep them in; nil, as an
// interface, without one.
func background(d Deps) exportasync.Runner {
	if d.DB == nil {
		return nil
	}
	h := &Handle{runner: calltask.New(calltask.Options{Store: calltask.NewPostgresStore(d.DB), Retention: d.Config.Retention()})}
	d.Lifecycle.OnComponent(h.Start, h.Stop)
	d.Lifecycle.OnDrain(h.Drain)
	return h.runner.Exports()
}

// Handle is the assembled extension. A nil Handle offers nothing.
type Handle struct {
	sources *mcptask.Sources
	runner  *calltask.Runner
	resolve mcptask.Resolver
}

// New assembles the extension, or returns nil where it is not offered.
func New(d Deps) *Handle {
	if !Offered(d.Config, d.DB) {
		return nil
	}
	runner := calltask.New(calltask.Options{Store: calltask.NewPostgresStore(d.DB), Retention: d.Config.Retention()})
	h := &Handle{sources: mcptask.NewSources(runner), runner: runner, resolve: resolver(d)}
	if d.Runs != nil {
		h.sources.Add(lateRuns{runs: d.Runs})
	}
	return h
}

// resolver identifies a tasks/* caller.
func resolver(d Deps) mcptask.Resolver {
	var personas middleware.PersonasForRoles
	if d.Personas != nil {
		personas = iam.PersonasForRoles(d.Personas)
	}
	return func(ctx context.Context, req mcp.Request) context.Context {
		if pc := middleware.ResolvePlatformContext(ctx, req, d.Authenticator, personas, d.AdminPersona); pc != nil {
			return middleware.WithPlatformContext(ctx, pc)
		}
		return ctx
	}
}

// lateRuns answers for run tasks through the script layer read at request
// time; before it exists, no run is a task.
type lateRuns struct {
	runs func() *scriptlayer.RunTasks
}

// Prefix is the run id prefix.
func (lateRuns) Prefix() string { return pkgsession.ScriptSessionPrefix }

// Get reports a run as a task.
func (l lateRuns) Get(ctx context.Context, id string) (*mcptask.Snapshot, error) {
	runs := l.runs()
	if runs == nil {
		return nil, mcptask.ErrNotFound
	}
	return runs.Get(ctx, id) //nolint:wrapcheck // the run source names the run it read
}

// Cancel cancels a run as cancel_run does.
func (l lateRuns) Cancel(ctx context.Context, id string) error {
	runs := l.runs()
	if runs == nil {
		return mcptask.ErrNotFound
	}
	return runs.Cancel(ctx, id) //nolint:wrapcheck // the run source names the run it canceled
}

// Tools names the calls that become tasks: a run_script run is its own
// record; an export is executed apart from its request.
func Tools() map[string]mcptask.Mode {
	return map[string]mcptask.Mode{
		scriptlayer.ToolNameRunScript: mcptask.Native,
		tooloutput.ToolTrinoExport:    mcptask.Detached,
		tooloutput.ToolAPIExport:      mcptask.Detached,
		tooloutput.ToolGraphQLExport:  mcptask.Detached,
	}
}

// Middleware is the receiving middleware, or nil where the extension is off.
func (h *Handle) Middleware() mcp.Middleware {
	if h == nil {
		return nil
	}
	return mcptask.Middleware(mcptask.Config{
		Tools: Tools(), Sources: h.sources, Detacher: h.runner, Resolve: h.resolve,
	})
}

// Register adds the tasks/* methods to server.
func (h *Handle) Register(server *mcp.Server) error {
	if h == nil {
		return nil
	}
	if err := mcptask.Register(server, h.sources); err != nil {
		return fmt.Errorf("registering the tasks methods: %w", err)
	}
	return nil
}

// Start starts the export runner's heartbeat.
func (h *Handle) Start(context.Context) error {
	if h != nil {
		h.runner.StartHeartbeat()
	}
	return nil
}

// Drain lets the exports running here finish, within ctx.
func (h *Handle) Drain(ctx context.Context) error {
	if h == nil {
		return nil
	}
	return h.runner.Drain(ctx) //nolint:wrapcheck // the runner names what it was draining
}

// Stop ends what is still running, recording that its server shut down.
func (h *Handle) Stop(ctx context.Context) error {
	if h != nil {
		h.runner.Stop(ctx)
	}
	return nil
}
