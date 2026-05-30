package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"

	"github.com/txn2/mcp-data-platform/pkg/session"
	sessionpostgres "github.com/txn2/mcp-data-platform/pkg/session/postgres"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// Reload event methods. These are internal control-plane signals carried
// on a DEDICATED broadcaster channel, separate from the client-facing
// tools/list_changed fan-out, so they are never written to an MCP
// client's SSE stream. The "platform/reload/" prefix (not
// "notifications/") makes the separation obvious in logs.
// Connection and catalog are wired in this change (the demonstrated
// #501 bug); persona and API-key reloaders are tracked follow-ups that
// plug into this same bus.
const (
	reloadMethodConnection = "platform/reload/connection"
	reloadMethodCatalog    = "platform/reload/catalog" //nolint:gosec // event method name, not a credential
)

// reloadParamOrigin tags each reload event with the publishing replica's
// instance id. The subscriber skips events whose origin matches its own
// id: the replica that handled the admin write has already reloaded its
// in-memory state synchronously, so re-applying its own broadcast would
// be a redundant (though idempotent) rebuild.
const reloadParamOrigin = "origin"

// reloadHandlers carries the local re-materialization callbacks the
// subscriber invokes when a peer replica announces a configuration
// change. Any nil handler is treated as "this subsystem does not
// participate in cross-replica reload yet" and the corresponding event
// is ignored. Keeping the callbacks injected (rather than reaching into
// Platform here) keeps the bus unit-testable in isolation.
type reloadHandlers struct {
	connection func(kind, name string)
	catalog    func(catalogID string)
}

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
	handlers reloadHandlers
	logger   *slog.Logger
}

// newReloadBus builds a bus over b. origin is this replica's unique
// instance id (used to skip self-published events). A nil logger falls
// back to slog.Default().
func newReloadBus(b session.Broadcaster, origin string, h reloadHandlers, logger *slog.Logger) *reloadBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &reloadBus{b: b, origin: origin, handlers: h, logger: logger}
}

// publishConnection announces that the (kind, name) connection's stored
// config changed and peers should rebuild it.
func (rb *reloadBus) publishConnection(ctx context.Context, kind, name string) {
	rb.publish(ctx, reloadMethodConnection, map[string]any{"kind": kind, "name": name})
}

// publishCatalog announces that an API catalog's specs changed and peers
// should rebuild every connection that references it.
func (rb *reloadBus) publishCatalog(ctx context.Context, catalogID string) {
	rb.publish(ctx, reloadMethodCatalog, map[string]any{"catalog_id": catalogID})
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
		kind, _ := ev.Params["kind"].(string)
		name, _ := ev.Params["name"].(string)
		if rb.handlers.connection != nil && kind != "" && name != "" {
			rb.logger.Info("reload-bus: reloading connection from peer", "kind", kind, "name", name)
			rb.handlers.connection(kind, name)
		}
	case reloadMethodCatalog:
		id, _ := ev.Params["catalog_id"].(string)
		if rb.handlers.catalog != nil && id != "" {
			rb.logger.Info("reload-bus: reloading catalog connections from peer", "catalog_id", id)
			rb.handlers.catalog(id)
		}
	default:
		// Unknown method on the dedicated reload channel: ignore. This is
		// the forward-compat path for a newer replica publishing a reload
		// kind an older replica does not understand yet.
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

// initReloadBus builds the dedicated cross-replica reload channel and
// starts the subscriber. It uses a separate postgres LISTEN/NOTIFY
// channel from the client-facing broadcaster (the configured channel
// plus a "_reload" suffix) so internal control-plane events are never
// written to an MCP client's SSE stream. On a single-replica or
// db-less deployment it falls back to an in-memory broadcaster, where
// publish/subscribe is a local no-op loop (harmless). Called at the end
// of initBroadcaster so it shares the broadcaster lifecycle.
func (p *Platform) initReloadBus() {
	var b session.Broadcaster
	if p.db != nil && p.config.Database.DSN != "" {
		channel := p.config.Sessions.BroadcastChannel
		if channel == "" {
			channel = sessionpostgres.DefaultNotifyChannel
		}
		reloadChannel := channel + "_reload"
		pb, err := sessionpostgres.NewBroadcaster(p.config.Database.DSN, p.db, reloadChannel, slog.Default())
		if err == nil {
			b = pb
			slog.Info("reload-bus: postgres LISTEN/NOTIFY", "channel", reloadChannel)
		} else {
			// Degraded: cross-replica reload is disabled, but the platform
			// still runs. Operators on a multi-replica deployment must
			// restart pods after admin config changes until this is fixed.
			slog.Warn("reload-bus: postgres init failed — cross-replica reload disabled, restart pods after admin changes",
				"error", err)
		}
	}
	if b == nil {
		b = session.NewMemoryBroadcaster(slog.Default())
	}
	p.reloadBroadcaster = b
	p.reloadBus = newReloadBus(b, newReplicaOrigin(), reloadHandlers{
		connection: p.reloadConnectionLocal,
		catalog:    p.reloadCatalogLocal,
		// persona and apiKey reloaders are tracked follow-ups on this bus
		// (issue #501); leaving them nil means those events are ignored
		// rather than mis-handled.
	}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	p.reloadCancel = cancel
	go p.reloadBus.run(ctx)
}

// stopReloadBus cancels the subscriber and closes the reload broadcaster.
func (p *Platform) stopReloadBus() {
	if p.reloadCancel != nil {
		p.reloadCancel()
	}
	if p.reloadBroadcaster != nil {
		_ = p.reloadBroadcaster.Close()
	}
}

// reloadConnectionLocal re-materializes one connection on this replica
// from the connection store. Used both by the reload subscriber (peer
// announced a change) and indirectly mirrors the admin hot-reload path.
func (p *Platform) reloadConnectionLocal(kind, name string) {
	inst, err := p.connectionStore.Get(context.Background(), kind, name)
	for _, tk := range p.toolkitRegistry.All() {
		if tk.Kind() != kind {
			continue
		}
		cm, ok := tk.(toolkit.ConnectionManager)
		if !ok {
			continue
		}
		_ = cm.RemoveConnection(name)
		if err == nil && inst != nil {
			_ = cm.AddConnection(name, inst.Config)
		}
	}
}

// reloadCatalogLocal rebuilds every api-gateway connection that mounts
// the given catalog on this replica.
func (p *Platform) reloadCatalogLocal(catalogID string) {
	for _, tk := range p.toolkitRegistry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.ReloadConnectionsByCatalog(catalogID)
		}
	}
}

// PublishConnectionReload announces a connection config change to peer
// replicas. Implements admin.ReloadNotifier. Safe when the bus is nil.
func (p *Platform) PublishConnectionReload(kind, name string) {
	if p.reloadBus != nil {
		p.reloadBus.publishConnection(context.Background(), kind, name)
	}
}

// PublishCatalogReload announces an API-catalog spec change to peer
// replicas. Implements admin.ReloadNotifier. Safe when the bus is nil.
func (p *Platform) PublishCatalogReload(catalogID string) {
	if p.reloadBus != nil {
		p.reloadBus.publishCatalog(context.Background(), catalogID)
	}
}
