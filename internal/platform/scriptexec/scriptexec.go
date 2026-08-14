// Package scriptexec executes approved managed scripts: the run worker that
// claims due runs off the queue, the per-run script principal it executes them
// under, and the writer that turns a script's output into a portal asset.
//
// It is the half of the managed-script feature that runs with nobody present.
// The authoring half (internal/platform/scriptlayer) is interactive and runs as
// whoever is typing; everything here runs later, unattended, which is why the
// execution gate exists and why this package reads it on every run rather than
// trusting the queue row that got here:
//
//   - code comes from scripts.approved_version_id and nothing else, so a run
//     enqueued against a version that is no longer the approved one fails
//     instead of executing code that lost its approval;
//   - authority comes from the grant bound to that version at approval, which
//     carries the roles the version's AUTHOR held, so an unattended run can
//     never exceed what the person who wrote the code could do;
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

	"github.com/txn2/mcp-data-platform/internal/pglisten"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
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

	// Runs, Scripts and Versions, when non-nil, are used directly instead of
	// building PostgreSQL stores from DB. Production passes DB and leaves them
	// nil; they exist so the execution side can be assembled over in-memory
	// stores, which is the only way to exercise the worker, the engine, and the
	// middleware chain together without a database. The same shape as
	// scriptlayer.Config.Store.
	Runs     script.RunStore
	Scripts  ScriptReader
	Versions VersionReader

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

	// Audit records the script_run lifecycle event. Optional.
	Audit middleware.AuditLogger

	// RunRetention overrides DefaultRunRetention.
	RunRetention time.Duration
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

// listenerControl narrows the LISTEN adapter to the two calls the handle makes,
// so the degraded-startup path is testable without a live Postgres. It mirrors
// notifydelivery, which needs the same seam for the same reason.
type listenerControl interface {
	Start(ctx context.Context) error
	Stop()
}

// Handle owns the running execution side.
type Handle struct {
	runs     script.RunStore
	worker   *worker
	listener listenerControl
}

// New composes the execution side. It returns nil when there is nowhere to keep
// runs, because a run queue with no storage is not a degraded feature, it is no
// feature; every method here is nil-safe so the caller wires it unconditionally.
func New(cfg Config) *Handle {
	runs, scripts, versions := cfg.stores()
	if runs == nil {
		return nil
	}
	h := &Handle{runs: runs}
	h.worker = newWorker(workerConfig{
		runs:      runs,
		scripts:   scripts,
		versions:  versions,
		runner:    newRunner(runs, cfg),
		retention: orDefaultRetention(cfg.RunRetention),
	})
	if cfg.DSN != "" {
		h.listener = pglisten.New(cfg.DSN, scriptstore.NotifyChannel, h)
	}
	return h
}

// stores resolves the three stores the execution side reads and writes,
// preferring what the caller supplied and falling back to PostgreSQL over DB.
// All three come from one place so a caller cannot end up with a run queue in
// one store and the scripts it names in another.
func (c Config) stores() (script.RunStore, ScriptReader, VersionReader) {
	runs, scripts, versions := c.Runs, c.Scripts, c.Versions
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
	}
	if runs == nil || scripts == nil || versions == nil {
		return nil, nil, nil
	}
	return runs, scripts, versions
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
	if h == nil {
		return
	}
	h.worker.Notify()
}

// Start launches the run worker and, when configured, the LISTEN adapter that
// wakes it on enqueue. A LISTEN failure degrades to the worker's poll interval
// rather than failing startup: the wakeup is a latency optimization, and the
// poll is what makes the queue correct. Nil-safe.
func (h *Handle) Start(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.worker.Start(ctx)
	if h.listener != nil {
		if err := h.listener.Start(ctx); err != nil {
			slog.Warn("scripts: run queue listener unavailable; falling back to polling", logKeyError, err)
			h.listener = nil
		}
	}
	return nil
}

// Stop terminates the worker and the listener. An abandoned claimed run is
// safe: its lease expires and another replica reclaims it. Nil-safe.
func (h *Handle) Stop(_ context.Context) error {
	if h == nil {
		return nil
	}
	if h.listener != nil {
		h.listener.Stop()
	}
	h.worker.Stop()
	return nil
}
