package platform

import (
	"context"
	"errors"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/graphqlwiring"
	"github.com/txn2/mcp-data-platform/pkg/connreconcile"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// slog keys shared by the connection reloaders.
const (
	logKeyKind = "kind"
	logKeyName = "name"
)

// ConnectionReloadOp is the intent behind a connection reload broadcast. It is
// carried through the reload bus so a peer applying a deletion never has to read
// the store: a delete is a one-shot event whose row is already gone, so a
// transient store-read failure on the peer could otherwise leave the connection
// live and callable until an unrelated reload or a restart (issue #885 review).
type ConnectionReloadOp int

const (
	// ReloadUpsert marks a created or updated connection: the peer reads the
	// store for the new config and re-materializes it (keeping the live config
	// in place if the read transiently fails, per #885).
	ReloadUpsert ConnectionReloadOp = iota
	// ReloadDelete marks a deleted connection: the peer removes it from every
	// matching live toolkit without reading the store.
	ReloadDelete
	// ReloadSchema marks a connection whose stored schema changed, by an
	// upload or a re-read on the publishing replica: the peer installs what
	// the schema store holds, without reading the connection store and
	// without touching the endpoint (#1676).
	ReloadSchema
)

// reloadOpNames is each op's spelling on the wire, which a peer's handler
// parses it back from.
var reloadOpNames = map[ConnectionReloadOp]string{ReloadUpsert: "upsert", ReloadDelete: "delete", ReloadSchema: "schema"}

// String renders the op for the reload-bus wire payload and logs.
func (o ConnectionReloadOp) String() string { return reloadOpNames[o] }

// parseConnectionReloadOp maps the wire op back to a ConnectionReloadOp. Any
// value that is not an explicit marker — including the empty string a
// pre-#885 replica publishes during a rolling upgrade — is treated as an
// upsert, so an unrecognized or missing op falls back to the read-and-decide
// path rather than removing a connection.
func parseConnectionReloadOp(s string) ConnectionReloadOp {
	for op, name := range reloadOpNames {
		if name == s {
			return op
		}
	}
	return ReloadUpsert
}

// This file holds the reload re-materialization handlers and the public
// reload-publish surface, both of which stay on Platform: the handlers reach
// into Platform-owned state (connection store, toolkit registry, persona
// registry, API-key store) and the Publish* methods are called by admin
// handlers. The dedicated cross-replica reload BUS (its broadcaster channel and
// the publish/subscribe machinery) lives in internal/platform/sessionsync and is
// reached through the sessions handle; the handlers are injected into it at
// construction (issue #843).

// reloadConnectionLocal re-materializes one connection on this replica in
// response to a peer's reload broadcast. op carries the peer's intent so a
// deletion is applied without a store read.
//
//   - ReloadDelete: remove the connection from every matching toolkit. No store
//     read happens, so a transient store failure can never leave a deleted
//     connection live on this replica. A failed removal is logged at WARN, not
//     ERROR: removing an already-absent connection is not state-corrupting.
//   - ReloadSchema: install the schema the schema store holds on every graphql
//     toolkit holding the connection. The connection's configuration did not
//     change, so nothing is rebuilt and the endpoint is not read.
//   - ReloadUpsert (and any legacy event without an op): read the store and
//     decide. The read outcome drives a three-way branch so a transient read
//     failure never silently drops a live connection (issue #885):
//   - read failed (not a not-found): log at ERROR and leave the live config
//     in place. Removing here would drop a healthy connection over a
//     database blip; a later upsert event re-materializes it.
//   - not found (raced with a concurrent delete): remove it.
//   - present: adopt the stored config, so the changed config takes effect
//     with the state the saving replica stored (#1714).
//
// Neither removal applies to a connection this replica's config file declares:
// an absent row is not evidence it should stop serving, because the file is
// what it runs on (#1400). An upsert that finds a row still applies to one.
func (p *Platform) reloadConnectionLocal(kind, name, op string) {
	rec := connreconcile.New(p.toolkitRegistry)
	switch parseConnectionReloadOp(op) {
	case ReloadDelete:
		p.removeReloadedConnection(rec, kind, name, "reload-bus: failed to remove deleted connection from toolkit")
		return
	case ReloadSchema:
		graphqlwiring.ReloadStoredSchema(context.Background(), p.toolkitRegistry, name)
		return
	case ReloadUpsert:
	}

	inst, err := p.connectionStore.Get(context.Background(), kind, name)
	switch {
	case err != nil && !errors.Is(err, ErrConnectionNotFound):
		slog.Error("reload-bus: failed to read connection from store; keeping live config",
			logKeyKind, kind, logKeyName, name, logKeyError, err)
	case inst == nil:
		p.removeReloadedConnection(rec, kind, name, "reload-bus: failed to remove connection from toolkit")
	default:
		// A failure here leaves a toolkit out of sync with the store, so it is
		// logged at ERROR; the reconciler still updates the other toolkits.
		for _, f := range rec.Adopt(kind, name, inst.Config) {
			slog.Error("reload-bus: failed to reconcile connection onto toolkit",
				logKeyKind, kind, logKeyName, name, "phase", f.Phase.String(), logKeyError, f.Err)
		}
	}
}

// removeReloadedConnection drops a connection the reload bus says is gone from
// this replica's toolkits, unless the config file declares it. A peer that
// predates the delete refusal, or one racing a delete, can still broadcast the
// removal of such a connection; honoring it would take it out of service here
// until a restart put it back, and the file is unaffected by anything the store
// did. A failed removal is logged at WARN, not ERROR: removing an
// already-absent connection is not state-corrupting.
func (p *Platform) removeReloadedConnection(rec *connreconcile.Reconciler, kind, name, msg string) {
	if p.config.DeclaresConnection(kind, name) {
		slog.Info("reload-bus: keeping a connection the configuration file declares",
			logKeyKind, kind, logKeyName, name)
		return
	}
	for _, f := range rec.Remove(kind, name) {
		slog.Warn(msg, logKeyKind, kind, logKeyName, name, logKeyError, f.Err)
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

// reloadPersonaLocal reconciles the persona registry from the store on
// this replica (re-registers/updates DB personas). Used by the reload
// subscriber when a peer changes a persona.
func (p *Platform) reloadPersonaLocal() {
	p.loadDBPersonas()
}

// reloadAPIKeyLocal re-syncs the in-memory DB-loaded API keys from the store
// on this replica, dropping revoked keys. A key a peer wrote is already in
// effect here, because the authenticator confirms database keys against the
// store; this keeps the copy in memory from growing stale between requests.
func (p *Platform) reloadAPIKeyLocal() {
	if p.apiKeyAuth == nil {
		return
	}
	if err := p.apiKeyAuth.SyncHashedKeys(context.Background()); err != nil {
		slog.Warn("reload-bus: failed to list api keys for reload", logKeyError, err)
	}
}

// PublishConnectionReload announces a connection config change to peer
// replicas. op distinguishes an upsert (peers read the store) from a delete
// (peers remove without a store read). Implements admin.ReloadNotifier. Safe
// when the layer is nil.
func (p *Platform) PublishConnectionReload(kind, name string, op ConnectionReloadOp) {
	p.sessions.PublishConnectionReload(context.Background(), kind, name, op.String())
}

// PublishPersonaReload announces a persona change to peer replicas.
func (p *Platform) PublishPersonaReload() {
	p.sessions.PublishPersonaReload(context.Background())
}

// PublishAPIKeyReload announces an API-key change to peer replicas.
func (p *Platform) PublishAPIKeyReload() {
	p.sessions.PublishAPIKeyReload(context.Background())
}

// PublishCatalogReload announces an API-catalog spec change to peer
// replicas. Implements admin.ReloadNotifier. Safe when the layer is nil.
func (p *Platform) PublishCatalogReload(catalogID string) {
	p.sessions.PublishCatalogReload(context.Background(), catalogID)
}
