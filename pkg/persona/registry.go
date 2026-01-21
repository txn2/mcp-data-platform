package persona

import (
	"fmt"
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

	r.personas[p.Name] = p
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

// matchesAnyRole checks if any persona role matches any user role.
func matchesAnyRole(personaRoles, userRoles []string) bool {
	for _, pr := range personaRoles {
		for _, ur := range userRoles {
			if pr == ur {
				return true
			}
		}
	}
	return false
}

// LoadFromConfig loads personas from a configuration map.
func (r *Registry) LoadFromConfig(config map[string]*PersonaConfig) error {
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
			Prompts: PromptConfig{
				SystemPrefix: cfg.Prompts.SystemPrefix,
				SystemSuffix: cfg.Prompts.SystemSuffix,
				Instructions: cfg.Prompts.Instructions,
			},
			Hints:    cfg.Hints,
			Priority: cfg.Priority,
		}

		if err := r.Register(p); err != nil {
			return fmt.Errorf("registering persona %s: %w", name, err)
		}
	}
	return nil
}

// PersonaConfig is the configuration format for personas.
type PersonaConfig struct {
	DisplayName string            `yaml:"display_name"`
	Description string            `yaml:"description,omitempty"`
	Roles       []string          `yaml:"roles"`
	Tools       ToolRulesConfig   `yaml:"tools"`
	Prompts     PromptConfigYAML  `yaml:"prompts"`
	Hints       map[string]string `yaml:"hints,omitempty"`
	Priority    int               `yaml:"priority,omitempty"`
}

// ToolRulesConfig is the YAML configuration for tool rules.
type ToolRulesConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// PromptConfigYAML is the YAML configuration for prompts.
type PromptConfigYAML struct {
	SystemPrefix string `yaml:"system_prefix,omitempty"`
	SystemSuffix string `yaml:"system_suffix,omitempty"`
	Instructions string `yaml:"instructions,omitempty"`
}
