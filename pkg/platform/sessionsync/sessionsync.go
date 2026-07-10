// Package sessionsync assembles the session / cross-replica-sync layer behind
// one Handle: the externalized session store (memory or postgres), the
// per-session enrichment-dedup cache, the client-facing MCP notification
// broadcaster, and the dedicated cross-replica reload bus (its own broadcaster
// channel plus the publish/subscribe machinery).
//
// Construction takes explicit inputs — a *sql.DB, the resolved session /
// broadcast config values, an optional injected session store, and the reload
// handlers Platform re-materializes local state through — so the subsystem is
// constructible and testable without a Platform. It imports pkg/session,
// pkg/session/postgres, and pkg/middleware, never pkg/platform. The *sql.DB and
// the config values back many other subsystems, so they stay owned by Platform
// and are passed in rather than owned here.
//
// Construction is two-phase: New builds the store + both broadcasters + reload
// bus (owning their cleanup and subscriber goroutines), and StartCache builds
// the enrichment-dedup cache during middleware assembly (gated by config on the
// Platform side). The reload re-materialization handlers stay on Platform (they
// reach into the connection store, toolkit registry, persona registry, and
// API-key store — state this package does not own) and are injected as
// callbacks. Close is the shutdown seam Platform wires into its own lifecycle.
package sessionsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/session"
	sessionpostgres "github.com/txn2/mcp-data-platform/pkg/session/postgres"
)

// storeKindDatabase / storeKindMemory name the two externalized session store
// backends. The empty string selects memory (the zero-config default).
const (
	storeKindDatabase = "database"
	storeKindMemory   = "memory"
)

// Config carries the resolved session / broadcast values the owner needs to
// assemble the layer. Platform resolves defaults (TTL, cleanup interval) before
// passing them so this package stays free of the platform's defaulting rules.
type Config struct {
	// Store selects the session store backend: "database", "memory", or ""
	// (memory). Ignored when an injected store is supplied to New.
	Store string
	// TTL is the resolved session lifetime; CleanupInterval is how often the
	// store's cleanup routine runs. Both are already defaulted by the caller.
	TTL             time.Duration
	CleanupInterval time.Duration
	// DSN is the database connection string; empty disables the postgres
	// broadcasters (memory fan-out only). BroadcastChannel overrides the
	// postgres LISTEN/NOTIFY channel; empty uses the package default.
	DSN              string
	BroadcastChannel string
}

// ReloadHandlers carries the local re-materialization callbacks the reload
// subscriber invokes when a peer replica announces a configuration change. They
// stay on Platform (they reach into Platform-owned state) and are injected here
// so the bus is unit-testable in isolation. Any nil handler means "this
// subsystem does not participate in cross-replica reload"; the event is ignored.
// This is also the reloadBus's handler type, so New passes it straight through.
type ReloadHandlers struct {
	// Connection receives (kind, name, op) where op is the opaque intent string
	// the publisher passed to PublishConnectionReload ("upsert"/"delete", empty
	// for a legacy pre-op event). The bus does not interpret op; the handler
	// does.
	Connection func(kind, name, op string)
	Catalog    func(catalogID string)
	Persona    func()
	APIKey     func()
}

// Handle owns the assembled session / cross-replica-sync layer: the session
// store (and its cleanup goroutine), the enrichment-dedup cache (and its
// cleanup goroutine, started by StartCache), the client-facing broadcaster, and
// the dedicated reload broadcaster + bus (and the subscriber goroutine). The
// read accessors expose the store / broadcaster / cache that Platform surfaces
// through its SessionStore() / Broadcaster() accessors, the HTTP session
// resolver, session-handle minting, and the admin session export/restore; the
// Publish*Reload delegators back the Platform wrappers admin handlers call.
// Close is the shutdown seam Platform wires into its own lifecycle.
type Handle struct {
	store             session.Store
	cache             *middleware.SessionEnrichmentCache
	broadcaster       session.Broadcaster
	reloadBroadcaster session.Broadcaster
	reloadBus         *reloadBus
	reloadCancel      context.CancelFunc

	// forcedStateless records that the database store was selected, so the
	// SDK's built-in session map must be bypassed. Platform reads this via
	// StatelessForced and applies it to its own Server.Streamable config.
	forcedStateless bool
}

// New assembles the session store, both broadcasters, and the reload bus from an
// explicit *sql.DB, the resolved config, an optional injected store, and the
// reload handlers. When injectedStore is non-nil it is used verbatim (the admin
// SessionStore override path) — store selection, the cleanup routine, and
// stateless forcing are all skipped, but the broadcasters and reload bus are
// still wired so Broadcaster() is non-nil and cross-replica reload still works.
//
// It returns an error when Config.Store is "database" but db is nil, or when
// Config.Store is an unknown value; otherwise the returned Handle is non-nil
// and its Broadcaster is guaranteed non-nil (memory fallback).
func New(db *sql.DB, cfg Config, injectedStore session.Store, handlers ReloadHandlers) (*Handle, error) {
	store, forcedStateless, err := buildStore(db, cfg, injectedStore)
	if err != nil {
		return nil, err
	}

	h := &Handle{
		store:           store,
		forcedStateless: forcedStateless,
	}
	h.broadcaster = buildClientBroadcaster(db, cfg)
	h.reloadBroadcaster = buildReloadBroadcaster(db, cfg)
	h.reloadBus = newReloadBus(h.reloadBroadcaster, newReplicaOrigin(), handlers, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	h.reloadCancel = cancel
	go h.reloadBus.run(ctx)

	return h, nil
}

// buildStore selects the session store backend and starts its cleanup routine.
// An injected store is used verbatim (no cleanup routine, no stateless forcing —
// the caller owns that store's lifecycle). Otherwise the database store forces
// stateless mode so the SDK skips its built-in session map.
func buildStore(db *sql.DB, cfg Config, injectedStore session.Store) (session.Store, bool, error) {
	if injectedStore != nil {
		return injectedStore, false, nil
	}

	switch cfg.Store {
	case storeKindDatabase:
		if db == nil {
			return nil, false, fmt.Errorf("sessions.store is \"database\" but no database configured")
		}
		store := sessionpostgres.New(db, sessionpostgres.Config{TTL: cfg.TTL})
		store.StartCleanupRoutine(cfg.CleanupInterval)
		slog.Info("session store: database (stateless mode enabled)",
			"ttl", cfg.TTL, "cleanup_interval", cfg.CleanupInterval)
		return store, true, nil
	case storeKindMemory, "":
		store := session.NewMemoryStore(cfg.TTL)
		store.StartCleanupRoutine(cfg.CleanupInterval)
		slog.Info("session store: memory",
			"ttl", cfg.TTL, "cleanup_interval", cfg.CleanupInterval)
		return store, false, nil
	default:
		return nil, false, fmt.Errorf("unknown session store: %q", cfg.Store)
	}
}

// buildClientBroadcaster picks the client-facing broadcaster: postgres
// LISTEN/NOTIFY when a database is configured, in-memory otherwise. A postgres
// setup failure falls back to memory rather than failing startup —
// tools/list_changed propagation is a UX feature, not a correctness
// requirement — with a distinguishable log line.
func buildClientBroadcaster(db *sql.DB, cfg Config) session.Broadcaster {
	wantPostgres := db != nil && cfg.DSN != ""
	if wantPostgres {
		// Channel override lets operators isolate deployments that
		// share a postgres instance — without it, both deployments
		// would LISTEN on the same channel and cross-broadcast
		// tools/list_changed to each other's downstream agents.
		channel := cfg.BroadcastChannel
		if channel == "" {
			channel = sessionpostgres.DefaultNotifyChannel
			// Multiple deployments sharing a postgres instance with
			// the default channel will cross-broadcast
			// tools/list_changed events to each other's downstream
			// agents. The doc tells operators to set the field per
			// deployment when sharing a DB, but the most common
			// operator error is forgetting to set it — surface a
			// warning here so the misconfiguration is visible at
			// startup rather than as a "tool list keeps changing
			// on its own" mystery in production.
			slog.Warn("broadcaster: using default channel; if multiple deployments share this postgres instance, set sessions.broadcast_channel per deployment to avoid cross-deployment fan-out",
				"channel", channel)
		}
		b, err := sessionpostgres.NewBroadcaster(cfg.DSN, db, channel, slog.Default())
		if err == nil {
			slog.Info("broadcaster: postgres LISTEN/NOTIFY",
				"channel", channel)
			return b
		}
		// Fallback path: the operator configured postgres but
		// NewBroadcaster failed (e.g., role lacks LISTEN privilege).
		// Don't fail startup — tools/list_changed propagation is a
		// UX feature, not a correctness requirement — but log
		// distinguishably from the intentional single-replica path
		// so log-grep operators can spot a degraded multi-replica
		// deployment.
		slog.Warn("broadcaster: postgres init failed — falling back to memory",
			"error", err)
	}
	b := session.NewMemoryBroadcaster(slog.Default())
	if wantPostgres {
		slog.Info("broadcaster: memory (postgres unavailable, cross-replica fan-out disabled)")
	} else {
		slog.Info("broadcaster: memory (single-replica)")
	}
	return b
}

// buildReloadBroadcaster picks the dedicated cross-replica reload channel. It
// uses a separate postgres LISTEN/NOTIFY channel from the client-facing
// broadcaster (the configured channel plus a "_reload" suffix) so internal
// control-plane events are never written to an MCP client's SSE stream. On a
// single-replica or db-less deployment it falls back to an in-memory
// broadcaster (a local no-op loop, harmless).
func buildReloadBroadcaster(db *sql.DB, cfg Config) session.Broadcaster {
	if db != nil && cfg.DSN != "" {
		channel := cfg.BroadcastChannel
		if channel == "" {
			channel = sessionpostgres.DefaultNotifyChannel
		}
		reloadChannel := channel + "_reload"
		pb, err := sessionpostgres.NewBroadcaster(cfg.DSN, db, reloadChannel, slog.Default())
		if err == nil {
			slog.Info("reload-bus: postgres LISTEN/NOTIFY", "channel", reloadChannel)
			return pb
		}
		// Degraded: cross-replica reload is disabled, but the platform
		// still runs. Operators on a multi-replica deployment must
		// restart pods after admin config changes until this is fixed.
		slog.Warn("reload-bus: postgres init failed — cross-replica reload disabled, restart pods after admin changes",
			"error", err)
	}
	return session.NewMemoryBroadcaster(slog.Default())
}

// StartCache builds the per-session enrichment-dedup cache and starts its
// cleanup goroutine. Callers gate this on config (session_dedup enabled); when
// not called the cache stays nil and SessionCache returns nil — the disabled
// no-op. No-op on a nil Handle or a repeat call, so a second call cannot leak
// the first cache's goroutine. Returns the cache for the caller to wire into
// the enrichment middleware config.
func (h *Handle) StartCache(entryTTL, sessionTimeout time.Duration) *middleware.SessionEnrichmentCache {
	if h == nil {
		return nil
	}
	if h.cache != nil {
		return h.cache
	}
	h.cache = middleware.NewSessionEnrichmentCache(entryTTL, sessionTimeout)
	h.cache.StartCleanup(1 * time.Minute)
	return h.cache
}

// SessionStore returns the externalized session store, or nil on a nil Handle.
func (h *Handle) SessionStore() session.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// Broadcaster returns the client-facing MCP notification broadcaster. Non-nil
// after a successful New (memory fallback guarantees it); nil only on a nil
// Handle.
func (h *Handle) Broadcaster() session.Broadcaster {
	if h == nil {
		return nil
	}
	return h.broadcaster
}

// SessionCache returns the enrichment-dedup cache, or nil on a nil Handle or
// when StartCache was never called (session_dedup disabled).
func (h *Handle) SessionCache() *middleware.SessionEnrichmentCache {
	if h == nil {
		return nil
	}
	return h.cache
}

// StatelessForced reports whether the database store was selected and the SDK's
// built-in session map must therefore be bypassed. Platform applies this to its
// Server.Streamable config after New.
func (h *Handle) StatelessForced() bool {
	return h != nil && h.forcedStateless
}

// PublishConnectionReload announces that the (kind, name) connection changed so
// peers rebuild it. op is an opaque intent string ("upsert"/"delete") the bus
// carries verbatim to the peer handler. No-op on a nil Handle.
func (h *Handle) PublishConnectionReload(ctx context.Context, kind, name, op string) {
	if h == nil {
		return
	}
	h.reloadBus.publishConnection(ctx, kind, name, op)
}

// PublishCatalogReload announces that an API catalog's specs changed so peers
// rebuild every connection referencing it. No-op on a nil Handle.
func (h *Handle) PublishCatalogReload(ctx context.Context, catalogID string) {
	if h == nil {
		return
	}
	h.reloadBus.publishCatalog(ctx, catalogID)
}

// PublishPersonaReload announces that persona definitions changed so peers
// reconcile their persona registry. No-op on a nil Handle.
func (h *Handle) PublishPersonaReload(ctx context.Context) {
	if h == nil {
		return
	}
	h.reloadBus.publishPersona(ctx)
}

// PublishAPIKeyReload announces that API keys changed so peers re-sync their
// in-memory key set. No-op on a nil Handle.
func (h *Handle) PublishAPIKeyReload(ctx context.Context) {
	if h == nil {
		return
	}
	h.reloadBus.publishAPIKey(ctx)
}

// Close tears down the layer in order: stop the enrichment cache, close the
// session store, close the client broadcaster, then cancel the reload subscriber
// and close the reload broadcaster. The broadcasters must close before Platform
// closes its *sql.DB because the postgres broadcasters hold their own dedicated
// LISTEN connections. Returns the joined store + client-broadcaster close errors;
// the reload-broadcaster close error is best-effort (the reload channel is a
// control-plane convenience, not a data path). No-op on a nil Handle.
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	var errs []error
	if h.cache != nil {
		slog.Debug("shutdown: stopping session cache")
		h.cache.Stop()
	}
	if h.store != nil {
		slog.Debug("shutdown: closing session store")
		if err := h.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close session store: %w", err))
		}
	}
	if h.broadcaster != nil {
		slog.Debug("shutdown: closing broadcaster")
		if err := h.broadcaster.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close broadcaster: %w", err))
		}
	}
	slog.Debug("shutdown: stopping reload bus")
	if h.reloadCancel != nil {
		h.reloadCancel()
	}
	if h.reloadBroadcaster != nil {
		_ = h.reloadBroadcaster.Close()
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("sessionsync: close: %w", err)
	}
	return nil
}
