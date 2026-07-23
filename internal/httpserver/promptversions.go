package httpserver

import (
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachhttp"
	"github.com/txn2/mcp-data-platform/pkg/prompt/versionhttp"
)

// This file holds the unit-testable identity adapters for the prompt
// version-history / review / usage REST surface (#1009). The handlers live in
// pkg/prompt/versionhttp (outside pkg/admin and pkg/portal, which sit at the
// package-size budget); the mount functions live in dbmounts.go because their
// bodies only run against a live Postgres (the versioning capability exists
// only on the database-backed prompt store).

// adminEmail resolves the authenticated admin's identity for approval stamps,
// falling back to the user ID (the API key name) for API-key admins without
// an email, so an approval is always attributable to some actor.
func adminEmail(r *http.Request) string {
	u := admin.GetUser(r.Context())
	if u == nil {
		return ""
	}
	if u.Email != "" {
		return u.Email
	}
	return u.UserID
}

// portalIdentityResolver adapts the portal auth context to the versionhttp
// identity shape: caller email, admin membership, and resolved persona.
func portalIdentityResolver(adminRoles []string, resolver portal.PersonaResolver) func(r *http.Request) *versionhttp.PortalIdentity {
	return func(r *http.Request) *versionhttp.PortalIdentity {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		id := &versionhttp.PortalIdentity{UserID: user.UserID, Email: user.Email, IsAdmin: rolesIntersect(user.Roles, adminRoles)}
		if resolver != nil {
			if pi := resolver(user.Roles); pi != nil {
				id.Persona = pi.Name
			}
		}
		return id
	}
}

// rolesIntersect reports whether any user role appears in the target role set.
func rolesIntersect(userRoles, targetRoles []string) bool {
	for _, ur := range userRoles {
		if slices.Contains(targetRoles, ur) {
			return true
		}
	}
	return false
}

// promptAttachmentDeps assembles the prompt-attachment handler dependencies
// shared by both surfaces (#1013). The attachment capability is asserted from
// the prompt store, which the prompt layer's notifying wrapper preserves; a
// deployment without it (no database) yields nil deps and attachhttp.New
// declines to build a handler, so the routes are simply not mounted.
func promptAttachmentDeps(p *platform.Platform, caller func(r *http.Request) *attachhttp.Identity) attachhttp.Deps {
	store := p.PromptStore()
	return attachhttp.Deps{
		Store:       store,
		Attachments: prompt.AsAttachmentStore(store),
		Resources:   p.ResourceStore(),
		Caller:      caller,
	}
}

// adminAttachmentIdentity resolves the admin caller's resource claims. Every
// authenticated admin API caller is an admin by construction (the surface sits
// behind the admin auth middleware), so IsAdmin is unconditional; persona
// memberships still come from the registry, because resource visibility grants
// an admin no persona they do not belong to.
func adminAttachmentIdentity(pr *persona.Registry, adminPersona string) func(r *http.Request) *attachhttp.Identity {
	return func(r *http.Request) *attachhttp.Identity {
		u := admin.GetUser(r.Context())
		if u == nil {
			return nil
		}
		claims := buildResourceClaims(&portal.User{UserID: u.UserID, Email: adminEmail(r), Roles: u.Roles}, pr, adminPersona)
		claims.IsAdmin = true
		return claims
	}
}

// portalAttachmentIdentity adapts the portal auth context to the same resource
// claims the resources REST surface builds, so the attachment routes apply an
// identical read rule over the caller's full persona membership.
func portalAttachmentIdentity(pr *persona.Registry, adminPersona string) func(r *http.Request) *attachhttp.Identity {
	return func(r *http.Request) *attachhttp.Identity {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		return buildResourceClaims(user, pr, adminPersona)
	}
}
