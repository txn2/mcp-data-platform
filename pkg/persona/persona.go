// Package persona provides persona-based access control and customization.
package persona

// defaultPersonaPriority is the default priority for built-in personas.
const defaultPersonaPriority = 100

// Persona defines a user persona with associated permissions and customizations.
type Persona struct {
	// Name is the unique identifier for this persona.
	Name string `json:"name" yaml:"name"`

	// DisplayName is the human-readable name.
	DisplayName string `json:"display_name" yaml:"display_name"`

	// Description describes this persona.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Roles are the roles that map to this persona. When a user authenticates
	// via OIDC, their token claims are mapped to roles. When using API keys,
	// roles are assigned directly. A user gets this persona when any of their
	// roles match.
	Roles []string `json:"roles" yaml:"roles"`

	// Tools defines tool access rules (allow/deny glob patterns).
	Tools ToolRules `json:"tools" yaml:"tools"`

	// Connections defines connection-level access rules. A tool call must pass
	// both the tool check and the connection check. If Connections.Allow is
	// empty, all connections are permitted (backward-compatible default).
	Connections ConnectionRules `json:"connections" yaml:"connections"`

	// Context defines per-persona overrides for the platform description and
	// agent instructions returned by the platform_info tool.
	Context ContextOverrides `json:"context" yaml:"context"`

	// Priority determines which persona takes precedence when a user's roles
	// match multiple personas. Higher values win. Default is 0; the built-in
	// admin persona uses 100.
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`

	// Source indicates where this persona was loaded from at runtime.
	// Values: "file" (YAML config), "database" (DB-managed), "both" (file
	// with DB override). This is runtime metadata — not persisted.
	Source string `json:"source,omitempty" yaml:"-"`
}

// ToolRules defines tool access rules for a persona.
type ToolRules struct {
	// Allow patterns for allowed tools (supports wildcards like "trino_*").
	Allow []string `json:"allow" yaml:"allow"`

	// Deny patterns for denied tools (takes precedence over Allow).
	Deny []string `json:"deny" yaml:"deny"`
}

// ConnectionRules defines connection-level access rules for a persona.
// These work alongside ToolRules — a tool call must pass both the tool
// check AND the connection check. If the Allow list is empty, all
// connections are permitted (backward-compatible default).
type ConnectionRules struct {
	// Allow patterns for allowed connections (supports wildcards like "prod-*").
	Allow []string `json:"allow,omitempty" yaml:"allow,omitempty"`

	// Deny patterns for denied connections (takes precedence over Allow).
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// ContextOverrides defines per-persona overrides for the description and
// agent instructions that the platform_info tool returns. These let you
// tailor what an AI agent sees based on who is using the platform.
//
// For each field pair (prefix/override), the override takes precedence.
// If an override is set, the prefix/suffix is ignored.
type ContextOverrides struct {
	// DescriptionPrefix is prepended to the server description (separated by
	// a blank line). Use this to add persona-specific context before the
	// base platform description. Ignored if DescriptionOverride is set.
	DescriptionPrefix string `json:"description_prefix,omitempty" yaml:"description_prefix,omitempty"`

	// DescriptionOverride replaces the server description entirely.
	// Use this when a persona needs a completely different description.
	DescriptionOverride string `json:"description_override,omitempty" yaml:"description_override,omitempty"`

	// AgentInstructionsSuffix is appended to the server agent instructions
	// (separated by a blank line). Use this to add persona-specific guidance
	// after the base instructions. Ignored if AgentInstructionsOverride is set.
	AgentInstructionsSuffix string `json:"agent_instructions_suffix,omitempty" yaml:"agent_instructions_suffix,omitempty"`

	// AgentInstructionsOverride replaces the server agent instructions entirely.
	// Use this when a persona needs completely different instructions.
	AgentInstructionsOverride string `json:"agent_instructions_override,omitempty" yaml:"agent_instructions_override,omitempty"`
}

// ApplyDescription returns the effective description for this persona.
// If DescriptionOverride is set, it replaces the base entirely.
// If DescriptionPrefix is set, it is prepended to the base.
// Otherwise the base is returned unchanged.
func (p *Persona) ApplyDescription(base string) string {
	if p.Context.DescriptionOverride != "" {
		return p.Context.DescriptionOverride
	}
	if p.Context.DescriptionPrefix != "" {
		if base == "" {
			return p.Context.DescriptionPrefix
		}
		return p.Context.DescriptionPrefix + "\n\n" + base
	}
	return base
}

// ApplyAgentInstructions returns the effective agent instructions for this persona.
// If AgentInstructionsOverride is set, it replaces the base entirely.
// If AgentInstructionsSuffix is set, it is appended to the base.
// Otherwise the base is returned unchanged.
func (p *Persona) ApplyAgentInstructions(base string) string {
	if p.Context.AgentInstructionsOverride != "" {
		return p.Context.AgentInstructionsOverride
	}
	if p.Context.AgentInstructionsSuffix != "" {
		if base == "" {
			return p.Context.AgentInstructionsSuffix
		}
		return base + "\n\n" + p.Context.AgentInstructionsSuffix
	}
	return base
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
		Connections: ConnectionRules{},
		Context:     ContextOverrides{},
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
		Connections: ConnectionRules{},
		Context:     ContextOverrides{},
		Priority:    defaultPersonaPriority,
	}
}
