package resource

import "slices"

// Claims represents the identity information needed for resource permission checks.
type Claims struct {
	Sub      string   // Keycloak subject (user ID)
	Email    string   // user email
	Personas []string // persona names the user belongs to
	Roles    []string // e.g., "admin", "platform-admin", "persona-admin:finance"
}

// CanWriteScope checks whether the caller has write permission for the given scope.
func CanWriteScope(c Claims, scope Scope, scopeID string) bool {
	switch scope {
	case ScopeUser:
		// Any authenticated user can write to their own user scope.
		// Platform admins can write to any user scope.
		return scopeID == c.Sub || isPlatformAdmin(c)
	case ScopePersona:
		return isPlatformAdmin(c) || isPersonaAdmin(c, scopeID)
	case ScopeGlobal:
		return isPlatformAdmin(c)
	default:
		return false
	}
}

// CanModifyResource checks whether the caller can update or delete a resource.
// The caller must be the original uploader OR have write permission for the scope.
func CanModifyResource(c Claims, r *Resource) bool {
	if r.UploaderSub == c.Sub {
		return true
	}
	return CanWriteScope(c, r.Scope, r.ScopeID)
}

// CanReadResource checks whether the caller can read a specific resource.
func CanReadResource(c Claims, r *Resource) bool {
	for _, sf := range VisibleScopes(c) {
		if sf.Scope == r.Scope {
			if sf.Scope == ScopeGlobal {
				return true
			}
			if sf.ScopeID == r.ScopeID {
				return true
			}
		}
	}
	return false
}

// VisibleScopes returns the set of (scope, scope_id) tuples the caller is
// allowed to see. Always derived from claims, never from request input.
func VisibleScopes(c Claims) []ScopeFilter {
	var filters []ScopeFilter

	// Every authenticated user sees global resources.
	filters = append(filters, ScopeFilter{Scope: ScopeGlobal})

	// User sees their own resources (match by sub or email so admins
	// can scope resources to users by email address).
	if c.Sub != "" {
		filters = append(filters, ScopeFilter{Scope: ScopeUser, ScopeID: c.Sub})
	}
	if c.Email != "" && c.Email != c.Sub {
		filters = append(filters, ScopeFilter{Scope: ScopeUser, ScopeID: c.Email})
	}

	// User sees resources for each persona they belong to.
	for _, p := range c.Personas {
		filters = append(filters, ScopeFilter{Scope: ScopePersona, ScopeID: p})
	}

	return filters
}

func isPlatformAdmin(c Claims) bool {
	return slices.Contains(c.Roles, "admin") || slices.Contains(c.Roles, "platform-admin")
}

func isPersonaAdmin(c Claims, personaName string) bool {
	if isPlatformAdmin(c) {
		return true
	}
	return slices.Contains(c.Roles, "persona-admin:"+personaName)
}
