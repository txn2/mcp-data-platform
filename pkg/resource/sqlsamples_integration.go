//go:build integration

package resource

// SQLSamples renders each statement this package assembles at run time, for the
// gate that hands store SQL to a real PostgreSQL to parse and plan (#1512).
//
// A statement built from a format string or from a scope predicate has no text
// in the source, so nothing could reach it: it is these builders, called here
// with representative inputs, that the gate prepares. The inputs exercise the
// shapes that differ structurally -- several scopes, a global scope with no id,
// every optional filter present, each sort, and an update that sets every
// column -- rather than one happy path.
//
// The file is integration-tagged, so it is absent from the default build.
func SQLSamples() map[string]string {
	scopes := []ScopeFilter{
		{Scope: ScopeUser, ScopeID: "u-1"},
		{Scope: ScopePersona, ScopeID: "analyst"},
		{Scope: ScopeGlobal},
	}
	// 768 is the width of the resources.embedding column (migration 000091).
	// The gate prepares rather than executes, so the values do not matter, but
	// a vector of the declared width is what the planner types $1 against.
	q := SearchQuery{
		Embedding: make([]float32, 768),
		QueryText: "quarterly report",
		Scopes:    scopes,
		Limit:     10,
	}
	filter := Filter{
		Scopes:   scopes,
		Category: "reference",
		Tag:      "finance",
		Query:    "report",
		Limit:    25,
	}
	lastRead := filter
	lastRead.Sort = SortLastRead

	name, desc, category := "Q4 report", "the quarterly numbers", "reference"
	update := Update{
		DisplayName: &name,
		Description: &desc,
		Tags:        []string{"finance", "q4"},
		Category:    &category,
	}

	hybrid, _ := buildHybridSearch(q)
	lexical, _ := buildLexicalSearch(q)
	updateSQL, _ := buildUpdate("r-1", update)
	count, page, _ := buildList(filter)
	_, lastReadPage, _ := buildList(lastRead)

	return map[string]string{
		"buildHybridSearch":       hybrid,
		"buildLexicalSearch":      lexical,
		"buildUpdate":             updateSQL,
		"buildList/count":         count,
		"buildList/page":          page,
		"buildList/page.lastRead": lastReadPage,
	}
}
