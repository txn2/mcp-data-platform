// Package sqltables extracts the physical tables a SQL statement reads.
//
// It is one extractor, not one per caller: semantic enrichment names the tables
// it must fetch context for, and the call catalog names the datasets a recorded
// query addressed (#1321). Both are answering the same question, and a second
// implementation of it would mean an enriched table and a cataloged target
// disagreeing about what a query touched.
//
// The extraction is lexical (regular expressions over FROM and JOIN clauses,
// plus Trino's Elasticsearch raw_query table function), not a parser: it names
// what a statement plainly reads, drops common table expressions, and makes no
// claim about a statement it cannot parse.
package sqltables

import (
	"regexp"
	"strings"
)

// catalogElasticsearch is the Trino catalog name used for Elasticsearch raw_query
// table function expansion.
const catalogElasticsearch = "elasticsearch"

// Ref is one table a statement reads.
type Ref struct {
	Catalog  string
	Schema   string
	Table    string
	FullPath string
	Source   string // "FROM", "JOIN", "TABLE_FUNCTION"
}

// Extract returns every physical table a statement reads.
// Uses regex for Trino-specific functions and standard table patterns.
// Combines ES raw_query indices with regular table references (e.g., JOINs).
// Filters out CTE references to only return physical tables.
func Extract(sql string) []Ref {
	cteNames := extractCTENames(sql)
	collector := newTableCollector(cteNames)

	// Extract ES raw_query indices (non-standard SQL)
	collector.addAll(extractESRawQuery(sql))

	// Extract regular table references with regex
	collector.addAll(extractTablesWithRegex(sql))

	return collector.refs
}

// tableCollector deduplicates table refs and filters out CTEs.
type tableCollector struct {
	refs     []Ref
	seen     map[string]bool
	cteNames map[string]bool
}

func newTableCollector(cteNames map[string]bool) *tableCollector {
	return &tableCollector{
		seen:     make(map[string]bool),
		cteNames: cteNames,
	}
}

func (c *tableCollector) addAll(refs []Ref) {
	for _, ref := range refs {
		c.add(ref)
	}
}

func (c *tableCollector) add(ref Ref) {
	if c.isCTE(ref) || c.seen[ref.FullPath] {
		return
	}
	c.seen[ref.FullPath] = true
	c.refs = append(c.refs, ref)
}

func (c *tableCollector) isCTE(ref Ref) bool {
	return ref.Catalog == "" && ref.Schema == "" && c.cteNames[ref.Table]
}

// extractCTENames extracts CTE (Common Table Expression) names from SQL.
func extractCTENames(sql string) map[string]bool {
	names := make(map[string]bool)
	matches := cteNamePattern.FindAllStringSubmatch(sql, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			names[match[1]] = true
		}
	}
	return names
}

// Regex patterns for SQL table extraction.
var (
	// ES raw_query patterns (non-standard Trino syntax).
	rawQueryPattern    = regexp.MustCompile(`(?i)TABLE\s*\(\s*elasticsearch\.system\.raw_query\s*\(`)
	indexParamPattern  = regexp.MustCompile(`(?i)index\s*=>\s*'([^']+)'`)
	schemaParamPattern = regexp.MustCompile(`(?i)schema\s*=>\s*'([^']+)'`)

	// CTE name pattern - matches "WITH name AS" or ", name AS" for chained CTEs.
	cteNamePattern = regexp.MustCompile(`(?i)(?:WITH|,)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+AS\s*\(`)

	// tableRefPattern matches what FROM and JOIN name: a Trino table
	// reference of up to three parts, and whether an opening parenthesis
	// follows it.
	//
	// The pattern deliberately stops at the name. An earlier form also
	// consumed an optional alias, which swallowed the JOIN keyword of
	// "FROM a.b.t1 JOIN a.b.t2" as if it were t1's alias and left the
	// joined table unextracted — every JOIN's second table was invisible to
	// enrichment. Nothing after the name is needed to know the name.
	tableRefPattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+` +
		`([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*){0,2})` +
		`\s*(\()?`)
)

// callPositions is the index of the trailing-parenthesis capture group in
// tableRefPattern, and nameGroup the index of the name.
const (
	nameGroup = 1
	callGroup = 2
)

// notTables are the words that can follow FROM or JOIN without naming a table.
// Every one of them is followed by something else that does: a table function's
// arguments, a lateral subquery, an inline VALUES list.
var notTables = map[string]bool{
	"unnest": true, "lateral": true, "table": true, "values": true, "select": true,
}

// extractTablesWithRegex extracts table references using regex.
func extractTablesWithRegex(sql string) []Ref {
	matches := tableRefPattern.FindAllStringSubmatch(sql, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	tables := make([]Ref, 0, len(matches))

	for _, match := range matches {
		if len(match) <= callGroup {
			continue
		}
		tablePath := match[nameGroup]

		// A name followed by "(" is a function call, not a table.
		if match[callGroup] != "" || notTables[strings.ToLower(tablePath)] {
			continue
		}
		if seen[tablePath] {
			continue
		}
		seen[tablePath] = true

		ref := parseTablePath(tablePath)
		ref.Source = "FROM"
		tables = append(tables, ref)
	}

	return tables
}

// tableNamePartsCount is the expected number of parts in a fully-qualified table name (catalog.schema.table).
const tableNamePartsCount = 3

// parseTablePath parses a dot-separated table path into Ref.
func parseTablePath(path string) Ref {
	parts := strings.Split(path, ".")
	ref := Ref{FullPath: path}

	switch len(parts) {
	case tableNamePartsCount:
		ref.Catalog = parts[0]
		ref.Schema = parts[1]
		ref.Table = parts[2]
	case 2:
		ref.Schema = parts[0]
		ref.Table = parts[1]
	case 1:
		ref.Table = parts[0]
	}

	return ref
}

// extractESRawQuery extracts index references from Elasticsearch raw_query.
func extractESRawQuery(sql string) []Ref {
	if !rawQueryPattern.MatchString(sql) {
		return nil
	}

	// Extract schema parameter (default to "default")
	schema := "default"
	if match := schemaParamPattern.FindStringSubmatch(sql); len(match) > 1 {
		schema = match[1]
	}

	// Extract index parameter (may be comma-separated)
	indexMatch := indexParamPattern.FindStringSubmatch(sql)
	if len(indexMatch) < 2 {
		return nil
	}

	indices := strings.Split(indexMatch[1], ",")
	refs := make([]Ref, 0, len(indices))

	for _, idx := range indices {
		idx = strings.TrimSpace(idx)
		if idx == "" {
			continue
		}
		refs = append(refs, Ref{
			Catalog:  catalogElasticsearch,
			Schema:   schema,
			Table:    idx,
			FullPath: catalogElasticsearch + "." + schema + "." + idx,
			Source:   "TABLE_FUNCTION",
		})
	}

	return refs
}
