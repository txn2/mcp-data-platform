package mentionhttp

import (
	"database/sql"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
)

// Audience is the mention audience over the platform database, nil when the
// deployment has none. Mentions are a database feature: the audience is read
// from share grants and the known-users directory, neither of which exists
// without one.
func Audience(db *sql.DB) *mention.Audience {
	if db == nil {
		return nil
	}
	return mention.NewAudience(db)
}

// IdentityResolver adapts the portal auth context to the Identity the
// handlers read.
func IdentityResolver(isAdmin func([]string) bool) func(*http.Request) *Identity {
	return func(r *http.Request) *Identity {
		user := portal.GetUser(r.Context())
		if user == nil {
			return nil
		}
		return &Identity{UserID: user.UserID, Email: user.Email, IsAdmin: isAdmin(user.Roles)}
	}
}
