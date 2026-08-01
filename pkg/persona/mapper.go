package persona

import (
	"context"
	"strings"
)

// RoleMapper maps identity claims to platform roles and personas.
type RoleMapper interface {
	// MapToRoles extracts roles from claims.
	MapToRoles(claims map[string]any) ([]string, error)

	// MapToPersona maps roles to a persona.
	MapToPersona(ctx context.Context, roles []string) (*Persona, error)
}

// OIDCRoleMapper extracts roles from OIDC token claims.
type OIDCRoleMapper struct {
	// ClaimPath is the dot-separated path to roles in claims.
	ClaimPath string

	// RolePrefix filters roles to those starting with this prefix.
	RolePrefix string

	// PersonaMapping maps roles to persona names.
	PersonaMapping map[string]string

	// Registry is the persona registry.
	Registry *Registry
}

// MapToRoles extracts roles from OIDC claims.
func (m *OIDCRoleMapper) MapToRoles(claims map[string]any) ([]string, error) {
	value := getNestedValue(claims, m.ClaimPath)
	if value == nil {
		return []string{}, nil
	}
	return m.extractRoles(value), nil
}

// extractRoles extracts roles from a value that may be []any or []string.
func (m *OIDCRoleMapper) extractRoles(value any) []string {
	var roles []string
	switch v := value.(type) {
	case []any:
		roles = m.extractFromAnySlice(v)
	case []string:
		roles = m.extractFromStringSlice(v)
	}
	return roles
}

// extractFromAnySlice extracts roles from []any.
func (m *OIDCRoleMapper) extractFromAnySlice(items []any) []string {
	var roles []string
	for _, item := range items {
		if s, ok := item.(string); ok && m.matchesPrefix(s) {
			roles = append(roles, s)
		}
	}
	return roles
}

// extractFromStringSlice extracts roles from []string.
func (m *OIDCRoleMapper) extractFromStringSlice(items []string) []string {
	var roles []string
	for _, s := range items {
		if m.matchesPrefix(s) {
			roles = append(roles, s)
		}
	}
	return roles
}

// matchesPrefix checks if a role matches the required prefix.
func (m *OIDCRoleMapper) matchesPrefix(role string) bool {
	return m.RolePrefix == "" || strings.HasPrefix(role, m.RolePrefix)
}

// MapToPersona maps roles to a persona: an explicit role-to-persona mapping
// first, then the registry's role matching. Roles that map to nothing resolve
// to the deny-all DefaultPersona, so an identity an operator never granted a
// role reaches no tool.
func (m *OIDCRoleMapper) MapToPersona(_ context.Context, roles []string) (*Persona, error) {
	// Check explicit mappings first
	for _, role := range roles {
		if personaName, ok := m.PersonaMapping[role]; ok {
			if persona, ok := m.Registry.Get(personaName); ok {
				return persona, nil
			}
		}
	}

	// Fall back to registry role matching
	if persona, ok := m.Registry.GetForRoles(roles); ok {
		return persona, nil
	}

	return DefaultPersona(), nil
}

// StaticRoleMapper assigns every caller one named persona regardless of their
// roles. It exists for embedders that do their own authorization upstream and
// want a fixed persona; it is not wired into any platform auth path, where
// access follows role-to-persona matching.
type StaticRoleMapper struct {
	// GroupPersonas maps groups to persona names.
	GroupPersonas map[string]string

	// PersonaName is the persona every caller is assigned. An empty name, or a
	// name absent from the registry, yields the deny-all DefaultPersona.
	PersonaName string

	// Registry is the persona registry.
	Registry *Registry
}

// MapToRoles returns static roles (not used for static mapping).
func (*StaticRoleMapper) MapToRoles(_ map[string]any) ([]string, error) {
	return []string{}, nil
}

// MapToPersona returns the configured persona, or the deny-all DefaultPersona
// when none is configured or the configured name is not registered.
func (m *StaticRoleMapper) MapToPersona(_ context.Context, _ []string) (*Persona, error) {
	if m.PersonaName != "" {
		if persona, ok := m.Registry.Get(m.PersonaName); ok {
			return persona, nil
		}
	}

	return DefaultPersona(), nil
}

// ChainedRoleMapper tries multiple mappers in order.
type ChainedRoleMapper struct {
	Mappers []RoleMapper
}

// MapToRoles aggregates roles from all mappers.
func (c *ChainedRoleMapper) MapToRoles(claims map[string]any) ([]string, error) {
	var allRoles []string
	seen := make(map[string]bool)

	for _, mapper := range c.Mappers {
		roles, err := mapper.MapToRoles(claims)
		if err != nil {
			continue
		}
		for _, role := range roles {
			if !seen[role] {
				seen[role] = true
				allRoles = append(allRoles, role)
			}
		}
	}

	return allRoles, nil
}

// MapToPersona uses the first mapper that returns a persona.
func (c *ChainedRoleMapper) MapToPersona(ctx context.Context, roles []string) (*Persona, error) {
	for _, mapper := range c.Mappers {
		persona, err := mapper.MapToPersona(ctx, roles)
		if err == nil && persona != nil {
			return persona, nil
		}
	}
	return DefaultPersona(), nil
}

// getNestedValue retrieves a value at a dot-separated path.
func getNestedValue(data map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var current any = data

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}

	return current
}
