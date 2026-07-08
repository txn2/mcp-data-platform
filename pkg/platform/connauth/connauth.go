// Package connauth assembles the connection-OAuth token lifecycle behind one
// Handle: the unified connection_oauth_tokens store (pkg/connoauth), the durable
// connection_auth_events store together with its nil-safe writer and daily
// 90-day prune routine (pkg/authevents), and the background token-refresh loop.
//
// Construction is two-phase. New takes explicit inputs (a *sql.DB and the
// platform's *fieldcrypt.RestFieldEncryptor) and returns the assembled store +
// writer + auth-event store, starting the prune routine. StartRefresher builds
// and starts the refresh loop from an injected ConfigResolver and AdvisoryLocker:
// the resolver is only constructible after the per-kind OAuth handlers are wired,
// and the caller owns the single- vs multi-replica locker choice, so this package
// stays free of *sql.DB / replica config for the refresher and never imports
// pkg/platform.
package connauth

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// authEventsRetention is the retention window for connection_auth_events. 90
// days matches the contract in #395: operators can always answer "what happened
// to this connection's tokens in the last quarter" from the History panel.
const authEventsRetention = 90 * 24 * time.Hour

// pruneInterval is the cadence of the auth-event prune routine. Daily is plenty
// for a table that grows at most a few events per connection per day; the first
// prune fires after one interval so a freshly-started replica does not
// immediately churn the DB.
const pruneInterval = 24 * time.Hour

// Handle owns the connection-OAuth token lifecycle and the goroutines behind it
// (the auth-event prune routine, started by New, and the background refresh
// loop, started by StartRefresher). The read accessors expose the token store,
// auth-event store, and nil-safe writer that the platform's read paths and the
// gateway / api-gateway toolkit wiring consume; Stop and Close are the shutdown
// seam the platform wires into its own lifecycle.
type Handle struct {
	store      connoauth.Store
	authEvents *authevents.PostgresStore
	authWriter *authevents.Writer
	refresher  *connoauth.Refresher
}

// New assembles the token store, the durable auth-event store and its nil-safe
// writer, and starts the daily 90-day prune routine. It returns nil when db is
// nil: the whole subsystem is a no-op without a database, matching the platform
// precondition (the platform falls back to the legacy per-kind in-memory stores
// in that case). enc may be nil (at-rest encryption disabled, dev-only) — it is
// passed through only when non-nil so a typed-nil interface never reaches the
// store's encryptor.
func New(db *sql.DB, enc *fieldcrypt.RestFieldEncryptor) *Handle {
	if db == nil {
		return nil
	}
	// Guard the typed-nil-in-interface trap: connoauth.NewPostgresStore checks
	// its FieldEncryptor argument against nil to select the noop fallback, so a
	// nil *RestFieldEncryptor must arrive as an untyped nil, not an interface
	// wrapping a nil pointer.
	var fe connoauth.FieldEncryptor
	if enc != nil {
		fe = enc
	}
	authStore := authevents.NewPostgresStore(db)
	authStore.StartPruneRoutine(pruneInterval, authEventsRetention)
	return &Handle{
		store:      connoauth.NewPostgresStore(db, fe),
		authEvents: authStore,
		authWriter: authevents.NewWriter(authStore, nil),
	}
}

// StartRefresher builds and starts the background token-refresh loop. The caller
// supplies the ConfigResolver (which depends on the per-kind OAuth handlers
// wired after the store exists) and the AdvisoryLocker it has selected
// (NoopLocker single-replica, PostgresLocker multi-replica), so this package
// stays free of the replica-mode decision. No-op on a nil Handle, a nil
// resolver, or a repeat call — the refresher is built once, so a second call
// cannot leak the first loop's goroutine.
func (h *Handle) StartRefresher(resolver connoauth.ConfigResolver, locker connoauth.AdvisoryLocker) {
	if h == nil || h.store == nil || resolver == nil {
		return
	}
	if h.refresher != nil {
		return
	}
	h.refresher = connoauth.NewRefresher(
		h.store, resolver, h.authWriter, locker,
		connoauth.RefresherConfig{},
	)
	h.refresher.Start()
}

// Store returns the unified OAuth-token store backing connection_oauth_tokens,
// or nil on a nil Handle (no database configured). Read by the admin unified
// OAuth handler and, via toolkit OAuthKindHandlers, by per-kind Authenticators.
func (h *Handle) Store() connoauth.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// AuthEventStore returns the read-side handle for connection lifecycle events
// (the admin OAuth History panel), or nil on a nil Handle. Returned as the
// interface so callers stay off the concrete type.
func (h *Handle) AuthEventStore() authevents.Store {
	if h == nil || h.authEvents == nil {
		return nil
	}
	return h.authEvents
}

// AuthEventWriter returns the writer wrapping the auth-event store. nil-safe by
// contract: the Writer's methods short-circuit on a nil receiver, so every call
// site can pass the result straight to a component without a nil-check.
func (h *Handle) AuthEventWriter() *authevents.Writer {
	if h == nil {
		return nil
	}
	return h.authWriter
}

// Stop stops the background refresh loop, waiting up to ctx's deadline for an
// in-flight refresh to settle before returning. No-op on a nil Handle or when
// StartRefresher was never called. The caller owns the deadline (the platform
// bounds it to fit the K8s termination grace period).
func (h *Handle) Stop(ctx context.Context) error {
	if h == nil || h.refresher == nil {
		return nil
	}
	slog.Debug("connauth: stopping refresher")
	if err := h.refresher.Stop(ctx); err != nil {
		return fmt.Errorf("connauth: stop refresher: %w", err)
	}
	return nil
}

// Close stops the auth-event store's daily prune goroutine and waits for it.
// No-op on a nil Handle or when no auth-event store was wired. Idempotent: the
// underlying store's Close no-ops after the first call.
func (h *Handle) Close() error {
	if h == nil || h.authEvents == nil {
		return nil
	}
	if err := h.authEvents.Close(); err != nil {
		return fmt.Errorf("connauth: close auth-event store: %w", err)
	}
	return nil
}
