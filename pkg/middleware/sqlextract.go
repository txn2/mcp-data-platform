package middleware

import (
	"regexp"
	"strings"

	"github.com/xwb1989/sqlparser"
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
// Uses SQL parser for standard queries, regex for Trino-specific functions.
func ExtractTablesFromSQL(sql string) []TableRef {
	// Try ES raw_query extraction first (non-standard SQL)
	if esRefs := extractESRawQuery(sql); len(esRefs) > 0 {
		return esRefs
	}

	// Try parsing with sqlparser first
	refs := extractTablesFromAST(sql)
	if len(refs) > 0 {
		return refs
	}

	// Fall back to regex for Trino 3-part names that sqlparser can't handle
	return extractTablesWithRegex(sql)
}

// extractTablesFromAST uses sqlparser to extract tables from standard SQL.
func extractTablesFromAST(sql string) []TableRef {
	stmt, err := sqlparser.Parse(sql)
	if err != nil {
		return nil // Parse failed, try regex fallback
	}

	var tables []TableRef

	// Walk AST to find all table expressions
	err = sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		if aliased, ok := node.(*sqlparser.AliasedTableExpr); ok {
			if tableName, ok := aliased.Expr.(sqlparser.TableName); ok {
				ref := tableNameToRef(tableName)
				ref.Source = "FROM"
				tables = append(tables, ref)
			}
		}
		return true, nil
	}, stmt)
	if err != nil {
		return nil
	}

	return tables
}

// tableNameToRef converts sqlparser.TableName to TableRef.
// Handles Trino's 3-part naming (catalog.schema.table).
func tableNameToRef(tn sqlparser.TableName) TableRef {
	ref := TableRef{
		Table: tn.Name.String(),
	}

	// sqlparser uses Qualifier for schema
	if !tn.Qualifier.IsEmpty() {
		qualifier := tn.Qualifier.String()
		// Check if qualifier contains a dot (indicating catalog.schema)
		catalog, schema := splitCatalogSchema(qualifier)
		ref.Catalog = catalog
		ref.Schema = schema
	}

	// Build full path
	ref.FullPath = buildFullPath(ref.Catalog, ref.Schema, ref.Table)
	return ref
}

// splitCatalogSchema splits a qualifier that may be "catalog.schema" or just "schema".
func splitCatalogSchema(qualifier string) (catalog, schema string) {
	parts := strings.SplitN(qualifier, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", qualifier
}

// buildFullPath constructs a dot-separated table path.
func buildFullPath(catalog, schema, table string) string {
	var parts []string
	if catalog != "" {
		parts = append(parts, catalog)
	}
	if schema != "" {
		parts = append(parts, schema)
	}
	parts = append(parts, table)
	return strings.Join(parts, ".")
}

// Regex patterns for SQL table extraction.
var (
	// ES raw_query patterns (non-standard Trino syntax)
	rawQueryPattern    = regexp.MustCompile(`(?i)TABLE\s*\(\s*elasticsearch\.system\.raw_query\s*\(`)
	indexParamPattern  = regexp.MustCompile(`(?i)index\s*=>\s*'([^']+)'`)
	schemaParamPattern = regexp.MustCompile(`(?i)schema\s*=>\s*'([^']+)'`)

	// Table reference patterns for Trino 3-part names
	// Matches: FROM/JOIN catalog.schema.table or schema.table or table
	// with optional alias and handles quoted identifiers
	tableRefPattern = regexp.MustCompile(`(?i)(?:FROM|JOIN)\s+` +
		`([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*){0,2})` +
		`(?:\s+(?:AS\s+)?[a-zA-Z_][a-zA-Z0-9_]*)?(?:\s|,|$|ON|WHERE|GROUP|ORDER|LIMIT|LEFT|RIGHT|INNER|OUTER|CROSS|NATURAL)`)
)

// extractTablesWithRegex extracts table references using regex.
// Used as fallback when sqlparser fails (e.g., for Trino 3-part names).
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

		// Skip duplicates
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

// parseTablePath parses a dot-separated table path into TableRef.
func parseTablePath(path string) TableRef {
	parts := strings.Split(path, ".")
	ref := TableRef{FullPath: path}

	switch len(parts) {
	case 3:
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
