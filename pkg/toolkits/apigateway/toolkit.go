package apigateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/observability"
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

// ErrOperationNotFound is returned when no spec in scope defines the
// requested operation id. The browse surface also answers it for an
// operation the route policy denies: an operation the caller may not
// invoke is absent from that surface rather than refused by it, which
// is the same treatment api_discover gives it.
var ErrOperationNotFound = errors.New("apigateway: operation not found")

// ErrAmbiguousOperation is returned when an operation id is defined by
// more than one component spec in a connection's catalog. Wrapped by
// ambiguousOperationError, which names the candidate specs.
//
// Unlike its siblings above it carries no package prefix: it is always
// wrapped rather than returned bare, and the wrap site supplies the
// prefix so the composed message reads as one sentence.
var ErrAmbiguousOperation = errors.New("ambiguous operation_id")

const (
	// ToolInvokeEndpoint is the MCP tool name for the invoke
	// operation. Exported so audit code and tests reference the same
	// literal as the registration site.
	ToolInvokeEndpoint = "api_invoke_endpoint"

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

	// catalogWiringDone reports whether platform-level wiring has had
	// its chance to run. Until it has, a nil catalogStore is the
	// construction order (NewMulti builds connections before the
	// platform wires the store and reloads them), not a deployment
	// gap, and the unbacked-catalog warning is withheld (#1509).
	catalogWiringDone bool

	// exampleStore reads the requests promoted on an endpoint: calls
	// against this connection that are known to have worked (#1321). nil
	// leaves an endpoint's schema exactly as its spec declares it.
	exampleStore catalog.ExampleStore

	// exportDeps holds platform-side dependencies for api_export
	// (nil = export disabled, tool not registered).
	exportDeps *ExportDeps

	// metrics is the observability recorder wired by the platform.
	// nil = metrics subsystem disabled; the instrumented transport
	// short-circuits to the bare base transport in that case.
	metrics *observability.Metrics

	// memBudget is the process-wide in-flight memory budget the
	// buffered tools (api_invoke_endpoint, api_export) reserve against
	// before allocating a response buffer (issue #535). nil = unlimited
	// (no global budget configured); the budget type is nil-safe so the
	// buffered path is unchanged in that case. Shared across toolkit
	// instances when the platform injects the same handle into each.
	memBudget *MemBudget

	// internalHandler serves handler=internal connections (issue
	// #1005). nil until SetInternalHandler wires it; adding an
	// internal connection before then fails.
	internalHandler http.Handler
}

// SetMemBudget wires the shared in-flight memory budget the buffered
// tools reserve against. Passing nil (or a disabled budget) leaves the
// buffered path unbounded by a global cap — per-request caps still
// apply. The platform creates one budget at startup and injects the
// same handle into every api gateway toolkit so accounting is truly
// process-wide, not per-instance.
func (t *Toolkit) SetMemBudget(b *MemBudget) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.memBudget = b
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

// SetExampleStore wires the store of promoted endpoint examples, so reading an
// endpoint's schema also shows the requests that are known to have worked
// against this connection (#1321).
func (t *Toolkit) SetExampleStore(s catalog.ExampleStore) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exampleStore = s
}

// CatalogStore returns the wired catalog store, or nil. Used by the
// admin layer to share the same store between toolkit reads and
// admin CRUD writes.
func (t *Toolkit) CatalogStore() catalog.Store {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.catalogStore
}

// warnNoCatalogStore reports one connection whose catalog_id names a
// spec bundle the toolkit cannot load because no catalog store is
// wired. Its endpoints surface is empty for as long as that holds.
func warnNoCatalogStore(connName, catalogID string) {
	slog.Warn("apigateway: connection references catalog but no catalog store wired",
		logKeyConnection, logsan.SanitizeForLog(connName), logKeyCatalogID, logsan.SanitizeForLog(catalogID))
}

// MarkCatalogWiringComplete records that platform-level wiring has
// run, and warns once for every registered connection that still
// references a catalog with no store to load it from.
//
// A nil store during construction is the platform's assembly order,
// not a deployment gap: NewMulti builds connections before the
// platform wires the catalog store, and WireAPIGatewayCatalogStore
// reloads every connection right after wiring it. Warning on that
// pass reported one broken connection per catalog-backed connection
// on every startup of a deployment where all of them served their
// specs normally (#1509).
//
// The deployment the message was written for is one where the store
// never arrives and the reload never corrects the state. That is what
// this reports, once the store's last chance to be wired has passed.
//
// Idempotent: a second call re-reports whatever is still unbacked.
// From here on, a connection built or reloaded without a store warns
// as it is built, since no store can still be on its way.
func (t *Toolkit) MarkCatalogWiringComplete() {
	type unbacked struct{ name, catalogID string }
	t.mu.Lock()
	t.catalogWiringDone = true
	var pending []unbacked
	if t.catalogStore == nil {
		for name, c := range t.connections {
			if c.cfg.CatalogID != "" {
				pending = append(pending, unbacked{name: name, catalogID: c.cfg.CatalogID})
			}
		}
	}
	t.mu.Unlock()
	sort.Slice(pending, func(i, j int) bool { return pending[i].name < pending[j].name })
	for _, u := range pending {
		warnNoCatalogStore(u.name, u.catalogID)
	}
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

// SetMetrics wires the observability recorder into the toolkit and
// retroactively instruments every already-registered connection's
// HTTP client so connections added before metrics were enabled still
// emit outbound observations. Passing nil is supported and clears
// the recorder, but does not unwrap already-wrapped transports —
// rebuilding a connection (ReloadConnection / RemoveConnection +
// AddConnection) is the supported path to drop the wrapping.
//
// Call this at startup, before traffic begins. The retro-wrap path
// mutates each connection's http.Client.Transport in place; http.Client
// does not document Transport as safe for concurrent reassignment with
// in-flight Do() calls. The platform's WireAPIGatewayMetrics is invoked
// once in cmd/main.go before any MCP listener starts accepting requests,
// so the in-place mutation is race-free in practice. instrumentClient
// is idempotent against the same (connection, metrics) pair so a second
// SetMetrics call with the same recorder is a no-op rather than a
// double-wrap.
func (t *Toolkit) SetMetrics(m *observability.Metrics) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.metrics = m
	if !m.Enabled() {
		return
	}
	for name, c := range t.connections {
		instrumentClient(c.client, name, m)
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
//
// path and template carry the same request in the two forms this toolkit
// holds it in. A listing surface starts from the operation the catalog
// declares, whose path is a template ("/v1/orders/{id}"); an invoke
// starts from the path the call reaches, with its parameters already
// substituted ("/v1/orders/42"). Both are passed so a policy written
// against either form governs both, rather than a rule hiding an
// operation from the listing while permitting the call it hides.
// template is empty when no operation in the connection's catalog
// declares a matching path, and equal to path for a path carrying no
// placeholder.
type RoutePolicy interface {
	Allow(ctx context.Context, connection, method, path, template string) (allowed bool, reason string)
}

// conn carries the materialized state for a single registered
// connection: parsed config, the Authenticator implementing its
// auth mode, a per-connection HTTP client, and (when the connection
// references a catalog) the merged operation index plus retained
// parsed OpenAPI documents that api_discover reads.
//
// embedVectors maps (spec_name, operation_id) to the pre-computed
// embedding vector loaded from the catalog store at registration
// time. The toolkit no longer computes vectors itself — the admin
// handler writes them at spec-upsert time and persists them in
// api_catalog_operation_embeddings, keyed on the spec content
// rather than on this connection. Two connections that mount the
// same catalog read the same rows and never trigger a duplicate
// embedding pass; a process restart still finds vectors ready
// because they live in Postgres. An empty map means the spec was
// written without an embedder configured (or the operator has not
// yet run the re-embed admin endpoint) — semantic ranking falls
// back to lexical with the errEmbeddingsNotIndexed note.
type conn struct {
	cfg          Config
	auth         Authenticator
	client       *http.Client
	specs        map[string]*specState
	operations   []OperationSummary
	embedVectors map[embedKey][]float32

	// operationRouterOnce lazily builds operationRouter from specs on
	// first ResolveOperationID call. The router lives on the conn and
	// is discarded when ReloadConnection rebuilds the conn after a
	// catalog edit, so no separate cache-invalidation path is needed.
	operationRouterOnce sync.Once
	operationRouter     routers.Router
	// operationRawPaths maps each operationRouter path key
	// (effectiveBasePath+rawPath) back to its spec-relative rawPath so
	// ResolveOperationID can synthesize "<METHOD> <rawPath>" ids for
	// operations with no declared operationId, matching the ids
	// api_discover advertises. Built alongside operationRouter.
	operationRawPaths map[string]string
	// operationWebDAVRoutes carries the WebDAV-flavored path templates in
	// this connection's catalog (those with an x-webdav-method carrier
	// operation). WebDAV verbs (PROPFIND/MKCOL/MOVE/COPY) are not
	// representable as OpenAPI PathItem methods and nested resource paths
	// exceed a single-segment path variable, so both miss the gorillamux
	// operationRouter; ResolveOperationID falls back to matching against
	// these routes. Empty for connections with no WebDAV operations (the
	// common case), in which the fallback is a no-op. Built in the same
	// operationRouterOnce pass as operationRouter (issue #876).
	operationWebDAVRoutes []webdavRoute
	// testDescs is a unit-test-only aid: lets ranking_test.go's
	// buildTestConn / populateTestEmbeddings round-trip an
	// operation's description through buildEmbedText without making
	// OperationSummary carry the description in production. Never
	// populated outside tests.
	testDescs map[string]string
}

// embedKey identifies an embedding row within a connection.
// Spec is part of the key because two component specs in the
// same catalog can legitimately carry the same operation_id
// (a vendor that ships a "search" op in every component spec it
// publishes). Without the spec discriminator a multi-spec catalog
// would silently collapse colliding ops onto one vector.
type embedKey struct {
	Spec        string
	OperationID string
}

// specState retains the parsed OpenAPI document for a component
// spec alongside the catalog metadata the portal needs (source
// kind, URL, etag, fetch time). The parsed doc is what
// api_discover walks to assemble per-endpoint detail.
// Without it, the toolkit would have to re-parse on every call.
//
// effectiveBasePath is the prefix applied to every operation's
// spec-relative path so api_discover's operations level, the synthesized
// operationID at its operation level, and the stored
// OperationSummary.Path all agree on a single full path the model
// passes to api_invoke_endpoint. Resolution order at registration
// time: SpecEntry.BasePath (operator override) wins, falling back to
// the servers[0].url-derived value, with both sources run through
// computeEffectiveBasePath so a connection.base_url that already
// contains a documented prefix as a suffix gets the prefix dropped
// and no doubling occurs at invoke time.
//
// specState is built per connection (buildConnSpecs takes the
// connection's base_url), so the resolved prefix is a function of the
// (connection, spec) pair even though the parsed document behind it is
// the same catalog row for every connection that mounts it.
//
// title, description, and operationCount back api_discover's specs
// level. title/description resolve at registration time: SpecEntry.Title/Description (operator
// override) wins, falling back to the spec content's info.title /
// info.description. operationCount is the number of operations the
// spec parsed to, computed once here so the summary does not re-walk
// the document on every call.
type specState struct {
	doc               *openapi3.T
	sourceKind        string
	sourceURL         string
	etag              string
	lastFetchedAt     time.Time
	effectiveBasePath string
	title             string
	description       string
	operationCount    int
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
			"or text response body. Address the operation either by operation_id (the same id " +
			"api_discover returns, with any path template values " +
			"passed in path_params) or by method+path directly; supply one form, not both. " +
			"Method is restricted to GET, POST, PUT, DELETE, PATCH, HEAD, " +
			"PROPFIND, MKCOL, MOVE, COPY; " +
			"path is joined to the connection's base_url. Every response reports body_bytes, the size " +
			"of the body read; a result past the connection's max_inline_bytes (default 32 KiB, a budget on " +
			"the rendered tool result) is re-encoded compactly, and one still past it has its body " +
			"cut to fit, is flagged with body_truncated, and " +
			"export_arguments carries the api_export call " +
			"that streams the whole response into an asset. A response's pagination signal is " +
			"reported in `pagination` and not followed; pass `paginate` to walk every page in " +
			"this one call and receive the merged array (api_export takes the same block and " +
			"streams the walk into an asset). Use list_connections to discover " +
			"available kind=api connections. " + toolkit.CaptureRoute,
		InputSchema: invokeEndpointSchema,
	}, t.handleInvoke)

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolDiscover,
		Title: "Discover API Operations",
		Description: "Discover what a registered API connection exposes, at the depth you ask for, " +
			"before calling api_invoke_endpoint. With neither spec nor operation_id, a catalog that " +
			"bundles more than one component spec returns its specs (name, title, description, " +
			"operation_count, base_path), and a single-spec catalog returns its operations directly. " +
			"With spec, query, or both, returns the matching operations (operation_id, method, path, " +
			"summary, tags, spec), ranked by query when one is given. With operation_id, returns that " +
			"one operation's parameters, request body, and per-status response shapes; security and " +
			"server metadata are omitted because the connection is pre-authenticated. Every response " +
			"carries level (specs, operations, operation) and next, the argument that goes one level " +
			"deeper. A connection with no catalog answers with a note; call api_invoke_endpoint with " +
			"method+path directly there. Operations the persona's route rules deny are absent, and " +
			"persona policy still applies at invoke time.",
		InputSchema: discoverSchema,
	}, t.handleDiscover)

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
	tools := []string{ToolInvokeEndpoint, ToolDiscover}
	t.mu.RLock()
	hasExport := t.exportDeps != nil
	t.mu.RUnlock()
	if hasExport {
		tools = append(tools, exportToolName)
	}
	return tools
}

// SpecSummary is one row at api_discover's specs level. base_path is the effective
// prefix applied to every operation in the spec (operator override or
// the value derived from servers[0].url); operation_count is the
// number of operations the spec parses to.
type SpecSummary struct {
	Name           string `json:"name"`
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	OperationCount int    `json:"operation_count"`
	BasePath       string `json:"base_path,omitempty"`
}

// buildSpecSummaries projects a connection's parsed specs into the
// summary rows of api_discover's specs level, sorted by spec name for
// a stable, model-friendly order.
func buildSpecSummaries(c *conn) []SpecSummary {
	out := make([]SpecSummary, 0, len(c.specs))
	for name, st := range c.specs {
		out = append(out, SpecSummary{
			Name:           name,
			Title:          st.title,
			Description:    st.description,
			OperationCount: st.operationCount,
			BasePath:       st.effectiveBasePath,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// filterBySpec returns the subset of ops whose Spec exactly matches
// spec. Empty spec is a passthrough (no filtering). The match is
// case-sensitive because spec names are catalog-managed slugs the
// catalog store validates against a fixed pattern; case insensitivity
// would create false equivalences for catalogs that happen to host
// distinct specs whose names differ only in case.
func filterBySpec(ops []OperationSummary, spec string) []OperationSummary {
	if spec == "" {
		return ops
	}
	out := make([]OperationSummary, 0, len(ops))
	for _, op := range ops {
		if op.Spec == spec {
			out = append(out, op)
		}
	}
	return out
}

// rankingFallbackNote builds the response Note when ranking fell
// back to lexical. An empty reason yields an empty Note. When the
// hybrid mode was auto-selected because the caller omitted ranking
// (defaulted), the note must not attribute "hybrid" to the caller —
// it phrases the cause as the default semantic path being
// unavailable, so an operator is never told they requested a mode
// they never passed (#858).
func rankingFallbackNote(defaulted bool, mode RankingMode, reason string) string {
	if reason == "" {
		return ""
	}
	if defaulted {
		return fmt.Sprintf("default semantic ranking unavailable, used lexical: %s", reason)
	}
	return fmt.Sprintf("ranking %q fell back to lexical: %s", mode, reason)
}

// parseRankingMode maps the input string to a RankingMode. Empty
// (omitted from the call) parses to lexical as the safe floor; the
// list-endpoints handler then upgrades an omitted ranking to hybrid
// when the connection has an embedding index available, so the
// semantic path is ON by default whenever its requirement is met
// (#858). An explicit "lexical" is preserved as an opt-out.
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
		allowed, _ := policy.Allow(ctx, connection, op.Method, op.Path, op.Path)
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

// SetEmbeddingProvider wires the embedding model used at ranking
// time to embed the agent's query. nil (the default) disables
// non-lexical ranking; calls that request it fall back to lexical
// with a note. The toolkit no longer warms per-operation vectors
// from this hook — those are computed at spec-upsert time by the
// admin handler and persisted in api_catalog_operation_embeddings,
// which makes them survive restarts and shared across every
// connection that mounts the same catalog. To recompute vectors
// for a catalog that was written before the provider was wired,
// the operator runs the re-embed admin endpoint or re-saves the
// spec; the toolkit then picks up the new vectors on the next
// ReloadConnection.
func (t *Toolkit) SetEmbeddingProvider(p embedding.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.embedder = p
}

// EmbeddingProvider returns the embedding provider configured on
// the toolkit, or nil. Exposed so the admin handler can fall back
// to the toolkit-wired provider when no platform-level embedder
// was injected via Deps.Embedder.
func (t *Toolkit) EmbeddingProvider() embedding.Provider {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.embedder
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
// the operation index. Per-spec embedding vectors are also loaded
// from the catalog store at the same time — no goroutine, no
// warmer; vectors that were computed at spec-upsert time live in
// api_catalog_operation_embeddings and a process restart picks
// them back up unchanged. Catalog-loading failures are non-fatal:
// the connection still registers (with zero ops) so portal
// operators can see it and fix the catalog reference, rather than
// the connection vanishing from the UI.
func (t *Toolkit) addParsedConnection(name string, cfg Config) error {
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		return fmt.Errorf("apigateway: %s: %w", name, err)
	}
	specs, ops, vectors := t.buildConnSpecs(name, cfg.CatalogID, cfg.BaseURL)
	client, err := t.newConnClient(name, cfg)
	if err != nil {
		return err
	}
	c := &conn{
		cfg:          cfg,
		auth:         auth,
		client:       client,
		specs:        specs,
		operations:   ops,
		embedVectors: vectors,
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.connections[name]; exists {
		return fmt.Errorf("apigateway: %s: %w", name, ErrConnectionExists)
	}
	// Read t.metrics under the lock — SetMetrics writes it under
	// the same lock, so reading it outside (as we did previously)
	// raced against runtime hot-add via AddConnection.
	instrumentClient(client, name, t.metrics)
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

// newConnClient builds the per-connection HTTP client: an in-process
// dispatch client for handler=internal connections (issue #1005), the
// normal network client otherwise. An internal connection added
// before SetInternalHandler wired a handler is refused — it could
// never serve a request.
func (t *Toolkit) newConnClient(name string, cfg Config) (*http.Client, error) {
	if cfg.Handler != HandlerInternal {
		return newHTTPClient(cfg), nil
	}
	t.mu.RLock()
	h := t.internalHandler
	t.mu.RUnlock()
	if h == nil {
		return nil, fmt.Errorf("apigateway: %s: handler=internal requires SetInternalHandler before the connection is added", name)
	}
	return newInternalHTTPClient(h), nil
}

// specSummaryTitle resolves the title shown at api_discover's specs level and the
// multi-spec gate for one component spec. The operator override on
// the catalog row wins; otherwise the value derives from the spec
// content's info.title. Empty when neither is set.
func specSummaryTitle(e catalog.SpecEntry, doc *openapi3.T) string {
	if e.Title != "" {
		return e.Title
	}
	if doc != nil && doc.Info != nil {
		return strings.TrimSpace(doc.Info.Title)
	}
	return ""
}

// specSummaryDescription resolves the description the same way as
// specSummaryTitle, falling back to info.description. The override is
// already normalized at write time; the derived value is only trimmed
// so a verbose spec description is surfaced as the author wrote it.
func specSummaryDescription(e catalog.SpecEntry, doc *openapi3.T) string {
	if e.Description != "" {
		return e.Description
	}
	if doc != nil && doc.Info != nil {
		return strings.TrimSpace(doc.Info.Description)
	}
	return ""
}

// buildConnSpecs loads the connection's catalog (when configured),
// parses each component spec, builds the merged operation index,
// and pre-loads the embedding vectors persisted at spec-upsert
// time. A nil catalog store, an empty catalog_id, or a load
// failure all return zero values; the caller proceeds with no
// spec surface and logs the reason.
//
// connBaseURL is the connection's configured base URL. Its path
// component is consulted when deriving each spec's effective base
// path: if the connection's base_url already contains the spec's
// servers[0] path segment, the spec base path is dropped so the
// invoke-time URL join does not double the segment.
func (t *Toolkit) buildConnSpecs(connName, catalogID, connBaseURL string) (
	specs map[string]*specState, operations []OperationSummary, vectors map[embedKey][]float32,
) {
	if catalogID == "" {
		return nil, nil, nil
	}
	t.mu.RLock()
	store := t.catalogStore
	wiringDone := t.catalogWiringDone
	t.mu.RUnlock()
	if store == nil {
		// Before wiring completes this is the expected construction
		// order and the connection is rebuilt moments later;
		// MarkCatalogWiringComplete reports the connections still in
		// this state once the store can no longer arrive.
		if wiringDone {
			warnNoCatalogStore(connName, catalogID)
		}
		return nil, nil, nil
	}
	entries, err := store.ListSpecs(context.Background(), catalogID)
	if err != nil {
		slog.Warn("apigateway: failed to load catalog specs",
			logKeyConnection, logsan.SanitizeForLog(connName), logKeyCatalogID, logsan.SanitizeForLog(catalogID), logKeyError, err)
		return nil, nil, nil
	}
	specs = make(map[string]*specState, len(entries))
	vectors = make(map[embedKey][]float32)
	for _, e := range entries {
		doc, perr := parseOpenAPISpec(e.Content)
		if perr != nil {
			slog.Warn("apigateway: skipping unparseable spec",
				logKeyConnection, logsan.SanitizeForLog(connName), logKeyCatalogID, logsan.SanitizeForLog(catalogID),
				"spec_name", e.SpecName, logKeyError, perr)
			continue
		}
		// An operator override is authoritative and singular: it names
		// the one prefix this spec contributes, so it is the only
		// candidate the no-doubling check may match against. Absent an
		// override, every declared server is a candidate so a spec
		// shared across connections that speak the same API resolves
		// per connection rather than against servers[0] alone (#1298).
		basePathSources := []string{e.BasePath}
		if e.BasePath == "" {
			basePathSources = specBasePaths(doc)
		}
		effectiveBasePath := computeEffectiveBasePath(connBaseURL, basePathSources)
		specOps, _ := buildOperationIndex(doc, e.SpecName, effectiveBasePath)
		specs[e.SpecName] = &specState{
			doc:               doc,
			sourceKind:        e.SourceKind,
			sourceURL:         e.SourceURL,
			etag:              e.ETag,
			lastFetchedAt:     e.LastFetchedAt,
			effectiveBasePath: effectiveBasePath,
			title:             specSummaryTitle(e, doc),
			description:       specSummaryDescription(e, doc),
			operationCount:    len(specOps),
		}
		operations = append(operations, specOps...)
		// Pre-loading vectors from the store: every embedding row
		// is keyed on (catalog_id, spec_name, operation_id) and
		// was written at spec-upsert time. Missing rows mean the
		// spec was written without an embedder configured, or the
		// embedding compute step failed; in either case
		// embedVectors stays empty for that spec and ranking falls
		// back to lexical with the errEmbeddingsNotIndexed note.
		rows, listErr := store.ListOperationEmbeddings(context.Background(), catalogID, e.SpecName)
		if listErr != nil {
			slog.Warn("apigateway: failed to load operation embeddings",
				logKeyConnection, logsan.SanitizeForLog(connName), logKeyCatalogID, logsan.SanitizeForLog(catalogID),
				"spec_name", e.SpecName, logKeyError, listErr)
			continue
		}
		for _, r := range rows {
			vectors[embedKey{Spec: e.SpecName, OperationID: r.OperationID}] = r.Embedding
		}
	}
	return specs, operations, vectors
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

// connectionDescription resolves the description surfaced for a connection:
// the operator/built-in-supplied Description when set, otherwise the base
// URL. The base-URL fallback preserves the long-standing behavior where an
// api connection's subtitle in the admin UI (and list_connections) is its
// upstream root.
func connectionDescription(cfg Config) string {
	if cfg.Description != "" {
		return cfg.Description
	}
	return cfg.BaseURL
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
			Name:           name,
			Description:    connectionDescription(c.cfg),
			IsDefault:      name == t.defaultName,
			CatalogID:      c.cfg.CatalogID,
			OperationCount: len(c.operations),
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
		return toolkit.ErrorResult("connection is required"), nil, nil
	}
	t.mu.RLock()
	c, ok := t.connections[in.Connection]
	policy := t.routePolicy
	budget := t.memBudget
	t.mu.RUnlock()
	if !ok {
		return toolkit.ErrorResult(fmt.Sprintf("connection %q not found (use list_connections to discover api connections)", in.Connection)), nil, nil
	}

	// Resolve operation_id addressing into concrete method+path before
	// any gating or upstream work, so the route policy, path validation,
	// and audit all see the same values a plain method+path call would
	// (issue #1046).
	method, path, addrErr := operationAddressing{
		Method: in.Method, Path: in.Path, OperationID: in.OperationID,
		Spec: in.Spec, PathParams: in.PathParams,
	}.resolve(c)
	if addrErr != nil {
		return toolkit.ErrorResult(addrErr.Error()), nil, nil
	}
	in.Method, in.Path = method, path

	// Run the route policy BEFORE invoke() so an unauthorized call
	// never produces an outbound HTTP request — and never appears in
	// the upstream's access log.
	if res := checkRoutePolicy(ctx, policy, in, routeTemplateFor(policy, c, method, path)); res != nil {
		return res, nil, nil
	}

	// Raw passthrough (issue #535): when the REST shim has installed a
	// RawSink on the context, stream the upstream body straight to it
	// instead of buffering + enveloping. The route policy above has
	// already authorized the call, so the streamed path inherits the
	// same gating as the buffered path.
	if raw := rawPassthroughFromContext(ctx); raw != nil {
		if in.Paginate != nil {
			return toolkit.ErrorResult("paginate is not available on the raw passthrough route; call api_invoke_endpoint through the enveloped route or api_export"), nil, nil
		}
		return t.handleInvokeRaw(ctx, c, in, raw)
	}

	// Whether api_export is registered on this deployment gates the
	// steering hints below — the model must not be told to use a tool
	// that isn't available here. Read once, before invoke().
	t.mu.RLock()
	hasExport := t.exportDeps != nil
	t.mu.RUnlock()

	inv := invocation{cfg: c.cfg, auth: c.auth, client: c.client, specs: c.specs, webdavRoutes: c.webdavRoutes(), budget: budget, inlineBudget: inlineBudgetFor(ctx, c.cfg)}
	if in.Paginate != nil {
		return handleInvokeWalk(ctx, inv, pageAuthorizer(ctx, policy, c), in, hasExport)
	}

	out, err := invoke(ctx, inv, in)
	if err != nil {
		// A binary body refused before buffering renders as a
		// structured 415 with a steer to api_export; budget rejections
		// render as a structured 429; argument validation failures
		// render as a plain tool error.
		var nb *nonInlineableBodyError
		if errors.As(err, &nb) {
			return nb.result(hasExport), nil, nil
		}
		return budgetOrErrorResult(err), nil, nil
	}
	// Report the path an operation_id resolved to. The caller supplied an
	// id, not a path, so this is the only place the catalog's base-path
	// prefix and the path_params substitution become visible — without it
	// a prefix that routes to the wrong upstream reads as an unexplained
	// upstream 4xx (issue #1298). Set before the budget is applied so the
	// result measured is the one returned.
	if in.OperationID != "" {
		out.ResolvedPath = in.Path
	}
	// The rendering is taken before the result is built: applyInlineBudget
	// mutates out, and out is also the structured output returned below, so
	// the two must not be evaluated as operands of one call.
	text := applyInlineBudget(&out, in, inv.inlineBudget, hasExport)
	return buildInvokeResult(out, text), out, nil
}

// handleInvokeWalk is the `paginate` branch of handleInvoke (issue
// #1535). The route policy is re-run on every page's address, so a next
// link cannot walk the call onto an operation the persona is not allowed.
// A failed page is a tool error naming the page; a budget refusal keeps
// its structured 429 shape.
func handleInvokeWalk(ctx context.Context, inv invocation, authorize func(InvokeInput) error, in InvokeInput, hasExport bool) (*mcp.CallToolResult, any, error) {
	out, err := invokeWalk(ctx, inv, authorize, in)
	if err != nil {
		return budgetOrErrorResult(err), nil, nil
	}
	if in.OperationID != "" {
		out.ResolvedPath = in.Path
	}
	text := applyInlineBudget(&out, in, inv.inlineBudget, hasExport)
	return buildInvokeResult(out, text), out, nil
}

// pageAuthorizer is the route policy check a walk runs on every page.
// nil when no policy is installed, so a deployment without one pays
// nothing per page.
func pageAuthorizer(ctx context.Context, policy RoutePolicy, c *conn) func(InvokeInput) error {
	if policy == nil {
		return nil
	}
	return func(in InvokeInput) error {
		return routePolicyError(ctx, policy, in, routeTemplateFor(policy, c, in.Method, in.Path))
	}
}

// buildInvokeResult wraps an InvokeOutput in a CallToolResult,
// classifies the outcome, and stamps the result for the audit
// middleware and the REST shim:
//
//   - _meta.audit_outcome is set on EVERY call (including success)
//     so the audit middleware can populate audit_logs.error_category
//     for every row without parsing the JSON body.
//   - _meta.audit_outcome_message carries a concise human-readable
//     summary: the scrubbed transport-error text for gateway-level
//     failures, or the canonical HTTP status reason phrase (via
//     http.StatusText) for upstream 4xx/5xx. Empty on the success
//     path.
//   - IsError is set ONLY for gateway-level failures (transport
//     error, upstream timeout, i.e. Status == 0). Upstream 4xx and
//     5xx responses leave IsError = false because the gateway
//     successfully proxied; the upstream returned what it returned.
//     The REST shim's classifyToolError relies on this distinction
//     to map gateway-level failures to 502 / 504 while letting
//     successful proxies of upstream errors flow through as wire
//     HTTP 200 with the upstream code embedded in the body.
func buildInvokeResult(out InvokeOutput, text []byte) *mcp.CallToolResult {
	if len(text) == 0 {
		// The output could not be rendered at all. JSONResult reported that
		// in band rather than as a Go error; this keeps that contract.
		return toolkit.ErrorResult("apigateway: internal error marshaling response")
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}}
	outcome := ClassifyInvokeOutcome(out)
	result.Meta = mcp.Meta{observability.MetaAuditOutcome: outcome}
	if msg := auditOutcomeMessage(out); msg != "" {
		result.Meta[observability.MetaAuditOutcomeMessage] = msg
	}
	if outcome == observability.OutcomeTransportErr || outcome == observability.OutcomeUpstreamTimeout {
		result.IsError = true
	}
	stampWalkMeta(result, out.WalkStats)
	return result
}

// stampWalkMeta puts a walk's page count on the result's _meta for the
// audit middleware, which records it on the one call's row under
// parameters.result. A single-page call stamps nothing.
func stampWalkMeta(result *mcp.CallToolResult, stats *WalkStats) {
	if stats == nil {
		return
	}
	if result.Meta == nil {
		result.Meta = mcp.Meta{}
	}
	result.Meta[observability.MetaAuditResult] = map[string]any{
		"pages_fetched": stats.PagesFetched,
		"items_merged":  stats.ItemsMerged,
		"stopped_by":    stats.StoppedBy,
	}
}

// auditOutcomeMessage returns the human-readable summary string the
// audit middleware should record alongside the outcome category. For
// gateway-level failures the scrubbed transport error is the most
// specific signal; for upstream 4xx/5xx the canonical reason phrase
// keeps the audit row grep-friendly without dragging the upstream's
// arbitrary error body into the column. Empty string when the call
// succeeded or no useful summary is available.
func auditOutcomeMessage(out InvokeOutput) string {
	if out.Error != "" {
		return out.Error
	}
	if out.Status >= httpStatus4xxLo && out.Status < httpStatus6xxLo {
		return http.StatusText(out.Status)
	}
	return ""
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
//
// template is the catalog path in.Path resolved from, reported alongside
// it so a rule naming the operation as the catalog declares it refuses
// the calls that operation serves. It is empty when the connection has
// no catalog or none of its operations declare a matching path.
func checkRoutePolicy(ctx context.Context, policy RoutePolicy, in InvokeInput, template string) *mcp.CallToolResult {
	if err := routePolicyError(ctx, policy, in, template); err != nil {
		return toolkit.ErrorResult(err.Error())
	}
	return nil
}

// routePolicyError is checkRoutePolicy's decision as an error, so a page
// walk can run the same gate on every page and fail the call in the
// policy's own words.
func routePolicyError(ctx context.Context, policy RoutePolicy, in InvokeInput, template string) error {
	if policy == nil {
		return nil
	}
	method, err := validateMethod(in.Method)
	if err != nil {
		return err
	}
	if err := validatePath(in.Path); err != nil {
		return err
	}
	allowed, reason := policy.Allow(ctx, in.Connection, method, in.Path, template)
	if allowed {
		return nil
	}
	msg := "not authorized for this method/path on this connection"
	if reason != "" {
		msg = msg + ": " + reason
	}
	return errors.New(msg)
}

// newHTTPClient builds the per-connection HTTP client. Redirects
// are explicitly disallowed so the toolkit does not blindly
// re-issue a request (and re-attach the connection's credential)
// to a host the operator did not authorize. The model can follow
// redirects manually by reading the upstream Location header from
// the response and issuing a new api_invoke_endpoint call with the
// redirected URL.
//
// Metrics wrapping is applied by the caller (see
// instrumentClient) rather than here so test helpers can construct
// a bare client without threading a metrics handle through every
// call site.
//
// TLS-config build errors are intentionally not surfaced from this
// constructor. ParseConfig has already validated cert + key + CA
// bundle, so buildTLSConfig only fails here if a caller has
// constructed a Config by hand and bypassed Validate. The fallback
// returns a transport with the system default tls.Config and the
// first outbound call will fail loudly with the underlying tls
// error, which is the same surface a misconfigured transport would
// produce on any other auth mode.
func newHTTPClient(cfg Config) *http.Client {
	return &http.Client{
		Timeout:   cfg.CallTimeout,
		Transport: newHTTPTransport(cfg),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// instrumentClient wraps client.Transport with the metrics-recording
// transport when metrics is enabled. No-op otherwise so test helpers
// that build a bare client continue to compile without changes.
//
// Idempotent: if client.Transport is already a *instrumentedTransport
// with the same connection name, the call is a no-op. This prevents
// double-wrapping (and therefore double-recording) when SetMetrics
// runs against connections that were already instrumented at
// construction time.
func instrumentClient(client *http.Client, connection string, metrics *observability.Metrics) {
	if client == nil {
		return
	}
	if existing, ok := client.Transport.(*instrumentedTransport); ok {
		if existing.connection == connection && existing.metrics == metrics {
			return
		}
	}
	client.Transport = newInstrumentedTransport(client.Transport, connection, metrics)
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
//
// When the connection carries mTLS material (cfg.MTLSClientCertPEM
// + cfg.MTLSClientKeyPEM) or a custom CA bundle (cfg.TLSCABundlePEM),
// the transport's TLSClientConfig is populated accordingly. With
// neither set, TLSClientConfig stays nil and Go's net/http uses
// system defaults. buildTLSConfig errors here are degraded to nil
// (see newHTTPClient for the rationale).
func newHTTPTransport(cfg Config) *http.Transport {
	t := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: cfg.ConnectTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       idleConnectionTimeout,
		MaxIdleConns:          maxIdleConnections,
	}
	if tlsCfg, err := buildTLSConfig(cfg); err == nil && tlsCfg != nil {
		t.TLSClientConfig = tlsCfg
	}
	return t
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
