package platform

import (
	"context"
	"errors"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/graphqlwiring"
	"github.com/txn2/mcp-data-platform/internal/platform/storeresync"
	"github.com/txn2/mcp-data-platform/internal/platform/utilconn"
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

// The reload handlers here read Platform-owned state and apply it through
// internal/platform/storeresync; the bus itself is internal/platform/sessionsync,
// which the handlers are injected into (#843).

// reloadConnectionLocal applies a peer's reload broadcast to one connection
// on this replica. op carries the peer's intent: a delete is applied without a
// store read (storeresync.Removed), a schema change installs what the schema
// store holds without rebuilding anything, and an upsert, or a legacy event
// without an op, reads the store and decides (storeresync.Upserted, #885).
// Neither removal applies to a connection this replica's file declares (#1400).
func (p *Platform) reloadConnectionLocal(kind, name, op string) {
	switch parseConnectionReloadOp(op) {
	case ReloadDelete:
		storeresync.Removed(p.toolkitRegistry, p.config, kind, name, "reload-bus: failed to remove deleted connection from toolkit")
		return
	case ReloadSchema:
		graphqlwiring.ReloadStoredSchema(context.Background(), p.toolkitRegistry, name)
		return
	case ReloadUpsert:
	}
	inst, err := p.connectionStore.Get(context.Background(), kind, name)
	if errors.Is(err, ErrConnectionNotFound) {
		inst, err = nil, nil
	}
	read := storeresync.Read{Found: inst != nil, Err: err}
	if inst != nil {
		read.Config = inst.Config
	}
	storeresync.Upserted(p.toolkitRegistry, p.config, kind, name, read)
}

// reloadCatalogLocal rebuilds every connection mounting the catalog here.
func (p *Platform) reloadCatalogLocal(catalogID string) {
	storeresync.Catalog(p.toolkitRegistry.All(), catalogID)
}

// reloadPersonaLocal reconciles the persona registry with the store on this
// replica. Used by the reload subscriber when a peer changes a persona.
func (p *Platform) reloadPersonaLocal() {
	p.loadDBPersonas()
}

// resyncFromStore re-reads what the reload bus carries, as a restart would,
// when its LISTEN connection comes back (#1902). A free function, for the
// god-object budget.
func resyncFromStore(p *Platform) {
	defer p.reloadAPIKeyLocal()
	defer p.reloadPersonaLocal()
	if p.connectionStore == nil || !p.connectionStore.Persistent() {
		return
	}
	instances, err := p.connectionStore.List(context.Background())
	if err != nil {
		slog.Error("reload-bus: failed to list connections for resync; keeping live config", logKeyError, err)
		return
	}
	stored := make([]storeresync.StoredConnection, 0, len(instances))
	for _, inst := range instances {
		stored = append(stored, storeresync.StoredConnection{Kind: inst.Kind, Name: inst.Name, Config: inst.Config})
	}
	storeresync.Connections(stored, p.toolkitRegistry, p.config, platformRegisteredConnection)
}

// platformRegisteredConnection reports a connection the platform registers.
func platformRegisteredConnection(kind, name string) bool {
	return kind == apigatewaykit.Kind && (name == utilconn.ConnectionName || name == adminSelfConnectionName)
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
