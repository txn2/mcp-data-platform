package sessionsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/txn2/mcp-data-platform/pkg/session"
)

// Reload event methods. These are internal control-plane signals carried
// on a DEDICATED broadcaster channel, separate from the client-facing
// tools/list_changed fan-out, so they are never written to an MCP
// client's SSE stream. The "platform/reload/" prefix (not
// "notifications/") makes the separation obvious in logs.
const (
	reloadMethodConnection = "platform/reload/connection"
	reloadMethodCatalog    = "platform/reload/catalog" //nolint:gosec // event method name, not a credential
	reloadMethodPersona    = "platform/reload/persona" //nolint:gosec // event method name, not a credential
	reloadMethodAPIKey     = "platform/reload/apikey"  //nolint:gosec // event method name, not a credential
)

// reloadParamOrigin tags each reload event with the publishing replica's
// instance id. The subscriber skips events whose origin matches its own
// id: the replica that handled the admin write has already reloaded its
// in-memory state synchronously, so re-applying its own broadcast would
// be a redundant (though idempotent) rebuild.
const reloadParamOrigin = "origin"

// reloadBus publishes and consumes cross-replica reload events over a
// dedicated broadcaster channel. It is the server-side counterpart to
// the client-facing notification path: when an operator changes
// configuration through the admin API on one replica, that replica
// reloads locally AND publishes a reload event here; every other replica
// receives the event and re-materializes the affected in-memory state.
//
// Without this, a multi-replica deployment serves stale connection,
// catalog, persona, and API-key state on every replica except the one
// that handled the admin request (see issue #501).
type reloadBus struct {
	b        session.Broadcaster
	origin   string
	handlers ReloadHandlers
	logger   *slog.Logger
}

// newReloadBus builds a bus over b. origin is this replica's unique
// instance id (used to skip self-published events). A nil logger falls
// back to slog.Default().
func newReloadBus(b session.Broadcaster, origin string, h ReloadHandlers, logger *slog.Logger) *reloadBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &reloadBus{b: b, origin: origin, handlers: h, logger: logger}
}

// publishConnection announces that the (kind, name) connection changed and
// peers should rebuild it. op is the opaque intent string ("upsert"/"delete")
// carried verbatim to the subscriber so a deletion is applied without a peer
// store read.
func (rb *reloadBus) publishConnection(ctx context.Context, kind, name, op string) {
	rb.publish(ctx, reloadMethodConnection, map[string]any{"kind": kind, "name": name, "op": op})
}

// publishCatalog announces that an API catalog's specs changed and peers
// should rebuild every connection that references it.
func (rb *reloadBus) publishCatalog(ctx context.Context, catalogID string) {
	rb.publish(ctx, reloadMethodCatalog, map[string]any{"catalog_id": catalogID})
}

// publishPersona announces that persona definitions changed and peers
// should reconcile their persona registry from the store.
func (rb *reloadBus) publishPersona(ctx context.Context) {
	rb.publish(ctx, reloadMethodPersona, nil)
}

// publishAPIKey announces that API keys changed and peers should
// re-sync their in-memory key set from the store.
func (rb *reloadBus) publishAPIKey(ctx context.Context) {
	rb.publish(ctx, reloadMethodAPIKey, nil)
}

func (rb *reloadBus) publish(ctx context.Context, method string, params map[string]any) {
	if rb == nil || rb.b == nil {
		return
	}
	if params == nil {
		params = make(map[string]any, 1)
	}
	params[reloadParamOrigin] = rb.origin
	if err := rb.b.Publish(ctx, session.Event{Method: method, Params: params}); err != nil {
		// Best-effort: a failed publish means peers miss this one change
		// until their next restart or a later successful publish. Log so
		// a degraded reload channel is visible rather than silent.
		rb.logger.Warn("reload-bus: publish failed", "method", method, "error", err)
	}
}

// run subscribes to the reload channel and dispatches events until ctx is
// canceled. Intended to be launched in its own goroutine at startup.
func (rb *reloadBus) run(ctx context.Context) {
	if rb == nil || rb.b == nil {
		return
	}
	sub := rb.b.Subscribe(ctx, "reload-bus")
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.Events():
			if !ok {
				return
			}
			rb.dispatch(ev)
		}
	}
}

// dispatch routes one reload event to the matching local handler. Events
// published by this same replica are skipped (the handler already ran
// synchronously on the write path).
func (rb *reloadBus) dispatch(ev session.Event) {
	if origin, _ := ev.Params[reloadParamOrigin].(string); origin == rb.origin {
		return
	}
	switch ev.Method {
	case reloadMethodConnection:
		rb.dispatchConnection(ev)
	case reloadMethodCatalog:
		rb.dispatchCatalog(ev)
	case reloadMethodPersona:
		if rb.handlers.Persona != nil {
			rb.logger.Info("reload-bus: reloading personas from peer")
			rb.handlers.Persona()
		}
	case reloadMethodAPIKey:
		if rb.handlers.APIKey != nil {
			rb.logger.Info("reload-bus: reloading API keys from peer")
			rb.handlers.APIKey()
		}
	default:
		// Unknown method on the dedicated reload channel: ignore. This is
		// the forward-compat path for a newer replica publishing a reload
		// kind an older replica does not understand yet.
	}
}

// dispatchConnection applies a peer's connection reload (kind/name/op). op is
// absent for events from a pre-op replica during a rolling upgrade; it is
// passed through empty and the handler falls back to a store read.
func (rb *reloadBus) dispatchConnection(ev session.Event) {
	kind, _ := ev.Params["kind"].(string)
	name, _ := ev.Params["name"].(string)
	op, _ := ev.Params["op"].(string)
	if rb.handlers.Connection != nil && kind != "" && name != "" {
		rb.logger.Info("reload-bus: reloading connection from peer", "kind", kind, "name", name, "op", op)
		rb.handlers.Connection(kind, name, op)
	}
}

// dispatchCatalog applies a peer's catalog reload (catalog_id).
func (rb *reloadBus) dispatchCatalog(ev session.Event) {
	id, _ := ev.Params["catalog_id"].(string)
	if rb.handlers.Catalog != nil && id != "" {
		rb.logger.Info("reload-bus: reloading catalog connections from peer", "catalog_id", id)
		rb.handlers.Catalog(id)
	}
}

// newReplicaOrigin returns a stable-per-process identifier used to tag
// reload events so a replica skips its own broadcasts. Hostname plus a
// random suffix keeps it unique even when two replicas share a hostname
// (unlikely under k8s, but cheap insurance).
func newReplicaOrigin() string {
	host, _ := os.Hostname()
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	if host == "" {
		host = "replica"
	}
	return host + "-" + hex.EncodeToString(buf)
}
