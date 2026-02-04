// Package persona provides persona-based access control and customization.
package persona

// Persona defines a user persona with associated permissions and customizations.
type Persona struct {
	// Name is the unique identifier for this persona.
	Name string `json:"name" yaml:"name"`

	// DisplayName is the human-readable name.
	DisplayName string `json:"display_name" yaml:"display_name"`

	// Description describes this persona.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Roles are the roles that map to this persona.
	Roles []string `json:"roles" yaml:"roles"`

	// Tools defines tool access rules.
	Tools ToolRules `json:"tools" yaml:"tools"`

	// Prompts defines prompt customizations.
	Prompts PromptConfig `json:"prompts" yaml:"prompts"`

	// Hints provides tool-specific hints for the AI.
	Hints map[string]string `json:"hints,omitempty" yaml:"hints,omitempty"`

	// Priority determines which persona takes precedence.
	// Higher values have higher priority.
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`
}

// ToolRules defines tool access rules for a persona.
type ToolRules struct {
	// Allow patterns for allowed tools (supports wildcards like "trino_*").
	Allow []string `json:"allow" yaml:"allow"`

	// Deny patterns for denied tools (takes precedence over Allow).
	Deny []string `json:"deny" yaml:"deny"`
}

// PromptConfig defines prompt customizations for a persona.
type PromptConfig struct {
	// SystemPrefix is prepended to system prompts.
	SystemPrefix string `json:"system_prefix,omitempty" yaml:"system_prefix,omitempty"`

	// SystemSuffix is appended to system prompts.
	SystemSuffix string `json:"system_suffix,omitempty" yaml:"system_suffix,omitempty"`

	// Instructions are additional instructions for this persona.
	Instructions string `json:"instructions,omitempty" yaml:"instructions,omitempty"`
}

// DefaultPersona creates a default persona that denies all access.
// This ensures fail-closed behavior - users must be explicitly granted access.
func DefaultPersona() *Persona {
	return &Persona{
		Name:        "default",
		DisplayName: "Default User (No Access)",
		Description: "Default persona with no access - configure explicit personas for access",
		Roles:       []string{},
		Tools: ToolRules{
			Allow: []string{},    // DENY BY DEFAULT
			Deny:  []string{"*"}, // EXPLICIT DENY ALL
		},
		Prompts: PromptConfig{},
		Hints:   make(map[string]string),
	}
}

// AdminPersona creates an admin persona with full access.
func AdminPersona() *Persona {
	return &Persona{
		Name:        "admin",
		DisplayName: "Administrator",
		Description: "Full access to all tools and features",
		Roles:       []string{"admin"},
		Tools: ToolRules{
			Allow: []string{"*"},
			Deny:  []string{},
		},
		Prompts:  PromptConfig{},
		Hints:    make(map[string]string),
		Priority: 100,
	}
}

// GetFullSystemPrompt returns the complete system prompt by combining
// SystemPrefix, Instructions, and SystemSuffix.
func (p *Persona) GetFullSystemPrompt() string {
	var parts []string

	if p.Prompts.SystemPrefix != "" {
		parts = append(parts, p.Prompts.SystemPrefix)
	}
	if p.Prompts.Instructions != "" {
		parts = append(parts, p.Prompts.Instructions)
	}
	if p.Prompts.SystemSuffix != "" {
		parts = append(parts, p.Prompts.SystemSuffix)
	}

	if len(parts) == 0 {
		return ""
	}

	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += "\n\n" + parts[i]
	}
	return result
}
