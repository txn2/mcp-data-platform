package platform

import (
	"github.com/txn2/mcp-data-platform/internal/platform/userdir"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// initUserStore assembles the known-users directory (#614) via the userdir
// owner. The directory requires a database; without one the feature is disabled
// (nil handle) and every consumer degrades cleanly to free-typed email sharing.
func (p *Platform) initUserStore() {
	p.users = userdir.New(p.db)
}

// UserStore returns the known-users directory store (nil when no database is
// configured). Delegates to the userdir owner.
func (p *Platform) UserStore() user.Store {
	return p.users.Store()
}

// BoundPrincipals resolves a person by address through the user directory
// (#1759); a site tile is drawn as its owner through it (#2747). Nil with none.
func (p *Platform) BoundPrincipals() auth.PrincipalSource {
	if p.users.Directory() == nil {
		return nil
	}
	return p.users
}
