//go:build integration

package postgres

import "github.com/txn2/mcp-data-platform/pkg/prompt"

// SQLSamples renders each statement this package assembles at run time, for the
// gate that hands store SQL to a real PostgreSQL to parse and plan (#1512).
//
// A statement built from a format string or from a visibility clause has no
// text in the source, so nothing could reach it: it is these builders, called
// here with representative inputs, that the gate prepares. The visibility
// clause differs by caller, so an admin, a persona member and a plain owner are
// each rendered.
//
// The file is integration-tagged, so it is absent from the default build.
func SQLSamples() map[string]string {
	// 768 is the width of the prompts.embedding column. The gate prepares
	// rather than executes, so the values do not matter, but a vector of the
	// declared width is what types $1.
	owner := prompt.SearchQuery{
		Embedding:  make([]float32, 768),
		QueryText:  "quarterly report",
		OwnerEmail: "owner@example.com",
		Persona:    "analyst",
		Limit:      10,
	}
	admin := owner
	admin.IsAdmin = true
	scoped := owner
	scoped.Scope = string(prompt.ScopePersonal)

	ownerHybrid, _ := buildHybridSearch(owner)
	adminHybrid, _ := buildHybridSearch(admin)
	ownerLexical, _ := buildLexicalSearch(owner)
	adminLexical, _ := buildLexicalSearch(admin)
	scopedLexical, _ := buildLexicalSearch(scoped)

	return map[string]string{
		"buildHybridSearch/owner":   ownerHybrid,
		"buildHybridSearch/admin":   adminHybrid,
		"buildLexicalSearch/owner":  ownerLexical,
		"buildLexicalSearch/admin":  adminLexical,
		"buildLexicalSearch/scoped": scopedLexical,
	}
}
