//go:build integration

package assetsimilar

import "github.com/txn2/mcp-data-platform/internal/portal/portaldomain"

// SQLSamples renders each statement this package assembles at run time, for
// the gate that hands store SQL to a real PostgreSQL to parse and plan
// (#1512).
func SQLSamples() map[string]string {
	vec := make([]float32, 768)
	reach := portaldomain.NewReach("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com", "analyst", portaldomain.ScopeAll)
	nearest, _, _ := BuildNearest(NearestQuery{Vector: vec, Reach: reach, Exclude: []string{"a1"}})
	owner := portaldomain.NewAssetOwner("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com")
	owned, _, _ := BuildPairs(GroupsQuery{Owner: &owner})
	every, _, _ := BuildPairs(GroupsQuery{})
	return map[string]string{
		"BuildNearest":     nearest,
		"BuildPairs/owner": owned,
		"BuildPairs/every": every,
	}
}
