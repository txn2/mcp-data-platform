package persona

import (
	"fmt"
	"path/filepath"
	"slices"
	"sync"
)

// Registry manages persona definitions.
type Registry struct {
	mu sync.RWMutex

	personas       map[string]*Persona
	defaultPersona string
}

// NewRegistry creates a new persona registry.
func NewRegistry() *Registry {
	return &Registry{
		personas: make(map[string]*Persona),
	}
}

// Register adds a persona to the registry.
func (r *Registry) Register(p *Persona) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p.Name == "" {
		return fmt.Errorf("persona name is required")
	}
	if err := validateAPIRoutes(p.APIRoutes); err != nil {
		return fmt.Errorf("persona %q: %w", p.Name, err)
	}

	r.personas[p.Name] = p
	return nil
}

// validateAPIRoutes catches misconfigurations in APIRoutes at
// registration time so a typo can't silently disable a deny rule
// (matchPattern returns false on filepath.ErrBadPattern, which is
// the safe default for allow rules but lets a malformed deny rule
// fail open). Also rejects an empty Connection — required per the
// APIRouteRule docstring; without this check matchingRouteRules
// would silently skip the rule and the operator would see no error.
func validateAPIRoutes(rules []APIRouteRule) error {
	for i, rule := range rules {
		if rule.Connection == "" {
			return fmt.Errorf("api_routes[%d]: Connection is required", i)
		}
		if _, err := filepath.Match(rule.Connection, ""); err != nil {
			return fmt.Errorf("api_routes[%d]: invalid Connection glob %q: %w", i, rule.Connection, err)
		}
		for j, m := range rule.Methods {
			if _, err := filepath.Match(m, ""); err != nil {
				return fmt.Errorf("api_routes[%d].methods[%d]: invalid glob %q: %w", i, j, m, err)
			}
		}
		for j, p := range rule.Paths {
			if _, err := filepath.Match(p, ""); err != nil {
				return fmt.Errorf("api_routes[%d].paths[%d]: invalid glob %q: %w", i, j, p, err)
			}
		}
		switch rule.Action {
		case "", ActionAllow, ActionDeny:
		default:
			return fmt.Errorf("api_routes[%d]: invalid action %q (want %q or %q)", i, rule.Action, ActionAllow, ActionDeny)
		}
	}
	return nil
}

// Get retrieves a persona by name.
func (r *Registry) Get(name string) (*Persona, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.personas[name]
	return p, ok
}

// SetDefault sets the default persona name.
func (r *Registry) SetDefault(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultPersona = name
}

// GetDefault returns the default persona.
func (r *Registry) GetDefault() (*Persona, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.defaultPersona == "" {
		return nil, false
	}
	p, ok := r.personas[r.defaultPersona]
	return p, ok
}

// All returns all registered personas.
func (r *Registry) All() []*Persona {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Persona, 0, len(r.personas))
	for _, p := range r.personas {
		result = append(result, p)
	}
	return result
}

// GetForRoles returns the best matching persona for the given roles.
func (r *Registry) GetForRoles(roles []string) (*Persona, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var bestMatch *Persona
	bestPriority := -1

	for _, p := range r.personas {
		if matchesAnyRole(p.Roles, roles) {
			if p.Priority > bestPriority {
				bestMatch = p
				bestPriority = p.Priority
			}
		}
	}

	if bestMatch != nil {
		return bestMatch, true
	}

	// Fall back to default
	if r.defaultPersona != "" {
		p, ok := r.personas[r.defaultPersona]
		return p, ok
	}

	return nil, false
}

// Unregister removes a persona by name. Returns error if not found.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.personas[name]; !ok {
		return fmt.Errorf("persona %q not found", name)
	}
	delete(r.personas, name)
	if r.defaultPersona == name {
		r.defaultPersona = ""
	}
	return nil
}

// DefaultName returns the default persona name.
func (r *Registry) DefaultName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultPersona
}

// matchesAnyRole checks if any persona role matches any user role.
func matchesAnyRole(personaRoles, userRoles []string) bool {
	for _, pr := range personaRoles {
		if slices.Contains(userRoles, pr) {
			return true
		}
	}
	return false
}

// LoadFromConfig loads personas from a configuration map.
func (r *Registry) LoadFromConfig(config map[string]*Config) error {
	for name, cfg := range config {
		p := &Persona{
			Name:        name,
			DisplayName: cfg.DisplayName,
			Description: cfg.Description,
			Roles:       cfg.Roles,
			Tools: ToolRules{
				Allow: cfg.Tools.Allow,
				Deny:  cfg.Tools.Deny,
			},
			Connections: ConnectionRules{
				Allow: cfg.Connections.Allow,
				Deny:  cfg.Connections.Deny,
			},
			Context: ContextOverrides{
				DescriptionPrefix:         cfg.Context.DescriptionPrefix,
				DescriptionOverride:       cfg.Context.DescriptionOverride,
				AgentInstructionsSuffix:   cfg.Context.AgentInstructionsSuffix,
				AgentInstructionsOverride: cfg.Context.AgentInstructionsOverride,
			},
			Priority: cfg.Priority,
		}

		if err := r.Register(p); err != nil {
			return fmt.Errorf("registering persona %s: %w", name, err)
		}
	}
	return nil
}

// Config is the configuration format for personas.
type Config struct {
	DisplayName string                `yaml:"display_name"`
	Description string                `yaml:"description,omitempty"`
	Roles       []string              `yaml:"roles"`
	Tools       ToolRulesConfig       `yaml:"tools"`
	Connections ConnectionRulesConfig `yaml:"connections"`
	Context     ContextOverridesYAML  `yaml:"context"`
	Priority    int                   `yaml:"priority,omitempty"`
}

// ConnectionRulesConfig is the YAML configuration for connection rules.
type ConnectionRulesConfig struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// ToolRulesConfig is the YAML configuration for tool rules.
type ToolRulesConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// ContextOverridesYAML is the YAML configuration for context overrides.
type ContextOverridesYAML struct {
	DescriptionPrefix         string `yaml:"description_prefix,omitempty"`
	DescriptionOverride       string `yaml:"description_override,omitempty"`
	AgentInstructionsSuffix   string `yaml:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string `yaml:"agent_instructions_override,omitempty"`
}
