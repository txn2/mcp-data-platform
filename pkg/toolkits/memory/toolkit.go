// Package memory provides the memory_manage and memory_capture MCP tools.
// Recall (reading memory back) is served by the unified search tool.
package memory

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const manageToolName = "memory_manage"

// Toolkit implements the memory management toolkit. Recall is handled by the
// unified search tool (#632); this toolkit owns only the memory_manage
// write path.
type Toolkit struct {
	name     string
	store    memstore.Store
	embedder embedding.Provider
	// threadLinker and recallChecker power memory_capture (#633); both optional.
	threadLinker  ThreadLinker
	recallChecker RecallChecker
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

// RegisterTools registers memory_manage with the MCP server. Recall moved to
// the unified search tool (#632).
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  manageToolName,
		Title: "Memory Manage",
		Description: "Manage the lifecycle of EXISTING persistent memory. " +
			"Commands: update, forget (archive), list, review_stale. " +
			"To CREATE memory or knowledge, use memory_capture (call it proactively to record corrections, " +
			"preferences, business context, and data-quality observations). " +
			"To find memory back, use search.",
		InputSchema: memoryManageSchema,
	}, t.handleManage)

	mcp.AddTool(s, &mcp.Tool{
		Name:  memoryCaptureToolName,
		Title: "Capture Knowledge",
		Description: "Record knowledge so it is never lost or re-derived. Call this PROACTIVELY whenever you " +
			"learn something worth keeping; do not wait to be asked. Choose the sink-class via `type`: " +
			"personal_preference and episodic_event are live for you immediately; business_knowledge, " +
			"schema_entity (with entity_urns), and operational_rule are reviewed before promotion to a shared " +
			"catalog. Examples: 'stores close at 9pm' -> business_knowledge; 'the amount column excludes returns' " +
			"-> schema_entity. Capture is recall-first: a restatement of something already known supersedes it " +
			"instead of duplicating.",
		InputSchema: memoryCaptureSchema,
	}, t.handleMemoryCapture)
}

// Tools returns the list of tool names.
func (*Toolkit) Tools() []string {
	return []string{manageToolName, memoryCaptureToolName}
}

// SetSemanticProvider is a no-op: recall (which used lineage) moved to
// search, so the memory toolkit no longer needs the semantic provider.
func (*Toolkit) SetSemanticProvider(_ semantic.Provider) {}

// SetQueryProvider is a no-op; memory toolkit does not use query execution.
func (*Toolkit) SetQueryProvider(_ query.Provider) {}

// Close releases resources.
func (*Toolkit) Close() error { return nil }

// Verify interface compliance.
var _ registry.Toolkit = (*Toolkit)(nil)
