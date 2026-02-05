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
func (f *ToolFilter) IsAllowed(persona *Persona, toolName string) bool {
	if persona == nil {
		return false // DENY if no persona - fail closed
	}

	// Check deny rules first (they take precedence)
	for _, pattern := range persona.Tools.Deny {
		if matchPattern(pattern, toolName) {
			return false
		}
	}

	// Check allow rules
	for _, pattern := range persona.Tools.Allow {
		if matchPattern(pattern, toolName) {
			return true
		}
	}

	// Default deny if no allow rules match
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

// matchPattern checks if a tool name matches a pattern.
// Supports glob-style patterns with * wildcard.
func matchPattern(pattern, name string) bool {
	// Use filepath.Match for glob matching
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// PersonaAuthorizer implements middleware.Authorizer using personas.
type PersonaAuthorizer struct {
	registry   *Registry
	roleMapper RoleMapper
	filter     *ToolFilter
}

// NewPersonaAuthorizer creates a new persona-based authorizer.
func NewPersonaAuthorizer(registry *Registry, mapper RoleMapper) *PersonaAuthorizer {
	return &PersonaAuthorizer{
		registry:   registry,
		roleMapper: mapper,
		filter:     NewToolFilter(registry),
	}
}

// IsAuthorized checks if the user is authorized for the tool.
// Returns the resolved persona name for audit logging.
func (a *PersonaAuthorizer) IsAuthorized(ctx context.Context, userID string, roles []string, toolName string) (bool, string, string) {
	// Get persona for roles
	persona, err := a.roleMapper.MapToPersona(ctx, roles)
	if err != nil {
		return false, "", "failed to determine persona"
	}

	personaName := ""
	if persona != nil {
		personaName = persona.Name
	}

	// Check if tool is allowed
	if !a.filter.IsAllowed(persona, toolName) {
		return false, personaName, "tool not allowed for persona: " + personaName
	}

	return true, personaName, ""
}

// Verify interface compliance.
var _ middleware.Authorizer = (*PersonaAuthorizer)(nil)
