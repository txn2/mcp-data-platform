// Package persona provides persona-based access control and customization.
package persona

// defaultPersonaPriority is the default priority for built-in personas.
const defaultPersonaPriority = 100

// personaNameAdmin is the canonical name of the built-in admin persona.
// Distinct from roleAdmin because persona names and OIDC role names
// live in different namespaces — the role comes from external IdP
// configuration, the persona name is internal — and a future
// deployment may need to rename one without renaming the other.
const personaNameAdmin = "admin"

// roleAdmin is the OIDC role name that maps to the admin persona.
// Today it equals personaNameAdmin; do not couple them.
const roleAdmin = "admin"

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

	// APIRoutes defines per-(connection, method, path) rules for the HTTP
	// API gateway toolkit (kind=api). Layered on top of Connections: if a
	// persona is allowed a connection at the tool/connection level, APIRoutes
	// can further restrict which HTTP methods and paths the model can invoke
	// against that connection. Personas with no APIRoutes entries for a
	// given connection get the existing connection-level behavior unchanged
	// (backward-compatible). See pkg/persona/filter.go IsAPIRouteAllowed.
	APIRoutes []APIRouteRule `json:"api_routes,omitempty" yaml:"api_routes,omitempty"`

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

// APIRouteRule constrains the HTTP API gateway's api_invoke_endpoint
// tool to specific (method, path) combinations on connections matched
// by Connection. A persona's APIRoutes list is consulted only for
// kind=api connections; other toolkit kinds ignore it.
//
// Semantics, evaluated against a single (connection, method, path) tuple:
//   - Rules whose Connection glob does not match are skipped.
//   - Among matching rules: any "deny" rule whose Methods+Paths match
//     denies the call (deny takes precedence).
//   - Otherwise, at least one "allow" rule whose Methods+Paths match
//     must be present for the call to be authorized.
//   - If NO rule matches the connection at all, the API route check is
//     a no-op — the existing ConnectionRules check is the sole gate
//     (backward-compatible behavior).
//
// Empty Methods or Paths slices mean "any" — a rule with empty Methods
// and Paths matches every method+path combination on the connection.
type APIRouteRule struct {
	// Connection is a glob (e.g. "crm-*") matched against the connection
	// name. Required.
	Connection string `json:"connection" yaml:"connection"`

	// Methods is a list of HTTP method globs (e.g. ["GET", "HEAD"]).
	// Empty = any method. Patterns are case-sensitive — typically
	// uppercase ("GET", "POST", etc.) since the toolkit normalizes
	// inbound methods to uppercase before this check runs.
	Methods []string `json:"methods,omitempty" yaml:"methods,omitempty"`

	// Paths is a list of path globs (e.g. ["/v1/users/*", "/v1/orders/**"]).
	// Empty = any path.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`

	// Action is "allow" (default) or "deny". Deny rules take precedence
	// over allow rules within the matching-Connection subset.
	Action string `json:"action,omitempty" yaml:"action,omitempty"`
}

// API route action values. Empty Action defaults to ActionAllow.
const (
	ActionAllow = "allow"
	ActionDeny  = "deny"
)

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
		Name:        personaNameAdmin,
		DisplayName: "Administrator",
		Description: "Full access to all tools and features",
		Roles:       []string{roleAdmin},
		Tools: ToolRules{
			Allow: []string{"*"},
			Deny:  []string{},
		},
		// Connections are deny-by-default (see ToolFilter.IsConnectionAllowed):
		// an empty Allow grants nothing. The admin persona allows "*" so it
		// reaches every connection, including the built-in platform-admin
		// self-connection.
		Connections: ConnectionRules{
			Allow: []string{"*"},
		},
		Context:  ContextOverrides{},
		Priority: defaultPersonaPriority,
	}
}
