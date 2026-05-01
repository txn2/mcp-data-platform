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

// Log key constants keep structured-slog field names consistent
// across the package — and across packages that compose with this
// one. LogKeyTokenURLHost is exported so the admin handler that
// performs the OAuth code-exchange (which lives outside this package)
// can use the same field name as the token-source's refresh /
// acquire logs, allowing operators to grep one connection's full
// auth lifecycle by `token_url_host=<host>`.
const (
	logKeyConnection = "connection"
	logKeyEndpoint   = "endpoint"
	logKeyError      = "error"

	// LogKeyTokenURLHost is the structured-log field name used when
	// emitting an IdP host. Exported so external packages don't
	// duplicate the literal and risk drift.
	LogKeyTokenURLHost = "token_url_host" // #nosec G101 -- structured-log key name, not a credential

	// LogKeyGrantType is the structured-log field name used when
	// emitting an OAuth grant_type. Exported so the admin handler
	// (which performs the authorization_code exchange) and the
	// gateway token source (which performs refresh / acquire) emit
	// the same field name across the full OAuth lifecycle.
	LogKeyGrantType = "grant_type"
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
//
// Concurrency: the toolkit lock is HELD ONLY for the snapshot of
// placeholders to retry and again for the final pointer-swap on each
// success. The discover() network I/O happens with the lock RELEASED
// so concurrent Status / ListConnections / Tools / AddConnection
// callers don't block on potentially-slow upstream dials. The
// placeholder remains in the connections map throughout, so the
// admin UI keeps showing "Connect" while a retry is in flight.
func (t *Toolkit) SetTokenStore(s TokenStore) {
	type pending struct {
		name string
		cfg  Config
	}
	t.mu.Lock()
	t.tokenStore = s
	store := s
	var retry []pending
	for name, u := range t.connections {
		if u.client != nil ||
			u.config.AuthMode != AuthModeOAuth ||
			u.config.OAuth.Grant != OAuthGrantAuthorizationCode {
			continue
		}
		retry = append(retry, pending{name: name, cfg: u.config})
	}
	t.mu.Unlock()

	// Retries run concurrently — each placeholder's discover() is bounded
	// by cfg.ConnectTimeout, so a single sick upstream no longer blocks
	// the others from coming up. installLiveConnection is concurrency-
	// safe (it re-acquires the toolkit lock for its atomic pointer swap).
	var wg sync.WaitGroup
	for _, p := range retry {
		wg.Add(1)
		go func(p pending) {
			defer wg.Done()
			ctx, cancel := dialContext(p.cfg)
			defer cancel()
			client, tools, err := discover(ctx, p.cfg, p.name, store)
			if err != nil {
				slog.Warn("gateway: retry placeholder after token store wired",
					logKeyConnection, p.cfg.ConnectionName,
					logKeyError, err)
				// Update the placeholder's lastError so Status() reflects
				// the most recent rejection reason. Without this, the UI
				// would still show the original placeholder error from
				// AddConnection time, even after a retry surfaced a new
				// upstream rejection.
				t.recordPlaceholderError(p.name, err.Error())
				return
			}
			t.installLiveConnection(p.name, p.cfg, client, tools)
		}(p)
	}
	wg.Wait()
}

// dialContext returns a context bounded by cfg.ConnectTimeout (or the
// package default when cfg.ConnectTimeout is zero/negative). Used for
// every discover() call so a hung MCP-protocol handshake against an
// unhealthy upstream cannot block the caller (admin OAuth callback,
// SetTokenStore retry, AddConnection) for longer than the operator-
// configured timeout.
func dialContext(cfg Config) (context.Context, context.CancelFunc) {
	timeout := cfg.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

// recordPlaceholderError updates a placeholder's lastError so Status()
// reflects the most recent dial / discover failure. No-op when the
// connection no longer exists (concurrent removal) or has already been
// promoted to a live client.
//
// Takes the message as a string rather than an error so the call site
// owns formatting decisions (e.g. wrapping, context prefixing); the
// helper itself stores raw display text without further interpretation.
func (t *Toolkit) recordPlaceholderError(name, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.connections[name]
	if !ok || u.client != nil {
		return
	}
	u.lastError = msg
}

// installLiveConnection promotes a placeholder to a live upstream by
// swapping the connection-map pointer atomically under the lock. If
// the connection was removed concurrently (or already replaced by
// another retry), the freshly-dialed client is closed and the result
// discarded — last-writer-wins for the rare race.
func (t *Toolkit) installLiveConnection(name string, cfg Config, client *upstreamClient, tools []*mcp.Tool) {
	t.mu.Lock()
	existing, ok := t.connections[name]
	if !ok || existing.client != nil {
		t.mu.Unlock()
		// Connection was removed or already promoted; drop our work.
		if cerr := client.close(); cerr != nil {
			slog.Warn("gateway: discarded retry client close error",
				logKeyConnection, cfg.ConnectionName,
				logKeyError, cerr)
		}
		return
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
	t.mu.Unlock()
	slog.Info("gateway: upstream connected",
		logKeyConnection, cfg.ConnectionName,
		logKeyEndpoint, cfg.Endpoint,
		"tools", len(u.toolNames))
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
	// lastError captures the most recent dial / discover error when the
	// connection is in placeholder state (client == nil). Surfaced via
	// Status().OAuth.LastError so the admin UI can show the operator the
	// actual upstream rejection reason — instead of leaving them to guess
	// at a silent "awaiting reauth" warning.
	//
	// Lifecycle: set by addParsedConnection at placeholder creation time
	// and refreshed by recordPlaceholderError when SetTokenStore's retry
	// path produces a new failure. On successful promotion to live,
	// installLiveConnection REPLACES the entire upstream struct with a
	// fresh value (so lastError starts at the zero string ""); there is
	// no in-place clear. Live connections never set or read this field.
	lastError string

	// claiming is set briefly while addParsedConnection is performing
	// network I/O (discover) WITHOUT holding the toolkit's mutex. The
	// entry exists in t.connections so concurrent AddConnection calls
	// for the same name see ErrConnectionExists, but other readers
	// (Status, ListConnections, Tools) treat this as a placeholder.
	// Cleared when addParsedConnection completes (entry replaced with
	// either a live connection or a real placeholder).
	claiming bool
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

// addParsedConnection runs the dial + registration. The toolkit's mutex
// is held ONLY for the brief slot-claim and slot-install steps; the
// network I/O (dial + ListTools) runs WITHOUT the lock so other
// operations (Status, ListConnections, Tools, RemoveConnection of a
// different name) proceed in parallel.
//
// A "claiming" sentinel entry is inserted in the connections map before
// the dial so concurrent AddConnection calls for the same name see
// ErrConnectionExists. The claim sentinel is identified by POINTER
// IDENTITY (not just the claiming bool) so that a Remove + Add cycle
// during a slow dial cannot install our result into a later caller's
// slot — see installDialResult.
func (t *Toolkit) addParsedConnection(name string, cfg Config) error {
	claim, err := t.claimConnectionSlot(name, cfg)
	if err != nil {
		return err
	}
	// Network I/O happens here, OUTSIDE the lock. A slow or hung
	// upstream blocks only this call — other toolkit operations
	// (Status polls, listing connections, calling tools on other
	// connections) proceed without contention.
	ctx, cancel := dialContext(cfg)
	defer cancel()
	client, tools, dialErr := discover(ctx, cfg, name, t.tokenStore)
	return t.installDialResult(dialResult{
		claim:   claim,
		name:    name,
		cfg:     cfg,
		client:  client,
		tools:   tools,
		dialErr: dialErr,
	})
}

// claimConnectionSlot inserts a sentinel "claiming" entry under the
// lock so concurrent AddConnection calls for the same name see
// ErrConnectionExists. Returns ErrConnectionExists if a real or
// claiming entry already occupies the slot. The returned *upstream is
// the inserted sentinel — callers MUST pass it to installDialResult
// so the install step can verify the slot still holds OUR claim
// (and not a fresh claim from a concurrent Remove + Add cycle).
func (t *Toolkit) claimConnectionSlot(name string, cfg Config) (*upstream, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.connections[name]; exists {
		return nil, fmt.Errorf("gateway: %s: %w", name, ErrConnectionExists)
	}
	claim := &upstream{
		name:     name,
		config:   cfg,
		desc:     "Connecting to " + cfg.Endpoint,
		claiming: true,
	}
	t.connections[name] = claim
	return claim, nil
}

// installDialResult finishes a claim by replacing the "claiming"
// sentinel with the dial's result. Holds the lock only briefly to
// mutate the map and register tools.
//
// Identity check: the install only proceeds if the slot still holds
// the SAME *upstream pointer that claimConnectionSlot inserted. A
// Remove + Add cycle during our dial replaces the entry with a fresh
// claim sentinel that has the same `claiming` bool but a different
// pointer; without this check, our (potentially stale) dial result
// would silently overwrite the second caller's claim.
// dialResult bundles the inputs to installDialResult so the function
// signature stays under revive's argument-limit ceiling without losing
// any of the data the install step needs.
type dialResult struct {
	claim   *upstream
	name    string
	cfg     Config
	client  *upstreamClient
	tools   []*mcp.Tool
	dialErr error
}

func (t *Toolkit) installDialResult(r dialResult) error {
	claim, name, cfg, client, tools, dialErr := r.claim, r.name, r.cfg, r.client, r.tools, r.dialErr
	t.mu.Lock()
	if t.connections[name] != claim {
		// Slot was removed (and possibly re-claimed) during our dial —
		// discard the result. The other caller's dial owns the slot.
		t.mu.Unlock()
		if client != nil {
			_ = client.close()
		}
		return nil
	}

	if dialErr != nil {
		// authorization_code connections without a stored token are
		// expected to fail discovery — keep a placeholder so the admin
		// status surface renders a Connect button. Other failure modes
		// drop the slot and bubble the error so retries can succeed.
		if cfg.AuthMode == AuthModeOAuth && cfg.OAuth.Grant == OAuthGrantAuthorizationCode {
			t.connections[name] = &upstream{
				name:      name,
				config:    cfg,
				desc:      "Awaiting OAuth authorization",
				lastError: dialErr.Error(),
			}
			t.mu.Unlock()
			slog.Warn("gateway: oauth authorization_code connection awaiting reauth",
				logKeyConnection, cfg.ConnectionName,
				logKeyEndpoint, cfg.Endpoint,
				logKeyError, dialErr)
			return nil
		}
		delete(t.connections, name)
		t.mu.Unlock()
		slog.Warn("gateway: upstream unavailable",
			logKeyConnection, cfg.ConnectionName,
			logKeyEndpoint, cfg.Endpoint,
			logKeyError, dialErr)
		return dialErr
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
	t.mu.Unlock()
	slog.Info("gateway: upstream connected",
		logKeyConnection, cfg.ConnectionName,
		logKeyEndpoint, cfg.Endpoint,
		"tools", len(u.toolNames))
	return nil
}

// RemoveConnection unregisters a connection's tools from the MCP server,
// closes its upstream session, and removes it from the toolkit.
//
// The toolkit's mutex is held only for the brief map mutation +
// server.RemoveTools call. The actual client.close() — which performs
// network I/O (DELETE the MCP session against the upstream) — runs
// WITHOUT the lock so a slow upstream cannot block other toolkit
// operations.
func (t *Toolkit) RemoveConnection(name string) error {
	t.mu.Lock()
	u, ok := t.connections[name]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf(errFmtConnection, name, ErrConnectionNotFound)
	}
	if t.server != nil && len(u.toolNames) > 0 {
		t.server.RemoveTools(u.toolNames...)
	}
	delete(t.connections, name)
	client := u.client
	connectionName := u.config.ConnectionName
	t.mu.Unlock()

	if client != nil {
		if err := client.close(); err != nil {
			slog.Warn("gateway: error closing upstream session",
				logKeyConnection, connectionName,
				logKeyError, err)
		}
	}
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
//
// For oauth connections the OAuth field is always populated when present:
// for live clients it reflects the live token source's state; for awaiting-
// reauth placeholders (client == nil) it is synthesized from the persisted
// token store so the admin UI can render the Connect button or show the
// "authorized but upstream unreachable" state without needing a successful
// upstream dial.
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
	switch {
	case u.client != nil && u.client.oauth != nil:
		s := u.client.oauth.Status()
		cs.OAuth = &s
	case u.config.AuthMode == AuthModeOAuth:
		// Placeholder for an oauth connection with no live client (e.g.
		// authorization_code awaiting first Connect, or a dial failure
		// post-restart). Build a status view from the persisted store so
		// the admin UI can drive the next step.
		src := newOAuthTokenSource(u.config.OAuth, name, t.tokenStore)
		s := src.Status()
		// Surface the placeholder's last dial error so the admin UI can
		// show the operator WHY the upstream rejected — instead of just
		// "needs reauth" with no clue what's broken. The token-source
		// Status() never sets LastError for a fresh placeholder (it has
		// no access/refresh attempts of its own to record), so the
		// placeholder's stored error wins here.
		if u.lastError != "" {
			s.LastError = u.lastError
		}
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
	slog.Info("gateway: IngestOAuthToken — start",
		logKeyConnection, in.Name,
		"authenticated_by", in.AuthenticatedBy,
		"access_token_len", len(in.AccessToken),
		"refresh_token_present", in.RefreshToken != "",
		"expires_in", in.ExpiresIn,
		"scope", in.Scope)
	t.mu.Lock()
	u, ok := t.connections[in.Name]
	if !ok {
		t.mu.Unlock()
		slog.Warn("gateway: IngestOAuthToken — connection not found",
			logKeyConnection, in.Name)
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
		slog.Error("gateway: IngestOAuthToken — IngestTokenResponse failed",
			logKeyConnection, in.Name, logKeyError, err)
		return fmt.Errorf("gateway: %s: ingest token: %w", in.Name, err)
	}
	slog.Debug("gateway: IngestOAuthToken — tokens persisted, rebuilding connection",
		logKeyConnection, in.Name)

	// Replace the connection with a fresh one so it gets re-dialed using
	// the now-valid tokens and its tools registered on the live server.
	if err := t.RemoveConnection(in.Name); err != nil && !errors.Is(err, ErrConnectionNotFound) {
		slog.Error("gateway: IngestOAuthToken — RemoveConnection failed",
			logKeyConnection, in.Name, logKeyError, err)
		return fmt.Errorf("gateway: %s: remove during reauth: %w", in.Name, err)
	}
	rawCfg := configToMap(cfg)
	if err := t.AddConnection(in.Name, rawCfg); err != nil {
		slog.Error("gateway: IngestOAuthToken — AddConnection failed",
			logKeyConnection, in.Name, logKeyError, err)
		return err
	}
	slog.Info("gateway: IngestOAuthToken — complete",
		logKeyConnection, in.Name,
		"authenticated_by", in.AuthenticatedBy)
	return nil
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
//
// Snapshots the live client pointers under the lock and closes them
// outside the lock so a slow upstream cannot block other toolkit
// operations during shutdown. A concurrent AddConnection that lands
// after the snapshot will leak its client at process exit — by design,
// Close is called once at process shutdown and external callers are
// expected to stop dispatching new AddConnection calls before invoking
// Close (the platform's lifecycle layer enforces this).
func (t *Toolkit) Close() error {
	t.mu.Lock()
	clients := make([]*upstreamClient, 0, len(t.connections))
	for _, u := range t.connections {
		if u.client != nil {
			clients = append(clients, u.client)
		}
	}
	t.mu.Unlock()

	var firstErr error
	for _, c := range clients {
		if err := c.close(); err != nil && firstErr == nil {
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
