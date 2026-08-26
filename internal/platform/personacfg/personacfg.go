// Package personacfg holds the YAML shape of a persona definition and the one
// conversion from it to the runtime persona the authorizer evaluates.
//
// It is a package of its own because pkg/platform is at its size budget and
// because the conversion had been written twice: once at startup and once in
// the admin API's revert-to-file path. The second copy is what dropped a
// persona's API route rules when its database override was deleted (#1479).
// The types are aliased back into pkg/platform, so a config file and every
// caller naming platform.PersonaDef are unchanged.
//
//nolint:revive // max-public-structs: this package's exported surface is one cohesive persona definition (the personas block, one persona, and the four rule/override shapes nested inside it), not a heap of unrelated types.
package personacfg

import "github.com/txn2/mcp-data-platform/pkg/persona"

// PersonasConfig holds persona definitions.
type PersonasConfig struct {
	Definitions map[string]PersonaDef `yaml:",inline"`
	// DefaultPersona is no longer honored: a caller whose roles match no
	// persona has no access. The field is still parsed so Validate can reject a
	// config that sets it, and — because Definitions is an inline map — so that
	// `default_persona: analyst` does not decode as a persona *named*
	// "default_persona".
	DefaultPersona string            `yaml:"default_persona"`
	RoleMapping    RoleMappingConfig `yaml:"role_mapping"`
}

// RoleMappingConfig configures role mapping.
type RoleMappingConfig struct {
	OIDCToPersona map[string]string `yaml:"oidc_to_persona"`
}

// PersonaDef defines a persona.
type PersonaDef struct {
	DisplayName string             `yaml:"display_name"`
	Description string             `yaml:"description,omitempty"`
	Roles       []string           `yaml:"roles"`
	Tools       ToolRulesDef       `yaml:"tools"`
	Connections ConnectionRulesDef `yaml:"connections"`
	// APIRoutes narrows which HTTP methods and paths this persona may
	// invoke on api-kind connections it already reaches. Optional: a
	// connection no rule names keeps the connection-level decision as its
	// only gate.
	APIRoutes []APIRouteDef `yaml:"api_routes,omitempty"`
	Context   ContextDef    `yaml:"context"`
	Priority  int           `yaml:"priority,omitempty"`
}

// APIRouteDef defines one per-(connection, method, path) rule for the HTTP API
// gateway in config. Mirrors persona.APIRouteRule.
type APIRouteDef struct {
	// Connection is a glob matched against the connection name.
	Connection string `yaml:"connection"`
	// Methods are HTTP method globs. Empty matches any method.
	Methods []string `yaml:"methods,omitempty"`
	// Paths are path globs, matched against both the path a call reaches and
	// the catalog template it resolved from. Empty matches any path.
	Paths []string `yaml:"paths,omitempty"`
	// Action is "allow" (the default) or "deny". Deny wins.
	Action string `yaml:"action,omitempty"`
}

// ToolRulesDef defines tool access rules.
type ToolRulesDef struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// ConnectionRulesDef defines connection access rules in config.
type ConnectionRulesDef struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// ContextDef defines per-persona context overrides.
type ContextDef struct {
	DescriptionPrefix         string `yaml:"description_prefix,omitempty"`
	DescriptionOverride       string `yaml:"description_override,omitempty"`
	AgentInstructionsSuffix   string `yaml:"agent_instructions_suffix,omitempty"`
	AgentInstructionsOverride string `yaml:"agent_instructions_override,omitempty"`
}

// ToPersona builds the runtime persona this definition describes. source is
// the provenance label the caller stamps on it.
//
// It is the single construction of a file persona: the platform's startup load
// and the admin API's revert-to-file path both go through it, so a field added
// to PersonaDef reaches both rather than only the one its author remembered.
func (d PersonaDef) ToPersona(name, source string) *persona.Persona {
	return &persona.Persona{
		Name:        name,
		DisplayName: d.DisplayName,
		Description: d.Description,
		Roles:       d.Roles,
		Tools: persona.ToolRules{
			Allow: d.Tools.Allow,
			Deny:  d.Tools.Deny,
		},
		Connections: persona.ConnectionRules{
			Allow: d.Connections.Allow,
			Deny:  d.Connections.Deny,
		},
		APIRoutes: d.apiRoutes(),
		Context: persona.ContextOverrides{
			DescriptionPrefix:         d.Context.DescriptionPrefix,
			DescriptionOverride:       d.Context.DescriptionOverride,
			AgentInstructionsSuffix:   d.Context.AgentInstructionsSuffix,
			AgentInstructionsOverride: d.Context.AgentInstructionsOverride,
		},
		Priority: d.Priority,
		Source:   source,
	}
}

// apiRoutes projects the configured route rules onto the shape the authorizer
// evaluates. Returns nil for an empty list so a persona that declares none is
// indistinguishable from one written before the field existed — both leave the
// connection-level check as the only gate.
func (d PersonaDef) apiRoutes() []persona.APIRouteRule {
	if len(d.APIRoutes) == 0 {
		return nil
	}
	rules := make([]persona.APIRouteRule, 0, len(d.APIRoutes))
	for _, r := range d.APIRoutes {
		rules = append(rules, persona.APIRouteRule{
			Connection: r.Connection,
			Methods:    r.Methods,
			Paths:      r.Paths,
			Action:     r.Action,
		})
	}
	return rules
}
