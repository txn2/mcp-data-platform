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
type ToolkitFactory func(name string, config map[string]interface{}) (Toolkit, error)

// ToolkitConfig holds configuration for a toolkit instance.
type ToolkitConfig struct {
	Kind    string
	Name    string
	Enabled bool
	Config  map[string]interface{}
	Default bool
}
