package resource

import (
	"slices"
	"strings"
)

// Claims represents the identity information needed for resource permission checks.
type Claims struct {
	Sub             string   // Keycloak subject (user ID)
	Email           string   // user email
	Personas        []string // persona names the user belongs to
	Roles           []string // raw roles from auth (may have prefix, e.g., "dp_admin")
	IsAdmin         bool     // resolved by the caller from persona config
	AdminOfPersonas []string // persona names this user can admin (resolved by caller from role patterns)
}

// personaAdminInfix is the role substring that marks a persona-admin grant.
// A role of any prefix carrying it (for example "dp_persona-admin:finance")
// grants admin authority over the named persona's resources.
const personaAdminInfix = "persona-admin:"

// BuildClaims assembles permission claims from an authenticated caller's
// identity. It owns the roles-to-persona-admin mapping so every surface that
// must apply the resource read rule — the resources middleware, prompt
// attachment serving (#1013), the attachment REST handler — derives claims
// identically instead of each reimplementing it.
//
// persona is the caller's single resolved persona; pass "" when none is
// resolved. It is separate from roles because persona membership is resolved
// from roles by the platform's persona registry, which this package does not
// see.
func BuildClaims(sub, email, persona string, roles []string, isAdmin bool) Claims {
	c := Claims{
		Sub:             sub,
		Email:           email,
		Roles:           roles,
		IsAdmin:         isAdmin,
		AdminOfPersonas: PersonaAdminRoles(roles),
	}
	if persona != "" {
		c.Personas = []string{persona}
	}
	return c
}

// PersonaAdminRoles extracts the persona names a role set grants admin
// authority over, tolerating any role prefix.
func PersonaAdminRoles(roles []string) []string {
	var out []string
	for _, r := range roles {
		if _, name, ok := strings.Cut(r, personaAdminInfix); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
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

// CanAccessResource checks whether the caller may see a specific resource at
// all: it is inside their visible scopes, OR they hold write authority over the
// scope it lives in (a platform admin, or that persona's admin).
//
// The second clause is what separates this from CanReadResource. VisibleScopes
// is membership-based and grants an admin no cross-persona read, so a platform
// admin who uploads a persona-scoped resource — which CanWriteScope explicitly
// permits — was then refused GET, PATCH and DELETE on it: they could create
// material they could neither manage nor remove. Use this as the visibility gate
// on a resource the caller names by id; CanReadResource remains the membership
// rule for enumeration and for content served into an agent's session.
//
// It deliberately checks CanWriteScope rather than CanModifyResource: the latter
// also grants the original uploader, and that grant is not re-derived from
// current authority. An admin who uploaded into another user's scope and then
// lost their admin role would otherwise keep reading, editing, and deleting that
// user's private file forever, because the uploader_sub on the row never
// changes. Every legitimate uploader whose authority came from their own scope
// (a user uploading to their own user scope) is already covered by
// CanReadResource.
func CanAccessResource(c Claims, r *Resource) bool {
	return CanReadResource(c, r) || CanWriteScope(c, r.Scope, r.ScopeID)
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
	return c.IsAdmin ||
		slices.Contains(c.Roles, "admin") ||
		slices.Contains(c.Roles, "platform-admin")
}

func isPersonaAdmin(c Claims, personaName string) bool {
	if isPlatformAdmin(c) {
		return true
	}
	return slices.Contains(c.AdminOfPersonas, personaName) ||
		slices.Contains(c.Roles, "persona-admin:"+personaName)
}
