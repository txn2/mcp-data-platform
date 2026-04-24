package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Toolkit is a gateway that re-exposes tools from an upstream MCP server
// through the platform's registry, auth, persona, and audit pipeline.
//
// Startup is failure-isolated: if the upstream is unreachable or rejects
// tool discovery, the toolkit is constructed with zero tools and a nil
// client. It will not crash the platform and will not interfere with
// other toolkits. Recovery requires restart or an admin-triggered refresh
// (not part of v1).
type Toolkit struct {
	name   string
	config Config
	client *upstreamClient
	tools  []forwardedTool

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// forwardedTool pairs the remote tool definition with its local namespaced name.
type forwardedTool struct {
	localName  string
	remoteName string
	definition *mcp.Tool
}

// New builds a Toolkit for the given upstream connection.
//
// The constructor synchronously dials the upstream and lists its tools,
// bounded by Config.ConnectTimeout. A failure at any step is logged and
// absorbed: the returned Toolkit is valid, exposes no tools, and holds a
// nil client. Callers never see a connection error from this constructor.
func New(name string, cfg Config) *Toolkit {
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}

	t := &Toolkit{
		name:   name,
		config: cfg,
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	client, remoteTools, err := discover(ctx, cfg)
	if err != nil {
		slog.Warn("gateway: upstream unavailable at startup",
			"connection", cfg.ConnectionName,
			"endpoint", cfg.Endpoint,
			"error", err)
		return t
	}

	t.client = client
	t.tools = namespaceTools(cfg.ConnectionName, remoteTools)
	slog.Info("gateway: upstream connected",
		"connection", cfg.ConnectionName,
		"endpoint", cfg.Endpoint,
		"tools", len(t.tools))
	return t
}

// discover dials the upstream and fetches its tool catalog in one step.
func discover(ctx context.Context, cfg Config) (*upstreamClient, []*mcp.Tool, error) {
	client, err := dial(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	remoteTools, err := client.listTools(ctx)
	if err != nil {
		_ = client.close()
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}
	return client, remoteTools, nil
}

// namespaceTools builds the local tool list by prefixing each remote name
// with the connection name and the separator ("__").
func namespaceTools(connection string, remote []*mcp.Tool) []forwardedTool {
	out := make([]forwardedTool, 0, len(remote))
	for _, rt := range remote {
		if rt == nil || rt.Name == "" {
			continue
		}
		out = append(out, forwardedTool{
			localName:  connection + NamespaceSeparator + rt.Name,
			remoteName: rt.Name,
			definition: rt,
		})
	}
	return out
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return Kind
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the connection name used in audit logs and persona rules.
func (t *Toolkit) Connection() string {
	return t.config.ConnectionName
}

// Tools returns the local (namespaced) tool names exposed by this toolkit.
// Empty when the upstream was unreachable at construction.
func (t *Toolkit) Tools() []string {
	out := make([]string, len(t.tools))
	for i, ft := range t.tools {
		out[i] = ft.localName
	}
	return out
}

// RegisterTools wires each discovered upstream tool into the server with a
// forwarding handler. Schemas are passed through verbatim.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	for _, ft := range t.tools {
		local := &mcp.Tool{
			Name:        ft.localName,
			Description: ft.definition.Description,
			InputSchema: ft.definition.InputSchema,
			Title:       ft.definition.Title,
			Annotations: ft.definition.Annotations,
		}
		s.AddTool(local, t.makeForwarder(ft.remoteName))
	}
}

// makeForwarder returns a handler that forwards a single proxied tool call
// to the upstream session. Transport errors are returned as tool-error
// results prefixed "upstream:<connection>:" so they flow through the audit
// pipeline like any other error.
func (t *Toolkit) makeForwarder(remoteName string) mcp.ToolHandler {
	connection := t.config.ConnectionName
	callTimeout := t.config.CallTimeout
	client := t.client
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

// SetSemanticProvider accepts the semantic provider used by enrichment
// middleware. The gateway does not consume it directly in v1.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
}

// SetQueryProvider accepts the query provider used by enrichment middleware.
// The gateway does not consume it directly in v1.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.queryProvider = provider
}

// Close releases the upstream session.
func (t *Toolkit) Close() error {
	if t.client == nil {
		return nil
	}
	return t.client.close()
}

// Verify interface compliance at compile time.
var _ interface {
	Kind() string
	Name() string
	Connection() string
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	Close() error
} = (*Toolkit)(nil)
