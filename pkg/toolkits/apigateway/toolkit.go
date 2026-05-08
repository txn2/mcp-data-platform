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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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

	logKeyConnection = "connection"
	logKeyError      = "error"
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

	mu          sync.RWMutex
	connections map[string]*conn
	routePolicy RoutePolicy

	semanticProvider semantic.Provider
	queryProvider    query.Provider
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
// connection: parsed config, the Authenticator implementing its auth
// mode, and a per-connection HTTP client whose Transport tunes
// idle-connection behavior independently from other connections.
type conn struct {
	cfg    Config
	auth   Authenticator
	client *http.Client
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

// RegisterTools registers api_invoke_endpoint with the MCP server.
// Future PRs add api_list_endpoints and api_get_endpoint_schema
// under the same toolkit (see RFC #364).
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
}

// Tools returns the list of tool names this toolkit registers.
func (*Toolkit) Tools() []string {
	return []string{ToolInvokeEndpoint}
}

// SetSemanticProvider stores the semantic provider. Reserved for
// future enrichment (e.g. response shaping driven by DataHub PII
// tags, see issue #373); not consumed in v1.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.semanticProvider = provider
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
// connection under lock.
func (t *Toolkit) addParsedConnection(name string, cfg Config) error {
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		return fmt.Errorf("apigateway: %s: %w", name, err)
	}
	c := &conn{
		cfg:    cfg,
		auth:   auth,
		client: newHTTPClient(cfg),
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.connections[name]; exists {
		return fmt.Errorf("apigateway: %s: %w", name, ErrConnectionExists)
	}
	t.connections[name] = c
	return nil
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
