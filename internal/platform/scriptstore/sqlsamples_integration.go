//go:build integration

package scriptstore

import "github.com/txn2/mcp-data-platform/pkg/script"

// SQLSamples renders each statement this package assembles at run time, for the
// gate that hands store SQL to a real PostgreSQL to parse and plan (#1512).
//
// The search statements are built from a format string and a visibility
// predicate, so neither has text in the source; these builders, called here,
// are what the gate prepares.
//
// The file is integration-tagged, so it is absent from the default build.
func SQLSamples() map[string]string {
	// 768 is the width of the scripts.embedding column. The gate prepares
	// rather than executes, so the values do not matter, but a vector of the
	// declared width is what types $1.
	q := script.SearchQuery{
		Embedding:  make([]float32, 768),
		QueryText:  "weekly refresh",
		OwnerEmail: "owner@example.com",
		Limit:      10,
	}
	// The listing's own filter, carrying every axis at once so the rendered
	// statement exercises each clause the builders can emit.
	listing := script.ListFilter{
		OwnerEmail: "owner@example.com",
		IDs:        []string{"3f2b6c1e-8d4a-4b8e-9f1a-2c3d4e5f6a7b"},
		Category:   "reports",
		Tags:       []string{"weekly"},
		Status:     "active",
		Search:     "refresh",
		Sort:       script.SortName,
		Limit:      25,
	}
	return map[string]string{
		"buildHybridSearch":  buildHybridSearch(q),
		"buildLexicalSearch": buildLexicalSearch(),
		// Rendered here because PostgreSQL is the only thing that catches a
		// count naming a table that does not exist: the route treats a failed
		// count as "no better total available" and falls back to the page
		// length, so the failure is silent and looks exactly like the defect
		// the count exists to fix (#1795).
		"buildCountQuery": func() string { q, _ := buildCountQuery(listing); return q }(),
		"buildScheduledCountQuery": func() string {
			q, _ := buildScheduledCountQuery(listing)
			return q
		}(),
		"buildListQuery": func() string { q, _ := buildListQuery(listing); return q }(),
		"buildRunListQuery": func() string {
			q, _ := buildRunListQuery(script.RunFilter{
				ScriptID: "3f2b6c1e-8d4a-4b8e-9f1a-2c3d4e5f6a7b", ScriptIDs: []string{},
				Status: "succeeded", RequestedBy: "owner@example.com", Limit: 10,
			})
			return q
		}(),
	}
}
