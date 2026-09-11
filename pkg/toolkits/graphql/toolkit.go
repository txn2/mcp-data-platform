package graphql

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Tool names this toolkit registers. Exported so audit code, persona
// configuration and tests reference the same literals as the
// registration site.
const (
	// ToolDiscover finds an operation and describes it.
	ToolDiscover = "graphql_discover"
	// ToolQuery executes one document.
	ToolQuery = "graphql_query"
	// ToolExport streams one document's result into an asset.
	ToolExport = "graphql_export"
)

// Errors the connection registry answers with.
var (
	// ErrConnectionExists is returned when AddConnection is called with
	// a name already registered.
	ErrConnectionExists = errors.New("graphql: connection already exists")
	// ErrConnectionNotFound is returned when an operation is requested
	// against a connection that has not been registered.
	ErrConnectionNotFound = errors.New("graphql: connection not found")
)

const (
	logKeyConnection = "connection"
	logKeyError      = "error"
)

// Toolkit is the graphql toolkit. One Toolkit manages every registered
// GraphQL connection, each addressing a different endpoint. Connections
// are added at startup from the platform's merged YAML+DB config, or at
// runtime by the admin REST handler when an operator saves one through
// the portal.
type Toolkit struct {
	name        string
	defaultName string

	mu             sync.RWMutex
	connections    map[string]*conn
	routePolicy    RoutePolicy
	connOAuthStore connoauth.Store
	authEvents     *authevents.Writer
	schemaStore    SchemaStore
	vectorReader   VectorReader
	embedder       embedding.Provider
	metrics        *observability.Metrics
	memBudget      *membudget.Budget
	exportDeps     *ExportDeps
}

// conn is the materialized state of one registered connection: its
// parsed config, the Authenticator implementing its auth mode, a
// per-connection HTTP client, and the schema it was last read with
// together with the operation index derived from it.
type conn struct {
	cfg    Config
	auth   Authenticator
	client *http.Client

	// schemaMu guards the schema and everything derived from it, which
	// a schema refresh replaces wholesale while calls are in flight.
	schemaMu   sync.RWMutex
	schema     *gqlschema.Schema
	operations []gqlschema.Operation
	fetchedAt  time.Time
	source     string
	// schemaErr is why this connection has no usable schema, in the
	// operator's words. A connection whose endpoint disables
	// introspection reports it here rather than presenting an empty
	// operation index that reads as a schema with nothing in it.
	schemaErr string
	// vectors are the persisted operation embeddings for the current
	// schema version, keyed by operation id. Empty until the index-jobs
	// consumer has embedded this schema.
	vectors map[string][]float32
}

// RoutePolicy gates a GraphQL call by (connection, method, path) on top
// of the platform's tool and connection authorization. The method is
// the operation kind (QUERY or MUTATION) and the path is the dotted
// operation id with dots as slashes, which puts a GraphQL operation
// into the same space a persona's api_routes rules already evaluate.
//
// The template argument is always equal to path here: a GraphQL
// operation id carries no parameters to substitute, so the two forms an
// HTTP route has collapse to one.
type RoutePolicy interface {
	Allow(ctx context.Context, connection, method, path, template string) (allowed bool, reason string)
}

// NewMulti creates the toolkit from every configured instance.
// Per-connection materialization failures (an authenticator that cannot
// be built) are logged and skipped so one bad connection cannot block
// platform startup. Endpoint failures happen at call time and are
// surfaced through the tool's response, not at startup.
func NewMulti(cfg MultiConfig) *Toolkit {
	t := &Toolkit{
		name:        cfg.DefaultName,
		defaultName: cfg.DefaultName,
		connections: make(map[string]*conn, len(cfg.Instances)),
	}
	names := make([]string, 0, len(cfg.Instances))
	for name := range cfg.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := t.addParsedConnection(name, cfg.Instances[name]); err != nil {
			slog.Warn("graphql: skipping connection",
				logKeyConnection, logsan.SanitizeForLog(name), logKeyError, err)
		}
	}
	return t
}

// addParsedConnection materializes one already-parsed connection.
// The schema is not read here: registration must not block on an
// endpoint, and the platform calls RefreshSchema (or the stored schema
// is loaded) once wiring is complete.
func (t *Toolkit) addParsedConnection(name string, cfg Config) error {
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		return err
	}
	c := &conn{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.connections[name]; exists {
		return fmt.Errorf("graphql: %s: %w", name, ErrConnectionExists)
	}
	upstreamauth.SetConnOAuthStore(auth, t.connOAuthStore)
	upstreamauth.SetAuthEvents(auth, t.authEvents)
	t.connections[name] = c
	return nil
}

// Kind returns the connection-instance kind discriminator.
func (*Toolkit) Kind() string { return Kind }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the default connection name for audit logging.
func (t *Toolkit) Connection() string { return t.defaultName }

// Tools returns the tool names this toolkit registers. graphql_export
// is present only when the platform wired the export dependencies, so
// the list matches what a client can actually call.
func (t *Toolkit) Tools() []string {
	tools := []string{ToolDiscover, ToolQuery}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.exportDeps != nil {
		tools = append(tools, ToolExport)
	}
	return tools
}

// SetSemanticProvider satisfies registry.Toolkit. A GraphQL endpoint
// carries its own schema and is not a table catalog, so there is
// nothing for the semantic layer to enrich here.
func (*Toolkit) SetSemanticProvider(semantic.Provider) {}

// SetQueryProvider satisfies registry.Toolkit. See
// SetSemanticProvider.
func (*Toolkit) SetQueryProvider(query.Provider) {}

// Close releases every connection's idle transport.
func (t *Toolkit) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.connections {
		if c.client != nil {
			c.client.CloseIdleConnections()
		}
	}
	return nil
}

// SetRoutePolicy wires the per-operation authorization check.
func (t *Toolkit) SetRoutePolicy(p RoutePolicy) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routePolicy = p
}

// SetConnOAuthStore wires the unified OAuth token store, required for
// the authorization_code grant, and re-threads it through every
// already-materialized authenticator.
func (t *Toolkit) SetConnOAuthStore(s connoauth.Store) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connOAuthStore = s
	for _, c := range t.connections {
		upstreamauth.SetConnOAuthStore(c.auth, s)
	}
}

// ConnOAuthStore returns the wired OAuth token store, or nil.
func (t *Toolkit) ConnOAuthStore() connoauth.Store {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connOAuthStore
}

// SetAuthEvents wires the audit-event writer into the toolkit and into
// every already-materialized authenticator, so an outbound token
// refresh emits its lifecycle event.
func (t *Toolkit) SetAuthEvents(w *authevents.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.authEvents = w
	for _, c := range t.connections {
		upstreamauth.SetAuthEvents(c.auth, w)
	}
}

// SetSchemaStore wires the store a connection's schema is kept in
// between restarts. Passing nil leaves schemas in memory only.
func (t *Toolkit) SetSchemaStore(s SchemaStore) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.schemaStore = s
}

// SetVectorReader wires the reader of persisted operation embeddings.
func (t *Toolkit) SetVectorReader(r VectorReader) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.vectorReader = r
}

// SetEmbeddingProvider wires the provider that embeds a caller's query
// for semantic and hybrid ranking. Operation vectors are never computed
// here: they are written by the platform's index-jobs consumer and read
// through the VectorReader.
func (t *Toolkit) SetEmbeddingProvider(p embedding.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.embedder = p
}

// SetMetrics wires the observability recorder. Every send reads it at
// call time, so a connection registered before metrics were enabled
// records from the first call after (#1678).
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics = m
}

// SetMemBudget wires the shared in-flight memory budget the buffered
// tools reserve against before allocating a response buffer. Passing
// nil leaves the buffered path bounded only by the per-connection read
// cap.
func (t *Toolkit) SetMemBudget(b *membudget.Budget) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.memBudget = b
}

// AddConnection registers a connection at runtime from the generic
// config map the admin API stores, and reads its schema. Satisfies
// toolkit.ConnectionManager.
//
// The read goes to the endpoint first and the store second: a connection
// created here is new to this process, and what the store holds for it
// (a schema another replica read or was handed) is what serves it when
// the endpoint will not (#1676).
func (t *Toolkit) AddConnection(name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	cfg.ConnectionName = name
	if err := t.addParsedConnection(name, cfg); err != nil {
		return err
	}
	t.readOrLoadStored(context.Background(), name)
	return nil
}

// UpdateConnection replaces a held connection's configuration and reads
// its schema again, keeping the schema it holds when the read fails.
// Satisfies toolkit.ConnectionUpdater, which is what makes a
// configuration save a change rather than a deletion followed by a
// registration: RemoveConnection drops the stored schema, and a save
// that went through it lost every schema an operator had supplied
// (#1676). A configuration that does not parse is refused with the
// connection left as it was.
func (t *Toolkit) UpdateConnection(name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	cfg.ConnectionName = name
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		return err
	}
	existing, _, ok := t.lookup(name)
	if !ok {
		return notFound(name)
	}
	ctx := context.Background()
	c := &conn{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}
	t.carrySchema(ctx, existing, c)
	t.mu.Lock()
	t.connections[name] = c
	t.mu.Unlock()
	if existing.client != nil {
		existing.client.CloseIdleConnections()
	}
	t.readOrLoadStored(ctx, name)
	return nil
}

// carrySchema installs the schema one connection holds on its
// replacement, with the operation index rebuilt under the replacement's
// configuration (namespace_depth may have changed). The replacement is
// complete before it is swapped in, so a save is never a moment with no
// schema in which a call would be refused, and a deployment with no
// store keeps its schema through a save whose re-read fails.
func (t *Toolkit) carrySchema(ctx context.Context, from, to *conn) {
	from.schemaMu.RLock()
	v := schemaVersion{schema: from.schema, source: from.source, fetchedAt: from.fetchedAt, schemaErr: from.schemaErr}
	from.schemaMu.RUnlock()
	if v.schema == nil {
		return
	}
	t.install(ctx, to, v)
}

// RemoveConnection drops a connection and closes its idle transports.
// It is the deletion: the stored schema is dropped with the connection,
// so one that is gone does not leave an operation index behind for a
// later connection of the same name to inherit. A configuration change
// arrives through UpdateConnection instead.
func (t *Toolkit) RemoveConnection(name string) error {
	t.mu.Lock()
	c, ok := t.connections[name]
	store := t.schemaStore
	if ok {
		delete(t.connections, name)
	}
	t.mu.Unlock()
	if !ok {
		return notFound(name)
	}
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	if store != nil {
		if err := store.DeleteSchema(context.Background(), c.cfg.ConnectionName); err != nil {
			slog.Warn("graphql: dropping stored schema failed",
				logKeyConnection, logsan.SanitizeForLog(name), logKeyError, err)
		}
	}
	return nil
}

// HasConnection reports whether a connection is registered.
func (t *Toolkit) HasConnection(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.connections[name]
	return ok
}

// ListConnections enumerates the registered connections for
// list_connections and the admin UI. OperationCount is how many
// operations the current schema exposes, so an operator can see at a
// glance whether a connection's schema was read.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	t.mu.RLock()
	names := make([]string, 0, len(t.connections))
	conns := make(map[string]*conn, len(t.connections))
	for name, c := range t.connections {
		names = append(names, name)
		conns[name] = c
	}
	def := t.defaultName
	t.mu.RUnlock()
	sort.Strings(names)
	out := make([]toolkit.ConnectionDetail, 0, len(names))
	for _, name := range names {
		c := conns[name]
		c.schemaMu.RLock()
		count := len(c.operations)
		c.schemaMu.RUnlock()
		out = append(out, toolkit.ConnectionDetail{
			Name:           name,
			Description:    connectionDescription(c.cfg),
			IsDefault:      name == def,
			OperationCount: count,
		})
	}
	return out
}

// connectionDescription is what an operator sees under a connection's
// name: their own description, or the endpoint it reaches when they
// wrote none.
func connectionDescription(cfg Config) string {
	if cfg.Description != "" {
		return cfg.Description
	}
	return cfg.EndpointURL
}

// connectionNames returns every registered connection name, sorted.
func (t *Toolkit) connectionNames() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.connections))
	for name := range t.connections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookup returns a registered connection and the route policy in force.
func (t *Toolkit) lookup(name string) (*conn, RoutePolicy, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.connections[name]
	return c, t.routePolicy, ok
}

// RegisterTools registers this toolkit's tools with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, discoverTool(), t.handleDiscover)
	mcp.AddTool(s, queryTool(), t.handleQuery)
	t.registerExportTool(s)
}
