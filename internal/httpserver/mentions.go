package httpserver

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/portal/mentionhttp"
)

// The mount function lives in dbmounts.go with the other database-only mounts;
// this file keeps the unit-testable adapters.

// Wiring for @-mention tagging on feedback threads (#627) and the people
// pickers that feed it. The handlers live in pkg/portal/mentionhttp, outside
// pkg/portal (which sits at the package-size budget); this file holds the
// identity adapter and the audience constructor both the portal REST surface
// and the notification bridge draw on.

// mentionAudience builds the mention audience resolver over the platform
// database, returning nil when the deployment has none. Mentions are a
// database feature: the audience is read from share grants and the known-users
// directory, neither of which exists without one.
func mentionAudience(p *platform.Platform) *mention.Audience {
	if p.DB() == nil {
		return nil
	}
	return mention.NewAudience(p.DB())
}

// mentionIdentityResolver adapts the portal auth context to the mentionhttp
// identity shape.
func mentionIdentityResolver(adminRoles []string) func(*http.Request) *mentionhttp.Identity {
	return func(r *http.Request) *mentionhttp.Identity {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		return &mentionhttp.Identity{
			UserID:  user.UserID,
			Email:   user.Email,
			IsAdmin: rolesIntersect(user.Roles, adminRoles),
		}
	}
}
