package registry

import (
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Registry manages toolkit registration and lifecycle.
type Registry struct {
	mu sync.RWMutex

	// Registered toolkits by kind+name
	toolkits map[string]Toolkit

	// Factory functions by kind
	factories map[string]ToolkitFactory

	// Providers for cross-injection
	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// NewRegistry creates a new toolkit registry.
func NewRegistry() *Registry {
	return &Registry{
		toolkits:  make(map[string]Toolkit),
		factories: make(map[string]ToolkitFactory),
	}
}

// RegisterFactory registers a toolkit factory for a kind.
func (r *Registry) RegisterFactory(kind string, factory ToolkitFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = factory
}

// SetSemanticProvider sets the semantic provider for all toolkits.
func (r *Registry) SetSemanticProvider(provider semantic.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.semanticProvider = provider

	for _, toolkit := range r.toolkits {
		toolkit.SetSemanticProvider(provider)
	}
}

// SetQueryProvider sets the query provider for all toolkits.
func (r *Registry) SetQueryProvider(provider query.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queryProvider = provider

	for _, toolkit := range r.toolkits {
		toolkit.SetQueryProvider(provider)
	}
}

// Register adds a toolkit to the registry.
func (r *Registry) Register(toolkit Toolkit) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := toolkitKey(toolkit.Kind(), toolkit.Name())
	if _, exists := r.toolkits[key]; exists {
		return fmt.Errorf("toolkit %s already registered", key)
	}

	// Inject providers for semantic/query context (used by enrichment middleware)
	if r.semanticProvider != nil {
		toolkit.SetSemanticProvider(r.semanticProvider)
	}
	if r.queryProvider != nil {
		toolkit.SetQueryProvider(r.queryProvider)
	}

	r.toolkits[key] = toolkit
	return nil
}

// CreateAndRegister creates a toolkit from config and registers it.
func (r *Registry) CreateAndRegister(cfg ToolkitConfig) error {
	r.mu.RLock()
	factory, ok := r.factories[cfg.Kind]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("unknown toolkit kind: %s", cfg.Kind)
	}

	toolkit, err := factory(cfg.Name, cfg.Config)
	if err != nil {
		return fmt.Errorf("creating toolkit %s/%s: %w", cfg.Kind, cfg.Name, err)
	}

	return r.Register(toolkit)
}

// Get retrieves a toolkit by kind and name.
func (r *Registry) Get(kind, name string) (Toolkit, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	toolkit, ok := r.toolkits[toolkitKey(kind, name)]
	return toolkit, ok
}

// GetByKind retrieves all toolkits of a kind.
func (r *Registry) GetByKind(kind string) []Toolkit {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Toolkit
	for key, toolkit := range r.toolkits {
		if toolkit.Kind() == kind {
			result = append(result, r.toolkits[key])
		}
	}
	return result
}

// All returns all registered toolkits.
func (r *Registry) All() []Toolkit {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Toolkit, 0, len(r.toolkits))
	for _, toolkit := range r.toolkits {
		result = append(result, toolkit)
	}
	return result
}

// AllTools returns all tool names from all toolkits.
func (r *Registry) AllTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]string, 0, len(r.toolkits)*4)
	for _, toolkit := range r.toolkits {
		tools = append(tools, toolkit.Tools()...)
	}
	return tools
}

// GetToolkitForTool returns toolkit info (kind, name, connection) for a tool.
// Returns found=false if the tool is not found in any registered toolkit.
func (r *Registry) GetToolkitForTool(toolName string) (kind, name, connection string, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, toolkit := range r.toolkits {
		for _, tool := range toolkit.Tools() {
			if tool == toolName {
				return toolkit.Kind(), toolkit.Name(), toolkit.Connection(), true
			}
		}
	}
	return "", "", "", false
}

// RegisterAllTools registers all tools from all toolkits with the MCP server.
func (r *Registry) RegisterAllTools(s *mcp.Server) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, toolkit := range r.toolkits {
		toolkit.RegisterTools(s)
	}
}

// Close closes all registered toolkits.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for _, toolkit := range r.toolkits {
		if err := toolkit.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing toolkits: %v", errs)
	}
	return nil
}

func toolkitKey(kind, name string) string {
	return kind + ":" + name
}
