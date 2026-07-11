package platform

import (
	"github.com/txn2/mcp-data-platform/internal/platform/userdir"
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
