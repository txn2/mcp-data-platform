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
	return map[string]string{
		"buildHybridSearch":  buildHybridSearch(q),
		"buildLexicalSearch": buildLexicalSearch(),
	}
}
