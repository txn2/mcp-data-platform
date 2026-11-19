//go:build integration

package assetlinks

import "github.com/txn2/mcp-data-platform/internal/portal/portaldomain"

// SQLSamples renders the statement this package assembles at run time, for
// the gate that hands store SQL to a real PostgreSQL to parse and plan (#1512).
func SQLSamples() map[string]string {
	reach := portaldomain.NewReach("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com", "analyst", portaldomain.ScopeAll)
	linkedFrom, _, _ := BuildLinkedFrom("asset-b", reach)
	return map[string]string{"BuildLinkedFrom": linkedFrom}
}
