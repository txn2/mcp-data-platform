//go:build integration

package library

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// SQLSamples renders each statement this package assembles at run time, for
// the gate that hands store SQL to a real PostgreSQL to parse and plan
// (#1512).
func SQLSamples() map[string]string {
	reach := portaldomain.NewReach("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com", "analyst", portaldomain.ScopeAll)
	owner := Scope{Owner: portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com")}
	recents, _, _ := buildItems("portal_opens", "last_opened_at", "u1", reach, 10)
	favorites, _, _ := buildItems("portal_favorites", "created_at", "u1", reach, 10)
	trash, _, _ := buildTrash(owner, 10, 0)
	everyone, _, _ := buildTrash(Scope{Everyone: true}, 10, 0)
	unused, _, _ := buildUnused(owner, time.Unix(0, 0), 10, 0)
	return map[string]string{
		"Recents":       recents,
		"Favorites":     favorites,
		"Trash":         trash,
		"TrashEveryone": everyone,
		"Unused":        unused,
	}
}
