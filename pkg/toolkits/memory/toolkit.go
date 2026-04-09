// Package memory provides the memory_manage and memory_recall MCP tools.
package memory

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	manageToolName = "memory_manage"
	recallToolName = "memory_recall"
)

// Toolkit implements the memory management toolkit.
type Toolkit struct {
	name             string
	store            memstore.Store
	embedder         embedding.Provider
	semanticProvider semantic.Provider
}

// New creates a new memory toolkit.
func New(name string, store memstore.Store, embedder embedding.Provider) (*Toolkit, error) {
	if store == nil {
		store = memstore.NewNoopStore()
	}
	if embedder == nil {
		embedder = embedding.NewNoopProvider(embedding.DefaultDimension)
	}

	return &Toolkit{
		name:     name,
		store:    store,
		embedder: embedder,
	}, nil
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string { return "memory" }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the connection name for audit logging.
func (*Toolkit) Connection() string { return "" }

// RegisterTools registers memory_manage and memory_recall with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  manageToolName,
		Title: "Memory Manage",
		Description: "Manages persistent agent/analyst memory. Commands: remember (create), update, forget (archive), list, review_stale. " +
			"Memories persist across sessions, scoped by user and persona. " +
			"Supports LOCOMO dimensions: knowledge, event, entity, relationship, preference.",
		InputSchema: memoryManageSchema,
	}, t.handleManage)

	mcp.AddTool(s, &mcp.Tool{
		Name:  recallToolName,
		Title: "Memory Recall",
		Description: "Retrieves relevant memories using multi-strategy search. Strategies: entity (URN lookup), " +
			"semantic (vector similarity), graph (DataHub lineage traversal), auto (combined). " +
			"Use when you need context from prior sessions that isn't automatically injected.",
		InputSchema: memoryRecallSchema,
	}, t.handleRecall)
}

// Tools returns the list of tool names.
func (*Toolkit) Tools() []string {
	return []string{manageToolName, recallToolName}
}

// SetSemanticProvider sets the semantic metadata provider for graph traversal.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
}

// SetQueryProvider is a no-op; memory toolkit does not use query execution.
func (*Toolkit) SetQueryProvider(_ query.Provider) {}

// Close releases resources.
func (*Toolkit) Close() error { return nil }

// Verify interface compliance.
var _ registry.Toolkit = (*Toolkit)(nil)
