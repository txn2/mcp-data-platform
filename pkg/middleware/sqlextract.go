package middleware

import (
	"regexp"
	"strings"
)

// TableRef represents an extracted table reference from SQL.
type TableRef struct {
	Catalog  string
	Schema   string
	Table    string
	FullPath string
	Source   string // "FROM", "JOIN", "TABLE_FUNCTION"
}

// ExtractTablesFromSQL extracts all table references from SQL.
// Uses regex for Trino-specific functions and standard table patterns.
// Combines ES raw_query indices with regular table references (e.g., JOINs).
// Filters out CTE references to only return physical tables.
func ExtractTablesFromSQL(sql string) []TableRef {
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
	refs     []TableRef
	seen     map[string]bool
	cteNames map[string]bool
}

func newTableCollector(cteNames map[string]bool) *tableCollector {
	return &tableCollector{
		seen:     make(map[string]bool),
		cteNames: cteNames,
	}
}

func (c *tableCollector) addAll(refs []TableRef) {
	for _, ref := range refs {
		c.add(ref)
	}
}

func (c *tableCollector) add(ref TableRef) {
	if c.isCTE(ref) || c.seen[ref.FullPath] {
		return
	}
	c.seen[ref.FullPath] = true
	c.refs = append(c.refs, ref)
}

func (c *tableCollector) isCTE(ref TableRef) bool {
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

	// Table reference patterns for Trino 3-part names
	// Matches: FROM/JOIN catalog.schema.table or schema.table or table.
	tableRefPattern = regexp.MustCompile(`(?i)(?:FROM|JOIN)\s+` +
		`([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*){0,2})` +
		`(?:\s+(?:AS\s+)?[a-zA-Z_][a-zA-Z0-9_]*)?(?:\s|,|$|ON|WHERE|GROUP|ORDER|LIMIT|LEFT|RIGHT|INNER|OUTER|CROSS|NATURAL)`)
)

// extractTablesWithRegex extracts table references using regex.
func extractTablesWithRegex(sql string) []TableRef {
	matches := tableRefPattern.FindAllStringSubmatch(sql, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	tables := make([]TableRef, 0, len(matches))

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		tablePath := match[1]

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

// parseTablePath parses a dot-separated table path into TableRef.
func parseTablePath(path string) TableRef {
	parts := strings.Split(path, ".")
	ref := TableRef{FullPath: path}

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
func extractESRawQuery(sql string) []TableRef {
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
	refs := make([]TableRef, 0, len(indices))

	for _, idx := range indices {
		idx = strings.TrimSpace(idx)
		if idx == "" {
			continue
		}
		refs = append(refs, TableRef{
			Catalog:  "elasticsearch",
			Schema:   schema,
			Table:    idx,
			FullPath: "elasticsearch." + schema + "." + idx,
			Source:   "TABLE_FUNCTION",
		})
	}

	return refs
}
