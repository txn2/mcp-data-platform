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
		Scopes: scopes,
		Path:   "reference/quarterly",
		Tag:    "finance",
		Query:  "report",
		Limit:  25,
	}
	lastRead := filter
	lastRead.Sort = SortLastRead
	// The administrator's unrestricted listing, whose visibility clause is a
	// constant rather than a scope predicate, so every later placeholder in the
	// statement is numbered from a different starting point (#1553).
	everyLibrary := filter
	everyLibrary.Scopes = nil
	everyLibrary.AllScopes = true

	name, desc := "Q4 report", "the quarterly numbers"
	update := Update{
		DisplayName: &name,
		Description: &desc,
		Tags:        []string{"finance", "q4"},
	}

	hybrid, _ := buildHybridSearch(q)
	lexical, _ := buildLexicalSearch(q)
	updateSQL, _ := buildUpdate("r-1", update)
	count, page, _ := buildList(filter)
	_, lastReadPage, _ := buildList(lastRead)
	everyCount, everyPage, _ := buildList(everyLibrary)
	// The folder rollup (#1555): a lateral expansion over each path's segments,
	// which is the one statement here whose shape the planner has to accept
	// rather than just its predicate.
	folders, _ := buildFolders(Filter{Scopes: scopes})
	// The pending-capture predicate (#1554): an ILIKE ANY over a bound array,
	// two nullable timestamp comparisons, and the whole resource projection.
	pending, _ := buildPendingThumbnails(Filter{Scopes: scopes}, 25)
	setThumb := "UPDATE resources SET thumbnail_s3_key = $1, thumbnail_captured_at = $2 WHERE id = $3"
	clearThumb := "UPDATE resources SET thumbnail_dark_s3_key = '', thumbnail_dark_captured_at = NULL WHERE id = $1"
	foldersAll, _ := buildFolders(Filter{AllScopes: true})

	return map[string]string{
		"buildHybridSearch":       hybrid,
		"buildLexicalSearch":      lexical,
		"buildUpdate":             updateSQL,
		"buildList/count":         count,
		"buildList/page":          page,
		"buildList/page.lastRead": lastReadPage,
		"buildList/count.all":     everyCount,
		"buildList/page.all":      everyPage,
		"buildFolders":            folders,
		"buildPendingThumbnails":  pending,
		"setThumbnail":            setThumb,
		"clearThumbnail":          clearThumb,
		"buildFolders/all":        foldersAll,
	}
}
