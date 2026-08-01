package mcpapps

import (
	"sync"
)

// Registry manages registered MCP Apps.
type Registry struct {
	mu   sync.RWMutex
	apps map[string]*AppDefinition
	// toolToApp maps tool names to their associated app
	toolToApp map[string]*AppDefinition
}

// NewRegistry creates a new app registry.
func NewRegistry() *Registry {
	return &Registry{
		apps:      make(map[string]*AppDefinition),
		toolToApp: make(map[string]*AppDefinition),
	}
}

// Register adds an app to the registry.
// Returns an error if validation fails or if an app with the same name exists.
func (r *Registry) Register(app *AppDefinition) error {
	if err := app.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.apps[app.Name]; exists {
		return ErrAppAlreadyRegistered
	}

	r.apps[app.Name] = app

	// Build tool -> app mapping
	for _, toolName := range app.ToolNames {
		r.toolToApp[toolName] = app
	}

	return nil
}

// Get retrieves an app by name.
// Returns nil if not found.
func (r *Registry) Get(name string) *AppDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.apps[name]
}

// GetForTool retrieves the app associated with a tool name.
// Returns nil if no app is registered for the tool.
func (r *Registry) GetForTool(toolName string) *AppDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.toolToApp[toolName]
}

// HasApps returns true if any apps are registered.
func (r *Registry) HasApps() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.apps) > 0
}

// Apps returns all registered apps.
func (r *Registry) Apps() []*AppDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	apps := make([]*AppDefinition, 0, len(r.apps))
	for _, app := range r.apps {
		apps = append(apps, app)
	}
	return apps
}

// ToolNames returns all tool names that have associated apps.
func (r *Registry) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.toolToApp))
	for name := range r.toolToApp {
		names = append(names, name)
	}
	return names
}
