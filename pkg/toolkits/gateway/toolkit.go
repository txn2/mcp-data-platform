package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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

	mu          sync.RWMutex
	server      *mcp.Server
	connections map[string]*upstream

	semanticProvider semantic.Provider
	queryProvider    query.Provider
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

	client, tools, err := discover(context.Background(), cfg)
	if err != nil {
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
		return fmt.Errorf("gateway: %s: %w", name, ErrConnectionNotFound)
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
		t.server.AddTool(local, u.makeForwarder(rt.Name))
	}
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

// makeForwarder returns a handler that forwards a single proxied tool call
// to this upstream's session.
func (u *upstream) makeForwarder(remoteName string) mcp.ToolHandler {
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
		return res, nil
	}
}

// discover dials the upstream and fetches its tool catalog in one step,
// bounded by the config's ConnectTimeout.
func discover(ctx context.Context, cfg Config) (*upstreamClient, []*mcp.Tool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	client, err := dial(dialCtx, cfg)
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
	client, tools, err := discover(ctx, cfg)
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
