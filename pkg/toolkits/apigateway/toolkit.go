package apigateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// ErrConnectionExists is returned when AddConnection is called with a
// name already registered in the toolkit.
var ErrConnectionExists = errors.New("apigateway: connection already exists")

// ErrConnectionNotFound is returned when an operation is requested
// against a connection that has not been registered.
var ErrConnectionNotFound = errors.New("apigateway: connection not found")

const (
	// ToolInvokeEndpoint is the MCP tool name for the invoke
	// operation. Exported so audit code and tests reference the same
	// literal as the registration site.
	ToolInvokeEndpoint = "api_invoke_endpoint"

	// ToolListEndpoints names the tool that returns OperationSummary
	// candidates from a connection's parsed OpenAPI spec. Companion
	// to ToolInvokeEndpoint: the model uses list to discover what's
	// available, then invoke to call it.
	ToolListEndpoints = "api_list_endpoints"

	logKeyConnection = "connection"
	logKeyError      = "error"
	logKeyCatalogID  = "catalog_id"
)

// Toolkit is the api-gateway toolkit. A single Toolkit manages
// multiple named connections, each addressing a different upstream
// HTTP API. Connections are added either at startup (from the
// platform's merged YAML+DB config) or at runtime via AddConnection
// (used by the admin REST handler when an operator saves a new
// connection through the portal).
type Toolkit struct {
	name        string
	defaultName string

	mu             sync.RWMutex
	connections    map[string]*conn
	routePolicy    RoutePolicy
	connOAuthStore connoauth.Store
	authEvents     *authevents.Writer

	semanticProvider semantic.Provider
	queryProvider    query.Provider
	embedder         embedding.Provider

	// catalogStore loads spec bundles by catalog_id when a connection
	// references one. nil = catalog-backed specs disabled (connections
	// with catalog_id set still register, but with zero ops).
	catalogStore catalog.Store

	// exportDeps holds platform-side dependencies for api_export
	// (nil = export disabled, tool not registered).
	exportDeps *ExportDeps
}

// ConnOAuthStore returns the unified OAuth token store wired into
// this toolkit, or nil when the toolkit was constructed without one.
func (t *Toolkit) ConnOAuthStore() connoauth.Store {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connOAuthStore
}

// SetCatalogStore wires the catalog store the toolkit consults when
// a connection's CatalogID is set. Passing nil disables catalog-
// backed specs — connections with CatalogID configured still
// register, but with zero ops on their list_endpoints surface.
func (t *Toolkit) SetCatalogStore(s catalog.Store) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.catalogStore = s
}

// CatalogStore returns the wired catalog store, or nil. Used by the
// admin layer to share the same store between toolkit reads and
// admin CRUD writes.
func (t *Toolkit) CatalogStore() catalog.Store {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.catalogStore
}

// ReloadConnection drops and rebuilds the named connection so a
// catalog mutation (a portal save against api_catalog_specs) is
// reflected immediately. The connection's auth state and HTTP
// client are reconstructed from cfg; in-flight OAuth refresh
// tokens persist via the unified connoauth store.
//
// Returns ErrConnectionNotFound when no connection has the given
// name. Other errors propagate from the rebuild path (config parse,
// authenticator construction).
func (t *Toolkit) ReloadConnection(name string) error {
	t.mu.Lock()
	existing, ok := t.connections[name]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf("apigateway: %s: %w", name, ErrConnectionNotFound)
	}
	cfg := existing.cfg
	if existing.client != nil {
		existing.client.CloseIdleConnections()
	}
	delete(t.connections, name)
	t.mu.Unlock()
	return t.addParsedConnection(name, cfg)
}

// ReloadConnectionsByCatalog rebuilds every registered connection
// whose CatalogID matches catalogID. Errors from individual
// rebuilds are logged but do not abort the sweep — one broken
// connection should not prevent the rest of the catalog's
// connections from picking up the new spec content.
func (t *Toolkit) ReloadConnectionsByCatalog(catalogID string) {
	if catalogID == "" {
		return
	}
	t.mu.RLock()
	names := make([]string, 0, len(t.connections))
	for n, c := range t.connections {
		if c.cfg.CatalogID == catalogID {
			names = append(names, n)
		}
	}
	t.mu.RUnlock()
	for _, n := range names {
		if err := t.ReloadConnection(n); err != nil {
			slog.Warn("apigateway: catalog reload failed",
				logKeyConnection, n, logKeyCatalogID, catalogID, logKeyError, err)
		}
	}
}

// SetConnOAuthStore wires the unified OAuth token store. Required for
// the authorization_code grant: the Authenticator reads through the
// store on every Apply and persists rotated refresh tokens back.
// Connections registered before SetConnOAuthStore is called will pick
// up the store immediately because the wire step re-threads every
// already-materialized authorization_code Authenticator.
func (t *Toolkit) SetConnOAuthStore(s connoauth.Store) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connOAuthStore = s
	for _, c := range t.connections {
		if ac, ok := c.auth.(*oauth2AuthorizationCodeAuth); ok {
			ac.SetConnOAuthStore(s)
		}
	}
}

// SetAuthEvents wires the audit-event writer to the toolkit and into
// every already-materialized authorization_code authenticator. Called
// by the platform alongside SetTokenStore so every outbound refresh
// emits its lifecycle event. The writer itself is nil-safe — passing
// nil silences events at the toolkit level (e.g., dev with no DB).
func (t *Toolkit) SetAuthEvents(w *authevents.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.authEvents = w
	for _, c := range t.connections {
		if ac, ok := c.auth.(*oauth2AuthorizationCodeAuth); ok {
			ac.SetAuthEvents(w)
		}
	}
}

// RoutePolicy gates an api_invoke_endpoint call by (connection, method,
// path) on top of the platform's existing tool/connection authorization.
// Layered design: the MCP middleware's Authorizer.IsAuthorized check
// covers "may this user call api_invoke_endpoint at all?" and "on this
// connection at all?". RoutePolicy answers the more specific question
// "may this user call THIS method on THIS path of this connection?".
//
// Reason is included for audit/log clarity when Allowed is false.
// Implementations must read the caller's roles from ctx (typically via
// the middleware's pre-authenticated user or an Authenticator) and
// resolve them to a persona's APIRoutes rules.
type RoutePolicy interface {
	Allow(ctx context.Context, connection, method, path string) (allowed bool, reason string)
}

// conn carries the materialized state for a single registered
// connection: parsed config, the Authenticator implementing its
// auth mode, a per-connection HTTP client, and (when the connection
// references a catalog) the merged operation index plus retained
// parsed OpenAPI documents that api_get_endpoint_schema reads.
//
// embedTexts is the parallel-indexed text fed to the embedding
// provider for semantic ranking; embeddings are populated lazily on
// the first non-lexical api_list_endpoints call (so an unreachable
// embedding service does not block platform startup). embedMu
// serializes the lazy populate so concurrent calls embed once.
// Invalidation is by full conn rebuild — ReloadConnection drops
// the conn and re-creates it when a catalog mutates, so the
// embedding cache is naturally fresh.
type conn struct {
	cfg         Config
	auth        Authenticator
	client      *http.Client
	specs       map[string]*specState
	operations  []OperationSummary
	embedTexts  []string
	embeddings  [][]float32
	embedMu     sync.Mutex
	embedFailed bool
}

// specState retains the parsed OpenAPI document for a component
// spec alongside the catalog metadata the portal needs (source
// kind, URL, etag, fetch time). The parsed doc is what
// api_get_endpoint_schema walks to assemble per-endpoint detail —
// without it, the toolkit would have to re-parse on every call.
type specState struct {
	doc           *openapi3.T
	sourceKind    string
	sourceURL     string
	etag          string
	lastFetchedAt time.Time
}

// New builds an empty toolkit. Connections are added later via
// AddConnection (used both by NewMulti at startup and by the admin
// hot-add path at runtime).
func New(name string) *Toolkit {
	if name == "" {
		name = Kind
	}
	return &Toolkit{
		name:        name,
		connections: make(map[string]*conn),
	}
}

// NewMulti builds a Toolkit and pre-loads the given parsed
// connection configs. Per-connection materialization failures are
// logged and skipped so a single bad connection does not block
// platform startup.
func NewMulti(cfg MultiConfig) *Toolkit {
	name := cfg.DefaultName
	if name == "" {
		name = Kind
	}
	t := New(name)
	t.defaultName = cfg.DefaultName
	for instanceName, c := range cfg.Instances {
		if c.ConnectionName == "" {
			c.ConnectionName = instanceName
		}
		if err := t.addParsedConnection(instanceName, c); err != nil {
			slog.Warn("apigateway: initial connection failed",
				logKeyConnection, instanceName, logKeyError, err)
		}
	}
	return t
}

// Kind returns the toolkit kind discriminator.
func (*Toolkit) Kind() string { return Kind }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the default connection name for audit
// attribution when a tool call does not carry one. Empty string when
// no default is configured (multi-connection deployments typically
// require the model to pass `connection` explicitly).
func (t *Toolkit) Connection() string { return t.defaultName }

// RegisterTools registers the api gateway's MCP tools.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolInvokeEndpoint,
		Title: "Invoke API Endpoint",
		Description: "Make an authenticated HTTP request against a registered API connection. " +
			"The connection's auth (none/bearer/api_key) is applied automatically; the model " +
			"never handles credentials. Returns status, selected response headers, and the parsed " +
			"or text response body. Method is restricted to GET, POST, PUT, DELETE, PATCH, HEAD; " +
			"path is joined to the connection's base_url; response bodies above the connection's " +
			"max_response_bytes are truncated and flagged. Use list_connections to discover " +
			"available kind=api connections.",
		InputSchema: invokeEndpointSchema,
	}, t.handleInvoke)

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolListEndpoints,
		Title: "List API Endpoints",
		Description: "List operations exposed by a registered API connection's OpenAPI " +
			"document. Use this BEFORE api_invoke_endpoint to discover what method+path " +
			"combinations the upstream supports. Optional `query` does a case-insensitive " +
			"substring match against operation_id, path, summary, and tags. Returns " +
			"operation_id, method, path, summary, and tags for each match. If the " +
			"connection has no OpenAPI spec configured, returns an empty list with a " +
			"note. Persona policy still applies at invoke time — a listed operation " +
			"may still be refused by api_invoke_endpoint.",
		InputSchema: listEndpointsSchema,
	}, t.handleListEndpoints)

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolGetEndpointSchema,
		Title: "Get API Endpoint Schema",
		Description: "Return detailed schema for one operation on an API connection: " +
			"parameters, request body, and per-status response shapes. Pass operation_id " +
			"from api_list_endpoints. Security and server metadata are omitted — the " +
			"connection is pre-authenticated. When an operation_id is defined by more " +
			"than one component spec in the connection's catalog, pass `spec` to " +
			"disambiguate; the ambiguity response lists the candidates.",
		InputSchema: getEndpointSchemaInputSchema,
	}, t.handleGetEndpointSchema)

	// api_export is registered only when ExportDeps were wired by
	// the platform (portal asset store available). Skipping the
	// registration when deps are nil keeps the model from seeing
	// a tool it can never successfully call.
	t.registerExportTool(s)
}

// Tools returns the list of tool names this toolkit registers.
// api_export is included only when the toolkit was constructed
// with ExportDeps wired so callers (audit / introspection) see the
// tool list that actually exists at runtime.
func (t *Toolkit) Tools() []string {
	tools := []string{ToolInvokeEndpoint, ToolListEndpoints, ToolGetEndpointSchema}
	t.mu.RLock()
	hasExport := t.exportDeps != nil
	t.mu.RUnlock()
	if hasExport {
		tools = append(tools, exportToolName)
	}
	return tools
}

// ListEndpointsInput is the parsed argument shape for
// api_list_endpoints. Field names match the JSON schema.
type ListEndpointsInput struct {
	Connection string `json:"connection"`
	Query      string `json:"query,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Ranking    string `json:"ranking,omitempty"`
}

// ListEndpointsOutput is the structured result. Empty + Note when
// the connection has no OpenAPI spec configured (so the model can
// distinguish "no spec" from "no matches").
type ListEndpointsOutput struct {
	Operations []OperationSummary `json:"operations"`
	Note       string             `json:"note,omitempty"`
}

// defaultListEndpointsLimit caps the result set when the caller
// doesn't specify limit. Keeps the response from blowing context on
// large APIs while staying generous enough for casual queries; the
// model can request more by passing limit explicitly.
const defaultListEndpointsLimit = 50

func (t *Toolkit) handleListEndpoints(ctx context.Context, _ *mcp.CallToolRequest, in ListEndpointsInput) (*mcp.CallToolResult, any, error) {
	if in.Connection == "" {
		return errorResult("connection is required"), nil, nil
	}
	t.mu.RLock()
	c, ok := t.connections[in.Connection]
	policy := t.routePolicy
	t.mu.RUnlock()
	if !ok {
		return errorResult(fmt.Sprintf("connection %q not found (use list_connections to discover api connections)", in.Connection)), nil, nil
	}
	if len(c.operations) == 0 {
		out := ListEndpointsOutput{
			Operations: []OperationSummary{},
			Note:       "no catalog_id configured for this connection — call api_invoke_endpoint with method+path directly",
		}
		return jsonResult(out), out, nil
	}
	// Filter through the route policy so a persona only sees the
	// operations it could actually invoke. Without this, a persona
	// scoped to GET /v1/users/* still sees DELETE /v1/users/{id}
	// listed and the model wastes a turn discovering the denial at
	// invoke time.
	visible := filterByRoutePolicy(ctx, policy, in.Connection, c.operations)
	limit := in.Limit
	if limit <= 0 {
		limit = defaultListEndpointsLimit
	}
	mode, modeErr := parseRankingMode(in.Ranking)
	if modeErr != nil {
		return errorResult(modeErr.Error()), nil, nil
	}
	ranked, fellBack := rankWithMode(ctx, rankRequest{
		tk: t, conn: c, ops: visible, query: in.Query, limit: limit, mode: mode,
	})
	out := ListEndpointsOutput{Operations: ranked}
	if fellBack {
		out.Note = fmt.Sprintf("ranking %q fell back to lexical: embedding pipeline unavailable for this connection", mode)
	}
	return jsonResult(out), out, nil
}

// parseRankingMode maps the input string to a RankingMode. Empty
// (omitted from the call) defaults to lexical for backward
// compatibility — adding semantic ranking to the toolkit must not
// silently change behavior for callers that don't pass the new
// field.
func parseRankingMode(raw string) (RankingMode, error) {
	switch raw {
	case "", string(RankingLexical):
		return RankingLexical, nil
	case string(RankingSemantic):
		return RankingSemantic, nil
	case string(RankingHybrid):
		return RankingHybrid, nil
	default:
		return "", fmt.Errorf(`invalid ranking %q (want "lexical", "semantic", or "hybrid")`, raw)
	}
}

// filterByRoutePolicy returns the subset of operations the supplied
// route policy permits for this connection. A nil policy is a
// passthrough (returns ops unchanged) — backward-compatible with
// deployments that haven't installed a policy yet. Operations the
// policy denies are dropped silently from the result; the model
// sees a curated catalog of what it can actually call.
func filterByRoutePolicy(ctx context.Context, policy RoutePolicy, connection string, ops []OperationSummary) []OperationSummary {
	if policy == nil {
		return ops
	}
	out := make([]OperationSummary, 0, len(ops))
	for _, op := range ops {
		allowed, _ := policy.Allow(ctx, connection, op.Method, op.Path)
		if allowed {
			out = append(out, op)
		}
	}
	return out
}

// SetSemanticProvider stores the semantic provider. Reserved for
// future enrichment (e.g. response shaping driven by DataHub PII
// tags, see issue #373); not consumed in v1.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.semanticProvider = provider
}

// SetEmbeddingProvider wires the embedding model used by the
// "semantic" and "hybrid" ranking modes of api_list_endpoints. nil
// (the default) disables non-lexical ranking; calls that request
// it fall back to lexical with a note. Per-connection embedding
// vectors are computed lazily on the first non-lexical call so an
// unreachable embedding service does not block platform startup.
func (t *Toolkit) SetEmbeddingProvider(p embedding.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.embedder = p
}

// SetQueryProvider stores the query provider. Reserved for future
// warehouse-bridging features (see issue #372); not consumed in v1.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queryProvider = provider
}

// SetRoutePolicy installs a per-(connection, method, path) authorization
// gate. When set, api_invoke_endpoint consults the policy after the
// connection lookup and before the upstream call. A nil policy means
// no per-route gating — the platform's existing tool/connection
// authorization is the sole gate (backward-compatible).
func (t *Toolkit) SetRoutePolicy(p RoutePolicy) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routePolicy = p
}

// RoutePolicy returns the currently installed route policy, or nil if
// none has been wired. Exposed so platform-side tests can verify that
// WireAPIGatewayRoutePolicy actually installed a policy and exercise
// it directly without spinning up a full MCP server.
func (t *Toolkit) RoutePolicy() RoutePolicy {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.routePolicy
}

// AddConnection parses a raw config map, materializes the per-
// connection auth + HTTP client, and registers the connection. Used
// both at startup (via NewMulti) and at runtime via the admin
// hot-add path.
func (t *Toolkit) AddConnection(name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return err
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	return t.addParsedConnection(name, cfg)
}

// addParsedConnection assumes the Config is already validated. It
// builds the Authenticator and HTTP client and inserts the
// connection under lock. When cfg.CatalogID is set AND a catalog
// store is wired, specs are loaded from the catalog and merged into
// the operation index. Catalog-loading failures are non-fatal: the
// connection still registers (with zero ops) so portal operators
// can see it and fix the catalog reference, rather than the
// connection vanishing from the UI.
func (t *Toolkit) addParsedConnection(name string, cfg Config) error {
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		return fmt.Errorf("apigateway: %s: %w", name, err)
	}
	specs, ops, texts := t.buildConnSpecs(name, cfg.CatalogID)
	c := &conn{
		cfg:        cfg,
		auth:       auth,
		client:     newHTTPClient(cfg),
		specs:      specs,
		operations: ops,
		embedTexts: texts,
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.connections[name]; exists {
		return fmt.Errorf("apigateway: %s: %w", name, ErrConnectionExists)
	}
	// Wire the unified token store into authorization_code
	// authenticators inline so a connection added BEFORE
	// SetConnOAuthStore still becomes functional once that wire step
	// runs (which re-threads the store across all connections).
	// Either ordering works.
	if ac, ok := auth.(*oauth2AuthorizationCodeAuth); ok {
		if t.connOAuthStore != nil {
			ac.SetConnOAuthStore(t.connOAuthStore)
		}
		if t.authEvents != nil {
			ac.SetAuthEvents(t.authEvents)
		}
	}
	t.connections[name] = c
	return nil
}

// buildConnSpecs loads the connection's catalog (when configured),
// parses each component spec, and returns the merged operation
// index alongside the per-spec retained state. A nil catalog
// store, an empty catalog_id, or a load failure all return zero
// values — the caller proceeds with no spec surface and logs the
// reason.
func (t *Toolkit) buildConnSpecs(connName, catalogID string) (
	specs map[string]*specState, operations []OperationSummary, texts []string,
) {
	if catalogID == "" {
		return nil, nil, nil
	}
	t.mu.RLock()
	store := t.catalogStore
	t.mu.RUnlock()
	if store == nil {
		slog.Warn("apigateway: connection references catalog but no catalog store wired",
			logKeyConnection, connName, logKeyCatalogID, catalogID)
		return nil, nil, nil
	}
	entries, err := store.ListSpecs(context.Background(), catalogID)
	if err != nil {
		slog.Warn("apigateway: failed to load catalog specs",
			logKeyConnection, connName, logKeyCatalogID, catalogID, logKeyError, err)
		return nil, nil, nil
	}
	specs = make(map[string]*specState, len(entries))
	for _, e := range entries {
		doc, perr := parseOpenAPISpec(e.Content)
		if perr != nil {
			slog.Warn("apigateway: skipping unparseable spec",
				logKeyConnection, connName, logKeyCatalogID, catalogID,
				"spec_name", e.SpecName, logKeyError, perr)
			continue
		}
		specs[e.SpecName] = &specState{
			doc:           doc,
			sourceKind:    e.SourceKind,
			sourceURL:     e.SourceURL,
			etag:          e.ETag,
			lastFetchedAt: e.LastFetchedAt,
		}
		specOps, specTexts := buildOperationIndex(doc, e.SpecName)
		operations = append(operations, specOps...)
		texts = append(texts, specTexts...)
	}
	return specs, operations, texts
}

// RemoveConnection drops a registered connection. Used by the admin
// hot-remove path when an operator deletes the connection in the
// portal. Idle keepalive sockets on the per-connection HTTP client
// are closed so they don't linger up to idleConnectionTimeout after
// the connection is gone.
func (t *Toolkit) RemoveConnection(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, exists := t.connections[name]
	if !exists {
		return fmt.Errorf("apigateway: %s: %w", name, ErrConnectionNotFound)
	}
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	delete(t.connections, name)
	return nil
}

// HasConnection reports whether a connection with the given name is
// registered.
func (t *Toolkit) HasConnection(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.connections[name]
	return ok
}

// ListConnections returns details for every registered connection,
// in name-sorted order. Implements toolkit.ConnectionLister so the
// platform's unified list_connections tool surfaces api connections
// alongside trino, s3, and mcp.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.connections))
	for name := range t.connections {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]toolkit.ConnectionDetail, 0, len(names))
	for _, name := range names {
		c := t.connections[name]
		out = append(out, toolkit.ConnectionDetail{
			Name:        name,
			Description: c.cfg.BaseURL,
			IsDefault:   name == t.defaultName,
		})
	}
	return out
}

// Close releases per-connection HTTP client resources.
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

// handleInvoke is the MCP handler for api_invoke_endpoint. It looks
// up the named connection, runs the call, and returns the structured
// result either as a JSON-encoded text content (for human-readable
// chat surfaces) and as a typed Output (so structured-output-aware
// clients can consume it directly).
func (t *Toolkit) handleInvoke(ctx context.Context, _ *mcp.CallToolRequest, in InvokeInput) (*mcp.CallToolResult, any, error) {
	if in.Connection == "" {
		return errorResult("connection is required"), nil, nil
	}
	t.mu.RLock()
	c, ok := t.connections[in.Connection]
	policy := t.routePolicy
	t.mu.RUnlock()
	if !ok {
		return errorResult(fmt.Sprintf("connection %q not found (use list_connections to discover api connections)", in.Connection)), nil, nil
	}

	// Run the route policy BEFORE invoke() so an unauthorized call
	// never produces an outbound HTTP request — and never appears in
	// the upstream's access log.
	if res := checkRoutePolicy(ctx, policy, in); res != nil {
		return res, nil, nil //nolint:nilerr // tool error surfaced via result
	}

	out, err := invoke(ctx, invocation{cfg: c.cfg, auth: c.auth, client: c.client}, in)
	if err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol — argument validation surfaced as tool error
	}
	// Clear the api_export hint when the toolkit was built without
	// export deps — the model would otherwise be told to use a tool
	// that isn't registered on this deployment. The hint itself
	// originates in executeRequest which has no toolkit handle, so
	// the gating happens here at the call site.
	t.mu.RLock()
	hasExport := t.exportDeps != nil
	t.mu.RUnlock()
	if !hasExport {
		out.Hint = ""
	}
	return jsonResult(out), out, nil
}

// checkRoutePolicy runs the optional per-(connection, method, path)
// authorization gate. Returns nil when the policy is unset or when
// the call is allowed. Returns a non-nil error result when the
// method or path validators reject the input, or when the policy
// denies the call.
//
// Method and path are validated up front so the policy sees
// normalized values (uppercase method, "/-prefixed" path). invoke()
// re-validates idempotently so the policy step can be skipped when
// no policy is installed without losing input safety.
func checkRoutePolicy(ctx context.Context, policy RoutePolicy, in InvokeInput) *mcp.CallToolResult {
	if policy == nil {
		return nil
	}
	method, mErr := validateMethod(in.Method)
	if mErr != nil {
		return errorResult(mErr.Error())
	}
	if pErr := validatePath(in.Path); pErr != nil {
		return errorResult(pErr.Error())
	}
	allowed, reason := policy.Allow(ctx, in.Connection, method, in.Path)
	if allowed {
		return nil
	}
	msg := "not authorized for this method/path on this connection"
	if reason != "" {
		msg = msg + ": " + reason
	}
	return errorResult(msg)
}

// jsonResult creates a successful MCP result with the JSON-encoded
// payload as text content. Mirrors the helper used by other
// in-repo toolkits so the chat surface formatting stays consistent.
func jsonResult(data any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return errorResult("internal error: " + err.Error())
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	b, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		b = []byte(`{"error": "internal error"}`)
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

// newHTTPClient builds the per-connection HTTP client. Redirects
// are explicitly disallowed so the toolkit does not blindly
// re-issue a request (and re-attach the connection's credential)
// to a host the operator did not authorize. The model can follow
// redirects manually by reading the upstream Location header from
// the response and issuing a new api_invoke_endpoint call with the
// redirected URL.
func newHTTPClient(cfg Config) *http.Client {
	return &http.Client{
		Timeout:   cfg.CallTimeout,
		Transport: newHTTPTransport(cfg),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// idleConnectionTimeout caps how long an idle keep-alive connection
// can sit in the pool before being closed. Independent of the
// per-call timeouts; a generous default reduces reconnect churn for
// chatty connections.
const idleConnectionTimeout = 90 * time.Second

// maxIdleConnections caps the per-host pool of reusable keep-alive
// sockets. Modest because each connection's typical workload is
// occasional fan-out from MCP tool calls, not high-throughput.
const maxIdleConnections = 10

// newHTTPTransport builds the per-connection http.Transport. The
// dial step (TCP + TLS handshake) is bound by cfg.ConnectTimeout so
// an unreachable upstream fails fast instead of consuming the full
// CallTimeout budget. Exposed as a separate function so unit tests
// can verify the wiring without standing up a network listener.
func newHTTPTransport(cfg Config) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: cfg.ConnectTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       idleConnectionTimeout,
		MaxIdleConns:          maxIdleConnections,
	}
}

// Verify interface compliance at compile time. The registry.Toolkit
// shape is inlined to avoid an import cycle (pkg/registry imports
// this package via factories.go).
var (
	_ interface {
		Kind() string
		Name() string
		Connection() string
		RegisterTools(s *mcp.Server)
		Tools() []string
		SetSemanticProvider(provider semantic.Provider)
		SetQueryProvider(provider query.Provider)
		Close() error
	} = (*Toolkit)(nil)
	_ toolkit.ConnectionManager = (*Toolkit)(nil)
	_ toolkit.ConnectionLister  = (*Toolkit)(nil)
)
