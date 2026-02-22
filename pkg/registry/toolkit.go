// Package registry provides toolkit registration and management.
package registry

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Toolkit is the interface that all composable toolkits must implement.
type Toolkit interface {
	// Kind returns the toolkit type (e.g., "trino", "datahub", "s3").
	Kind() string

	// Name returns the instance name from config.
	Name() string

	// Connection returns the connection name for audit logging.
	// This identifies the specific backend connection (e.g., "prod-trino", "main-datahub").
	Connection() string

	// RegisterTools registers all tools with the MCP server.
	RegisterTools(s *mcp.Server)

	// Tools returns a list of tool names provided by this toolkit.
	Tools() []string

	// SetSemanticProvider sets the semantic metadata provider for enrichment.
	SetSemanticProvider(provider semantic.Provider)

	// SetQueryProvider sets the query execution provider for enrichment.
	SetQueryProvider(provider query.Provider)

	// Close releases resources.
	Close() error
}

// ToolkitFactory creates a toolkit from configuration.
type ToolkitFactory func(name string, config map[string]any) (Toolkit, error)

// AggregateToolkitFactory creates a single toolkit from multiple instance configs.
// Used for toolkit kinds that support multi-connection routing internally
// (e.g., Trino with multiserver.Manager).
type AggregateToolkitFactory func(defaultName string, instances map[string]map[string]any) (Toolkit, error)

// ToolkitConfig holds configuration for a toolkit instance.
type ToolkitConfig struct {
	Kind    string
	Name    string
	Enabled bool
	Config  map[string]any
	Default bool
}
