package persona

import (
	"context"
	"path/filepath"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// ToolFilter filters tools based on persona rules.
type ToolFilter struct {
	registry *Registry
}

// NewToolFilter creates a new tool filter.
func NewToolFilter(registry *Registry) *ToolFilter {
	return &ToolFilter{registry: registry}
}

// IsAllowed checks if a tool is allowed for a persona.
func (*ToolFilter) IsAllowed(persona *Persona, toolName string) bool {
	allowed, _, _ := evaluateToolAccess(persona, toolName)
	return allowed
}

// AccessSource indicates which clause of a persona's tool rules
// produced an allow/deny decision. Used by the admin Tools-detail
// endpoint so operators can see WHY a persona allows or denies a tool.
type AccessSource string

const (
	// AccessSourceAllow — an explicit allow pattern matched.
	AccessSourceAllow AccessSource = "allow"
	// AccessSourceDeny — an explicit deny pattern matched (takes precedence over allow).
	AccessSourceDeny AccessSource = "deny"
	// AccessSourceDefault — no allow pattern matched; falls through to fail-closed default.
	AccessSourceDefault AccessSource = "default"
)

// AccessDecision is the per-tool decision a persona produces for a
// specific tool name, with the matching pattern and source recorded
// so the admin UI can render "why".
type AccessDecision struct {
	Allowed        bool         `json:"allowed"`
	MatchedPattern string       `json:"matched_pattern"`
	Source         AccessSource `json:"source"`
}

// WhyAllowed returns the access decision for a tool name with the
// matched pattern and source recorded. Mirrors IsAllowed's logic
// (deny first, allow second, default deny last) but surfaces which
// clause produced the decision so callers can explain it to operators.
func (*ToolFilter) WhyAllowed(persona *Persona, toolName string) AccessDecision {
	allowed, pattern, source := evaluateToolAccess(persona, toolName)
	return AccessDecision{
		Allowed:        allowed,
		MatchedPattern: pattern,
		Source:         source,
	}
}

// evaluateToolAccess is the shared core of IsAllowed and WhyAllowed.
func evaluateToolAccess(persona *Persona, toolName string) (allowed bool, pattern string, source AccessSource) {
	if persona == nil {
		// DENY if no persona — fail closed.
		return false, "", AccessSourceDefault
	}

	// Check deny rules first (they take precedence).
	for _, p := range persona.Tools.Deny {
		if matchPattern(p, toolName) {
			return false, p, AccessSourceDeny
		}
	}

	// Check allow rules.
	for _, p := range persona.Tools.Allow {
		if matchPattern(p, toolName) {
			return true, p, AccessSourceAllow
		}
	}

	// Default deny if no allow rules match.
	return false, "", AccessSourceDefault
}

// IsConnectionAllowed checks if a connection is allowed for a persona.
// If the persona has no connection allow rules, all connections are permitted
// (backward-compatible default). Empty connection names are always allowed.
func (*ToolFilter) IsConnectionAllowed(persona *Persona, connectionName string) bool {
	if persona == nil {
		return false
	}

	// Empty connection name = platform-level tools (always allowed).
	if connectionName == "" {
		return true
	}

	// Check deny rules first (they take precedence)
	for _, pattern := range persona.Connections.Deny {
		if matchPattern(pattern, connectionName) {
			return false
		}
	}

	// If no allow rules, all connections are permitted (backward compat).
	if len(persona.Connections.Allow) == 0 {
		return true
	}

	// Check allow rules
	for _, pattern := range persona.Connections.Allow {
		if matchPattern(pattern, connectionName) {
			return true
		}
	}

	// Default deny if allow rules exist but none match
	return false
}

// FilterTools filters a list of tools based on persona rules.
func (f *ToolFilter) FilterTools(persona *Persona, tools []string) []string {
	if persona == nil {
		return nil // DENY ALL if no persona - fail closed
	}

	var allowed []string
	for _, tool := range tools {
		if f.IsAllowed(persona, tool) {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}

// matchPattern checks if a name matches a glob pattern.
// Supports glob-style patterns with * wildcard.
func matchPattern(pattern, name string) bool {
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// Authorizer implements middleware.Authorizer using personas.
type Authorizer struct {
	registry   *Registry
	roleMapper RoleMapper
	filter     *ToolFilter
}

// NewAuthorizer creates a new persona-based authorizer.
func NewAuthorizer(registry *Registry, mapper RoleMapper) *Authorizer {
	return &Authorizer{
		registry:   registry,
		roleMapper: mapper,
		filter:     NewToolFilter(registry),
	}
}

// IsAuthorized checks if the user is authorized for the tool on the given connection.
// Both the tool and the connection must be allowed by the persona's rules.
// Returns the resolved persona name for audit logging.
func (a *Authorizer) IsAuthorized(ctx context.Context, _ string, roles []string, toolName, connectionName string) (allowed bool, personaName, reason string) {
	// Get persona for roles
	persona, err := a.roleMapper.MapToPersona(ctx, roles)
	if err != nil {
		return false, "", "failed to determine persona"
	}

	if persona != nil {
		personaName = persona.Name
	}

	// Check if tool is allowed
	if !a.filter.IsAllowed(persona, toolName) {
		return false, personaName, "tool not allowed for persona: " + personaName
	}

	// Check if connection is allowed
	if !a.filter.IsConnectionAllowed(persona, connectionName) {
		return false, personaName, "connection not allowed for persona: " + personaName
	}

	return true, personaName, ""
}

// Verify interface compliance.
var _ middleware.Authorizer = (*Authorizer)(nil)
