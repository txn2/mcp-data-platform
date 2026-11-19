package scripthttp

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// PortalIdentityOf resolves the portal caller for the script routes: who
// they are, the persona they resolved to, and whether they hold the admin
// roles that make the surface unrestricted for them. A request with no
// signed-in user resolves to nil, which the handlers answer as a 401; a nil
// resolver leaves the persona empty.
func PortalIdentityOf(isAdmin func([]string) bool, resolver portal.PersonaResolver) func(r *http.Request) *PortalIdentity {
	return func(r *http.Request) *PortalIdentity {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		id := &PortalIdentity{
			UserID: user.UserID, Email: user.Email, Roles: user.Roles,
			AuthType: user.AuthType, Claims: user.Claims,
			IsAdmin: isAdmin(user.Roles),
		}
		if resolver != nil {
			if pi := resolver(user.Roles); pi != nil {
				id.Persona = pi.Name
			}
		}
		return id
	}
}
