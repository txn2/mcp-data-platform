package registry

import (
	"fmt"
	"slices"
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

	// ordered holds the same toolkits in kind+name order. It is maintained on
	// registration rather than derived per read because every tool call walks
	// it (GetToolkitForTool) and the order has to be stable across calls; see
	// All.
	ordered []Toolkit

	// Factory functions by kind
	factories map[string]ToolkitFactory

	// Aggregate factory functions by kind (multi-instance → single toolkit)
	aggregateFactories map[string]AggregateToolkitFactory

	// Providers for cross-enrichment
	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// NewRegistry creates a new toolkit registry.
func NewRegistry() *Registry {
	return &Registry{
		toolkits:           make(map[string]Toolkit),
		factories:          make(map[string]ToolkitFactory),
		aggregateFactories: make(map[string]AggregateToolkitFactory),
	}
}

// RegisterFactory registers a toolkit factory for a kind.
func (r *Registry) RegisterFactory(kind string, factory ToolkitFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = factory
}

// RegisterAggregateFactory registers an aggregate toolkit factory for a kind.
// Aggregate factories receive all instance configs and produce a single toolkit
// that handles multi-connection routing internally.
func (r *Registry) RegisterAggregateFactory(kind string, factory AggregateToolkitFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aggregateFactories[kind] = factory
}

// GetAggregateFactory returns the aggregate factory for a kind, if registered.
func (r *Registry) GetAggregateFactory(kind string) (AggregateToolkitFactory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.aggregateFactories[kind]
	return f, ok
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
	r.reorder()
	return nil
}

// reorder rebuilds the ordered view after a registration. Callers hold the
// write lock. Registration happens at startup and on an admin hot-reload, so
// rebuilding costs nothing a reader would notice.
func (r *Registry) reorder() {
	keys := make([]string, 0, len(r.toolkits))
	for k := range r.toolkits {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	r.ordered = make([]Toolkit, 0, len(keys))
	for _, k := range keys {
		r.ordered = append(r.ordered, r.toolkits[k])
	}
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
	for _, toolkit := range r.ordered {
		if toolkit.Kind() == kind {
			result = append(result, toolkit)
		}
	}
	return result
}

// All returns all registered toolkits, ordered by kind then name.
//
// The order is part of the contract, not a convenience. Every surface that
// enumerates connections reads this list, and a caller resolving a connection
// NAME against it takes whichever entry it reaches first. Ranging a map here
// made that resolution pick a different toolkit on each call, so a deployment
// holding one connection name across several kinds reported a different kind on
// every page load (#1384).
func (r *Registry) All() []Toolkit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// A copy, and non-nil even when empty: the ordered view is the registry's
	// own state, and callers walk, filter, and sometimes append to what they
	// get back.
	out := make([]Toolkit, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// AllTools returns all tool names from all toolkits.
func (r *Registry) AllTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]string, 0, len(r.toolkits)*4)
	for _, toolkit := range r.ordered {
		tools = append(tools, toolkit.Tools()...)
	}
	return tools
}

// ToolkitMatch contains the result of matching a tool to its toolkit.
type ToolkitMatch struct {
	Kind       string
	Name       string
	Connection string
	Found      bool

	// ConnectionResolved is true when Connection was determined by the
	// toolkit's ConnectionResolver (per-tool routing) rather than from
	// the toolkit's default Connection(). When true, downstream
	// middleware MUST NOT override Connection from request arguments —
	// the toolkit owns the routing and a caller-supplied "connection"
	// arg is either ignored by the toolkit or attempts to spoof audit.
	ConnectionResolved bool
}

// GetToolkitForTool returns toolkit info (kind, name, connection) for a tool.
// Returns Found=false if the tool is not found in any registered toolkit.
func (r *Registry) GetToolkitForTool(toolName string) ToolkitMatch {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, toolkit := range r.ordered {
		if slices.Contains(toolkit.Tools(), toolName) {
			conn := toolkit.Connection()
			resolved := false
			if cr, ok := toolkit.(ConnectionResolver); ok {
				if c := cr.ConnectionForTool(toolName); c != "" {
					conn = c
					resolved = true
				}
			}
			return ToolkitMatch{
				Kind:               toolkit.Kind(),
				Name:               toolkit.Name(),
				Connection:         conn,
				Found:              true,
				ConnectionResolved: resolved,
			}
		}
	}
	return ToolkitMatch{}
}

// RegisterAllTools registers all tools from all toolkits with the MCP server,
// in the same order All reports them so two processes started from one config
// register in one order.
func (r *Registry) RegisterAllTools(s *mcp.Server) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, toolkit := range r.ordered {
		toolkit.RegisterTools(s)
	}
}

// Close closes all registered toolkits.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for _, toolkit := range r.ordered {
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
