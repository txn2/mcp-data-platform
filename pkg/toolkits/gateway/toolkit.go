package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// ErrConnectionExists is returned when AddConnection is called with a name
// already live in the toolkit.
var ErrConnectionExists = errors.New("gateway: connection already exists")

// ErrConnectionNotFound is returned when RemoveConnection is called for a
// name that is not present.
var ErrConnectionNotFound = errors.New("gateway: connection not found")

// Log key constants keep structured-slog field names consistent across the package.
const (
	logKeyConnection = "connection"
	logKeyEndpoint   = "endpoint"
	logKeyError      = "error"
)

// Toolkit is a gateway that proxies tools from one or more upstream MCP
// servers through the platform's registry, auth, persona, and audit pipeline.
//
// A single Toolkit manages multiple named upstream connections. Each upstream
// tool is re-exposed under a namespaced local name:
// "<connection_name>__<remote_tool_name>". Connections can be added or
// removed at runtime; the MCP server is notified of tool-list changes
// through AddTool / RemoveTools.
//
// Startup is failure-isolated: an unreachable upstream is logged and
// skipped — it does not block platform startup. Other connections remain
// functional.
type Toolkit struct {
	name        string
	defaultName string

	mu               sync.RWMutex
	server           *mcp.Server
	connections      map[string]*upstream
	enrichmentEngine *enrichment.Engine
	tokenStore       TokenStore

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// SetTokenStore wires a persistent OAuth token store into the gateway.
// Required for authorization_code grants to survive process restarts.
//
// When called after AddConnection, any authorization_code placeholder
// connections (those that were registered as "awaiting reauth" because
// the store wasn't yet wired) are automatically retried so persisted
// tokens are picked up without requiring a manual refresh. This makes
// startup wiring order-independent.
func (t *Toolkit) SetTokenStore(s TokenStore) {
	t.mu.Lock()
	t.tokenStore = s
	type pending struct {
		name string
		cfg  Config
	}
	var retry []pending
	for name, u := range t.connections {
		if u.client == nil &&
			u.config.AuthMode == AuthModeOAuth &&
			u.config.OAuth.Grant == OAuthGrantAuthorizationCode {
			retry = append(retry, pending{name: name, cfg: u.config})
			delete(t.connections, name)
		}
	}
	t.mu.Unlock()

	for _, p := range retry {
		if err := t.addParsedConnection(p.name, p.cfg); err != nil {
			slog.Warn("gateway: retry placeholder after token store wired",
				logKeyConnection, p.cfg.ConnectionName,
				logKeyError, err)
		}
	}
}

// SetEnrichmentEngine wires a cross-enrichment engine into this gateway.
// When set, every successful forwarded tool result is run through the
// engine before being returned to the client. Safe to call before or
// after RegisterTools — handlers fetch the current engine on each call.
func (t *Toolkit) SetEnrichmentEngine(e *enrichment.Engine) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enrichmentEngine = e
}

// upstream tracks a single live connection to a remote MCP server.
type upstream struct {
	name      string
	config    Config
	client    *upstreamClient
	tools     []*mcp.Tool // cached definitions from discovery
	toolNames []string
	desc      string
}

// New builds a Toolkit with the given default connection name and no
// initial connections.
func New(defaultName string) *Toolkit {
	return &Toolkit{
		name:        defaultName,
		defaultName: defaultName,
		connections: make(map[string]*upstream),
	}
}

// NewMulti builds a Toolkit and pre-loads the given parsed connection
// configs. Unreachable upstreams are logged and skipped so platform startup
// is never blocked.
func NewMulti(cfg MultiConfig) *Toolkit {
	name := cfg.DefaultName
	if name == "" {
		name = Kind
	}
	t := New(name)
	t.defaultName = cfg.DefaultName // may be empty; only "name" always set
	for instanceName, c := range cfg.Instances {
		if c.ConnectionName == "" {
			c.ConnectionName = instanceName
		}
		if err := t.addParsedConnection(instanceName, c); err != nil {
			slog.Warn("gateway: initial connection failed",
				logKeyConnection, instanceName, logKeyError, err)
		}
	}
	return t
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return Kind
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the default connection name used in audit logs when a
// request does not carry one. Empty if no default is configured.
func (t *Toolkit) Connection() string {
	return t.defaultName
}

// Tools returns the aggregate, sorted set of namespaced local tool names
// across all live connections.
func (t *Toolkit) Tools() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []string
	for _, u := range t.connections {
		out = append(out, u.toolNames...)
	}
	sort.Strings(out)
	return out
}

// ConnectionForTool maps a namespaced local tool name (e.g.
// "vendor__list_contacts") back to its source connection name. Used by
// the platform's audit middleware to attribute proxied tool calls to
// the specific upstream connection they were routed to.
func (t *Toolkit) ConnectionForTool(toolName string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, u := range t.connections {
		if slices.Contains(u.toolNames, toolName) {
			return u.config.ConnectionName
		}
	}
	return ""
}

// RegisterTools captures the server reference and registers every tool from
// every already-loaded connection. Must be called exactly once, after the
// toolkit is registered in the platform registry.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.server = s
	for _, u := range t.connections {
		if u.client == nil || len(u.tools) == 0 {
			continue
		}
		t.addToolsToServerLocked(u)
	}
}

// SetSemanticProvider stores the semantic provider (not consumed directly in v1).
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.semanticProvider = provider
}

// SetQueryProvider stores the query provider (not consumed directly in v1).
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queryProvider = provider
}

// AddConnection parses the raw config, dials the upstream, discovers its
// tools, and registers them on the server (if one is already set). The
// admin layer's hotAddConnection helper logs any returned error as a
// structured warning.
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

// addParsedConnection performs the shared dial + registration work under the
// toolkit's write lock.
func (t *Toolkit) addParsedConnection(name string, cfg Config) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.connections[name]; exists {
		return fmt.Errorf("gateway: %s: %w", name, ErrConnectionExists)
	}

	client, tools, err := discover(context.Background(), cfg, name, t.tokenStore)
	if err != nil {
		// authorization_code connections without a stored token are
		// expected to fail discovery — record a placeholder so the admin
		// status surface knows to render a Connect button. For other
		// failure modes, keep the existing behavior of bubbling the
		// error so retries from the admin path can succeed.
		if cfg.AuthMode == AuthModeOAuth && cfg.OAuth.Grant == OAuthGrantAuthorizationCode {
			slog.Warn("gateway: oauth authorization_code connection awaiting reauth",
				logKeyConnection, cfg.ConnectionName,
				logKeyEndpoint, cfg.Endpoint)
			t.connections[name] = &upstream{
				name:   name,
				config: cfg,
				desc:   "Awaiting OAuth authorization",
			}
			return nil
		}
		slog.Warn("gateway: upstream unavailable",
			logKeyConnection, cfg.ConnectionName,
			logKeyEndpoint, cfg.Endpoint,
			logKeyError, err)
		return err
	}

	u := &upstream{
		name:      name,
		config:    cfg,
		client:    client,
		tools:     tools,
		toolNames: makeLocalNames(cfg.ConnectionName, tools),
		desc:      "Gateway to " + cfg.Endpoint,
	}
	t.connections[name] = u
	if t.server != nil {
		t.addToolsToServerLocked(u)
	}
	slog.Info("gateway: upstream connected",
		logKeyConnection, cfg.ConnectionName,
		logKeyEndpoint, cfg.Endpoint,
		"tools", len(u.toolNames))
	return nil
}

// RemoveConnection unregisters a connection's tools from the MCP server,
// closes its upstream session, and removes it from the toolkit.
func (t *Toolkit) RemoveConnection(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	u, ok := t.connections[name]
	if !ok {
		return fmt.Errorf(errFmtConnection, name, ErrConnectionNotFound)
	}

	if t.server != nil && len(u.toolNames) > 0 {
		t.server.RemoveTools(u.toolNames...)
	}
	if u.client != nil {
		if err := u.client.close(); err != nil {
			slog.Warn("gateway: error closing upstream session",
				logKeyConnection, u.config.ConnectionName,
				logKeyError, err)
		}
	}
	delete(t.connections, name)
	return nil
}

// HasConnection reports whether a connection with the given name is live.
func (t *Toolkit) HasConnection(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.connections[name]
	return ok
}

// ConnectionStatus is a per-connection health snapshot exposed by the
// admin status endpoint.
type ConnectionStatus struct {
	Name     string       `json:"name"`
	Healthy  bool         `json:"healthy"`
	AuthMode string       `json:"auth_mode"`
	Tools    []string     `json:"tools,omitempty"`
	OAuth    *OAuthStatus `json:"oauth,omitempty"`
}

// Status returns a status snapshot for the named connection. Returns nil
// when the connection is not registered.
func (t *Toolkit) Status(name string) *ConnectionStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	u, ok := t.connections[name]
	if !ok {
		return nil
	}
	cs := &ConnectionStatus{
		Name:     name,
		Healthy:  u.client != nil,
		AuthMode: u.config.AuthMode,
		Tools:    append([]string(nil), u.toolNames...),
	}
	if u.client != nil && u.client.oauth != nil {
		s := u.client.oauth.Status()
		cs.OAuth = &s
	}
	return cs
}

// ReacquireOAuthToken forces a fresh client_credentials exchange for the
// named connection. Returns an error if the connection is missing or not
// configured for OAuth.
func (t *Toolkit) ReacquireOAuthToken(ctx context.Context, name string) error {
	t.mu.RLock()
	u, ok := t.connections[name]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf(errFmtConnection, name, ErrConnectionNotFound)
	}
	if u.client == nil || u.client.oauth == nil {
		return fmt.Errorf("gateway: %s: not configured for OAuth", name)
	}
	return u.client.oauth.Reacquire(ctx)
}

// IngestOAuthTokenInput is the parameter set for IngestOAuthToken.
// Defined as a struct to keep the public method's argument list under
// the project's revive limit.
type IngestOAuthTokenInput struct {
	Name            string
	AccessToken     string
	RefreshToken    string
	ExpiresIn       int
	Scope           string
	AuthenticatedBy string
}

// IngestOAuthToken stores tokens obtained from an authorization_code
// callback into the named connection's token source AND persists them
// via the toolkit's TokenStore. Triggers re-discovery of the upstream
// (re-dial + listTools) so the previously "needs reauth" connection
// becomes live with its discovered tools registered on the MCP server.
func (t *Toolkit) IngestOAuthToken(ctx context.Context, in IngestOAuthTokenInput) error {
	t.mu.Lock()
	u, ok := t.connections[in.Name]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf(errFmtConnection, in.Name, ErrConnectionNotFound)
	}
	cfg := u.config
	store := t.tokenStore
	t.mu.Unlock()

	// Even if the connection has no live client (the typical case for an
	// authorization_code connection awaiting Connect), build a temporary
	// token source bound to the same store, ingest the tokens, and let
	// the rebuild path below materialize the upstream client with the
	// fresh credentials.
	tmp := newOAuthTokenSource(cfg.OAuth, in.Name, store)
	if err := tmp.IngestTokenResponse(ctx, IngestTokenResponseInput{
		AccessToken:     in.AccessToken,
		RefreshToken:    in.RefreshToken,
		ExpiresIn:       in.ExpiresIn,
		Scope:           in.Scope,
		AuthenticatedBy: in.AuthenticatedBy,
	}); err != nil {
		return fmt.Errorf("gateway: %s: ingest token: %w", in.Name, err)
	}

	// Replace the connection with a fresh one so it gets re-dialed using
	// the now-valid tokens and its tools registered on the live server.
	if err := t.RemoveConnection(in.Name); err != nil && !errors.Is(err, ErrConnectionNotFound) {
		return fmt.Errorf("gateway: %s: remove during reauth: %w", in.Name, err)
	}
	rawCfg := configToMap(cfg)
	return t.AddConnection(in.Name, rawCfg)
}

// errFmtConnection is the standard "gateway: <name>: <wrapped err>"
// format. Centralized to satisfy revive's add-constant rule.
const errFmtConnection = "gateway: %s: %w"

// configToMap rebuilds the raw config map for a Config — used by the
// OAuth ingest path to round-trip a parsed Config back through
// AddConnection's parser. Only the fields that AddConnection reads need
// to round-trip; ConnectionName is set directly to preserve the original.
func configToMap(c Config) map[string]any {
	m := map[string]any{
		"endpoint":        c.Endpoint,
		"auth_mode":       c.AuthMode,
		"credential":      c.Credential,
		"connection_name": c.ConnectionName,
		"connect_timeout": c.ConnectTimeout.String(),
		"call_timeout":    c.CallTimeout.String(),
		"trust_level":     c.TrustLevel,
	}
	if c.OAuth.Grant != "" {
		m["oauth_grant"] = c.OAuth.Grant
		m["oauth_token_url"] = c.OAuth.TokenURL
		m["oauth_authorization_url"] = c.OAuth.AuthorizationURL
		m["oauth_client_id"] = c.OAuth.ClientID
		m["oauth_client_secret"] = c.OAuth.ClientSecret
		m["oauth_scope"] = c.OAuth.Scope
	}
	return m
}

// ListConnections returns metadata for every live connection, sorted by name.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]toolkit.ConnectionDetail, 0, len(t.connections))
	for name, u := range t.connections {
		out = append(out, toolkit.ConnectionDetail{
			Name:        name,
			Description: u.desc,
			IsDefault:   name == t.defaultName,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Close closes every upstream session. Safe to call on a never-registered
// toolkit.
func (t *Toolkit) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var firstErr error
	for _, u := range t.connections {
		if u.client == nil {
			continue
		}
		if err := u.client.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// addToolsToServerLocked registers each remote tool on the server under a
// namespaced local name. Caller must hold t.mu (write lock) and ensure
// t.server is non-nil.
func (t *Toolkit) addToolsToServerLocked(u *upstream) {
	for _, rt := range u.tools {
		if rt == nil || rt.Name == "" {
			continue
		}
		local := &mcp.Tool{
			Name:        u.config.ConnectionName + NamespaceSeparator + rt.Name,
			Description: rt.Description,
			InputSchema: rt.InputSchema,
			Title:       rt.Title,
			Annotations: rt.Annotations,
		}
		t.server.AddTool(local, t.makeForwarder(u, rt.Name, local.Name))
	}
}

// makeForwarder returns a handler that forwards the call upstream and, if
// an enrichment engine is configured, applies enrichment rules to the
// upstream response.
func (t *Toolkit) makeForwarder(u *upstream, remoteName, localName string) mcp.ToolHandler {
	connection := u.config.ConnectionName
	callTimeout := u.config.CallTimeout
	client := u.client
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if client == nil {
			return upstreamErr(connection, "upstream unavailable"), nil
		}
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()

		args := argumentsFromRequest(req)
		res, err := client.callTool(callCtx, remoteName, args)
		if err != nil {
			return upstreamErr(connection, err.Error()), nil
		}
		if !res.IsError {
			t.applyEnrichment(ctx, connection, localName, req, res)
		}
		return res, nil
	}
}

// applyEnrichment runs the configured engine against the upstream response.
// It mutates res.StructuredContent in place when enrichment fires; warnings
// are surfaced as additional TextContent entries appended to res.Content.
// A nil engine, missing structured input, or any internal failure leaves
// the result unchanged so enrichment is never load-bearing for correctness.
func (t *Toolkit) applyEnrichment(ctx context.Context, connection, localName string,
	req *mcp.CallToolRequest, res *mcp.CallToolResult,
) {
	t.mu.RLock()
	engine := t.enrichmentEngine
	t.mu.RUnlock()
	if engine == nil {
		return
	}

	input, ok := structuredInput(res)
	if !ok {
		return
	}

	call := enrichment.CallContext{
		Connection: connection,
		ToolName:   localName,
		Args:       argsFromRequest(req),
	}
	out := engine.Apply(ctx, call, input)
	if out.Response != nil {
		res.StructuredContent = out.Response
	}
	for _, w := range out.Warnings {
		res.Content = append(res.Content, &mcp.TextContent{Text: "warning: " + w})
	}
}

// structuredInput pulls a structured object out of an upstream
// CallToolResult, preferring StructuredContent and falling back to JSON
// embedded in the first TextContent.
func structuredInput(res *mcp.CallToolResult) (any, bool) {
	if res.StructuredContent != nil {
		return res.StructuredContent, true
	}
	if len(res.Content) == 0 {
		return nil, false
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

// argsFromRequest decodes the tool call's raw arguments into a generic
// any value for use as an evaluation-context input.
func argsFromRequest(req *mcp.CallToolRequest) any {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return nil
	}
	var parsed any
	if err := json.Unmarshal(req.Params.Arguments, &parsed); err != nil {
		return nil
	}
	return parsed
}

// makeLocalNames builds the namespaced tool-name slice for a set of
// discovered remote tools.
func makeLocalNames(connection string, remote []*mcp.Tool) []string {
	out := make([]string, 0, len(remote))
	for _, rt := range remote {
		if rt == nil || rt.Name == "" {
			continue
		}
		out = append(out, connection+NamespaceSeparator+rt.Name)
	}
	return out
}

// discover dials the upstream and fetches its tool catalog in one step,
// bounded by the config's ConnectTimeout.
func discover(ctx context.Context, cfg Config, connection string, store TokenStore) (*upstreamClient, []*mcp.Tool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	client, err := dial(dialCtx, cfg, connection, store)
	if err != nil {
		return nil, nil, err
	}
	remoteTools, err := client.listTools(dialCtx)
	if err != nil {
		_ = client.close()
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}
	return client, remoteTools, nil
}

// argumentsFromRequest extracts raw arguments from a tool request. Empty
// arguments are forwarded as nil so the outbound request omits the field.
func argumentsFromRequest(req *mcp.CallToolRequest) any {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return nil
	}
	return req.Params.Arguments
}

// upstreamErr wraps a message in a tool-error CallToolResult attributed to
// the given connection.
func upstreamErr(connection, msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("upstream:%s: %s", connection, msg),
		}},
	}
}

// ProbeTool is a summary of a single discovered upstream tool, used by the
// admin test endpoint to preview what a connection would expose.
type ProbeTool struct {
	Name        string `json:"name"`
	LocalName   string `json:"local_name"`
	Description string `json:"description,omitempty"`
}

// Probe dials the upstream described by cfg, lists its tools, and closes the
// session. It does NOT mutate any live toolkit state, so it's safe to call
// from the admin "test connection" endpoint before persisting a config.
func Probe(ctx context.Context, cfg Config) ([]ProbeTool, error) {
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = "probe"
	}
	// Probe is a one-shot dial — never persists tokens, so pass nil for
	// the store. authorization_code grants can't be probed without a
	// valid stored refresh token (which would require Connect first).
	client, tools, err := discover(ctx, cfg, cfg.ConnectionName, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.close() }()

	out := make([]ProbeTool, 0, len(tools))
	for _, rt := range tools {
		if rt == nil || rt.Name == "" {
			continue
		}
		out = append(out, ProbeTool{
			Name:        rt.Name,
			LocalName:   cfg.ConnectionName + NamespaceSeparator + rt.Name,
			Description: rt.Description,
		})
	}
	return out, nil
}

// Verify interface compliance at compile time.
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
