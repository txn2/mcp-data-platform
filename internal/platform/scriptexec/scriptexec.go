// Package scriptexec executes managed scripts: the run worker that claims due
// runs off the queue, the per-run script principal it executes them under, and
// the writer that turns a script's output into a portal asset.
//
// It is the half of the managed-script feature that runs with nobody present.
// The authoring half (internal/platform/scriptlayer) is interactive and runs
// as whoever is typing; everything here runs later, unattended, which is why
// the run gate is re-read on every run rather than trusted from the queue row
// that got here:
//
//   - code comes from the version the run was queued against, the latest saved
//     version at the moment of the request, so a run always executes an
//     immutable snapshot a person saved;
//   - authority is the roles that version's AUTHOR held when they saved it, so
//     an unattended run can never exceed what the person who wrote the code
//     could do;
//   - every capability call goes through the assembled MCP server over an
//     in-memory session, so persona and connection authorization, rate limiting,
//     and audit apply to a script exactly as they apply to an agent.
//
// The package must not import pkg/platform. The composition root passes in the
// stores, the assembled server, and the portal dependencies it already holds.
package scriptexec

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyprefs"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyqueue"
	"github.com/txn2/mcp-data-platform/internal/pglisten"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Structured-logging keys.
const (
	logKeyError = "error"
	logKeyRunID = "run_id"
)

// DefaultRunRetention is how long a terminal run row is kept when the
// deployment names no retention.
//
// It is a year, an order of magnitude beyond the notification queue's thirty
// days, because these rows are not queue bookkeeping. A scheduled script's run
// history is its refresh history — the record of what a dashboard was showing
// and when it last succeeded — which is product surface a person reads, not
// residue a sweep should be eager to remove.
const DefaultRunRetention = 365 * 24 * time.Hour

// Config carries everything the execution side needs. No platform types.
type Config struct {
	// DB backs the run, script, and version stores. A nil DB with no stores
	// supplied disables the feature: New returns nil and every method on the
	// nil handle is a no-op.
	DB *sql.DB

	// Runs, Scripts, Versions and Schedules, when non-nil, are used directly
	// instead of building PostgreSQL stores from DB. Production passes DB and
	// leaves them nil; they exist so the execution side can be assembled over
	// in-memory stores, which is the only way to exercise the worker, the
	// engine, and the middleware chain together without a database. The same
	// shape as scriptlayer.Config.Store.
	Runs      script.RunStore
	Scripts   ScriptReader
	Versions  VersionReader
	Schedules script.ScheduleStore

	// DSN is the raw database DSN for the LISTEN connection that wakes the
	// worker the moment a run is enqueued. Empty degrades to poll-only.
	DSN string

	// Server is the assembled MCP server a run drives over an in-memory
	// session. A nil server leaves the worker running but every run fails with
	// "script execution is unavailable", which is the honest report.
	Server *mcp.Server

	// Export carries the portal dependencies platform.export writes through. An
	// incomplete set leaves scripts able to query and unable to persist, which
	// each affected run reports.
	Export ExportDeps

	// Destinations is the deployment's configured bucket destinations, which a
	// run resolves platform.export names against at run time.
	Destinations []script.Destination

	// Metrics records what the run queue is doing: runs by script, trigger and
	// status, their duration, how many are executing on this replica, and the
	// fires the misfire policy stepped over (#1307). Optional — every method on
	// it is nil-safe — but without it the automations are invisible to an
	// operator watching the platform rather than reading the run table.
	Metrics *observability.Metrics

	// Audit records the script_run lifecycle event. Optional.
	Audit middleware.AuditLogger

	// Notifier queues the alert a failed SCHEDULED run raises. When nil and DB
	// is set, one is built over the notification queue unless
	// NotificationsDisabled says the deployment turned notifications off.
	Notifier Notifier

	// NotificationsDisabled mirrors notifications.enabled: false in YAML.
	NotificationsDisabled bool

	// DigestHourUTC is the hour daily digests are scheduled for, for a
	// recipient whose delivery mode is daily.
	DigestHourUTC int

	// RunRetention overrides DefaultRunRetention.
	RunRetention time.Duration

	// WorkerDisabled leaves this replica serving without ever claiming from the
	// run queue. run_script still enqueues and still waits on the result, which
	// a separate deployment of the same binary with the worker on produces, so
	// script execution scales apart from serving and a pathological script
	// reaches no pod an agent is talking to. The zero value runs the worker,
	// which is the single-binary default.
	WorkerDisabled bool
}

// ExportDeps is what turning a script's rows into a portal asset needs.
type ExportDeps struct {
	Assets   portal.AssetStore
	Versions portal.VersionStore
	S3       portal.S3Client
	Bucket   string
	Prefix   string
}

// ready reports whether an output can actually be written.
func (d ExportDeps) ready() bool {
	return d.Assets != nil && d.Versions != nil && d.S3 != nil && d.Bucket != ""
}

// Notifier queues one notification. It is the enqueue half of the email
// substrate, narrowed to the one call this package makes.
type Notifier interface {
	Notify(ctx context.Context, recipient, category string, p notification.Payload) (bool, error)
}

// newNotifier builds the enqueue side of the notification substrate over db.
//
// It is built here rather than handed in because of when this package is
// assembled. The substrate's running handle (internal/platform/notifydelivery)
// belongs to the HTTP composition root and is constructed after the platform
// has started — by which time the run worker is already claiming — and a
// worker-only deployment may never build one at all. The enqueue side is
// stateless over the pool, so it is constructed where it is used; the rows it
// writes are drained by whichever replica runs the send worker, which is the
// same arrangement every other producer of notifications relies on.
func newNotifier(cfg Config) Notifier {
	if cfg.Notifier != nil {
		return cfg.Notifier
	}
	if cfg.DB == nil || cfg.NotificationsDisabled {
		return nil
	}
	return notification.NewEnqueuer(
		notifyprefs.NewPostgresStore(cfg.DB),
		notifyqueue.NewPostgresStore(cfg.DB),
		cfg.DigestHourUTC)
}

// listenerControl narrows the LISTEN adapter to the two calls the handle makes,
// so the degraded-startup path is testable without a live Postgres. It mirrors
// notifydelivery, which needs the same seam for the same reason.
type listenerControl interface {
	Start(ctx context.Context) error
	Stop()
}

// Handle owns the running execution side.
//
// A handle with no worker is the split deployment's serving half: it still owns
// the queue, so run_script enqueues and waits exactly as it does on a
// single-binary replica, and nothing here ever claims. The listener goes with
// the worker, because waking a replica that will not claim buys nothing but a
// database connection.
type Handle struct {
	runs      script.RunStore
	worker    *worker
	scheduler *scheduler
	listener  listenerControl
	// closer releases the notification enqueuer this handle built, and is nil
	// when the notifier was supplied by the caller (whose lifecycle it is).
	closer func()
}

// New composes the execution side. It returns nil when there is nowhere to keep
// runs, because a run queue with no storage is not a degraded feature, it is no
// feature; every method here is nil-safe so the caller wires it unconditionally.
func New(cfg Config) *Handle {
	stores := cfg.stores()
	if stores.runs == nil {
		return nil
	}
	h := &Handle{runs: stores.runs}
	if cfg.WorkerDisabled {
		slog.Info("scripts: the run worker is off on this replica; queued runs wait for a worker deployment")
		return h
	}
	notifier := newNotifier(cfg)
	if enq, ok := notifier.(*notification.Enqueuer); ok && cfg.Notifier == nil {
		h.closer = enq.Close
	}
	h.worker = newWorker(workerConfig{
		runs:      stores.runs,
		scripts:   stores.scripts,
		versions:  stores.versions,
		runner:    newRunner(stores.runs, cfg),
		retention: orDefaultRetention(cfg.RunRetention),
		notifier:  notifier,
		metrics:   cfg.Metrics,
	})
	// Materializing where the worker runs, for the same reason the listener
	// does: a replica that will not claim gains nothing by producing rows for
	// one that will.
	h.scheduler = newScheduler(schedulerConfig{
		schedules: stores.schedules,
		scripts:   stores.scripts,
		versions:  stores.versions,
		wake:      h.worker.Notify,
		metrics:   cfg.Metrics,
	})
	if cfg.DSN != "" {
		h.listener = pglisten.New(cfg.DSN, scriptstore.NotifyChannel, h)
	}
	return h
}

// stores resolves the stores the execution side reads and writes, preferring
// what the caller supplied and falling back to PostgreSQL over DB. They come
// from one place so a caller cannot end up with a run queue in one store and
// the scripts it names in another.
//
// Schedules is the one that may legitimately be absent: an assembly with
// in-memory run, script, and version stores and no schedule store is the
// execution side with no scheduling, which the materializer reads as "this
// deployment does not schedule" rather than as a broken configuration.
func (c Config) stores() storeSet {
	runs, scripts, versions, schedules := c.Runs, c.Scripts, c.Versions, c.Schedules
	if c.DB != nil {
		store := scriptstore.New(c.DB)
		if runs == nil {
			runs = store
		}
		if scripts == nil {
			scripts = store
		}
		if versions == nil {
			versions = store
		}
		if schedules == nil {
			schedules = store
		}
	}
	if runs == nil || scripts == nil || versions == nil {
		return storeSet{}
	}
	return storeSet{runs: runs, scripts: scripts, versions: versions, schedules: schedules}
}

// storeSet is the resolved set of stores the execution side reads and writes.
// It is one value rather than four returns so a caller cannot pair them up
// wrongly, and so adding the next one does not change every call site.
type storeSet struct {
	runs      script.RunStore
	scripts   ScriptReader
	versions  VersionReader
	schedules script.ScheduleStore
}

// orDefaultRetention applies the default when a deployment names no retention.
func orDefaultRetention(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultRunRetention
	}
	return d
}

// Runs exposes the queue so the tool surface can enqueue and follow runs
// without building a second store over the same table. Nil-safe.
func (h *Handle) Runs() script.RunStore {
	if h == nil {
		return nil
	}
	return h.runs
}

// Notify wakes the run worker without waiting for its poll tick. The queue's
// LISTEN adapter calls it on every pg_notify the store fires, which is how an
// enqueue on any replica reaches a worker on any other; a producer holding this
// handle can call it directly and skip the round trip. Nil-safe.
func (h *Handle) Notify() {
	if h == nil || h.worker == nil {
		return
	}
	h.worker.Notify()
}

// Start launches the run worker and, when configured, the LISTEN adapter that
// wakes it on enqueue. A LISTEN failure degrades to the worker's poll interval
// rather than failing startup: the wakeup is a latency optimization, and the
// poll is what makes the queue correct. Nil-safe.
func (h *Handle) Start(ctx context.Context) error {
	if h == nil || h.worker == nil {
		return nil
	}
	h.worker.Start(ctx)
	h.scheduler.Start(ctx)
	if h.listener != nil {
		if err := h.listener.Start(ctx); err != nil {
			slog.Warn("scripts: run queue listener unavailable; falling back to polling", logKeyError, err)
			h.listener = nil
		}
	}
	return nil
}

// Stop closes the listener and the materializer, then drains the worker inside
// whatever budget ctx carries: the run in flight finishes if it can and is
// released if it cannot.
//
// Both producers stop before the consumer. The listener goes first so nothing
// wakes a worker that is already draining, and the materializer goes with it so
// the shutdown budget is not spent executing a fire that arrived during it —
// the fire is not lost, because the schedule is not advanced until its run
// exists, so the replica that takes over materializes it. Nil-safe.
func (h *Handle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if h.listener != nil {
		h.listener.Stop()
	}
	h.scheduler.Stop()
	if h.worker != nil {
		h.worker.Stop(ctx)
	}
	if h.closer != nil {
		h.closer()
	}
	return nil
}
