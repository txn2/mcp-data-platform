package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractIdentifiers(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		wantSet  map[string]bool // identifiers that MUST be present
		wantSkip []string        // tokens that must NOT be present
	}{
		{
			name:    "simple SELECT",
			sql:     "SELECT id, name FROM users WHERE age > 30",
			wantSet: map[string]bool{"select": true, "id": true, "name": true, "from": true, "users": true, "where": true, "age": true},
		},
		{
			name:    "case insensitive",
			sql:     "SELECT Id, NAME from Users",
			wantSet: map[string]bool{"select": true, "id": true, "name": true, "from": true, "users": true},
		},
		{
			name:    "string literals are skipped",
			sql:     "SELECT * FROM t WHERE name = 'John O''Brien'",
			wantSet: map[string]bool{"select": true, "t": true, "name": true, "where": true},
			wantSkip: []string{
				"john",  // inside string literal
				"brien", // inside string literal
				"o",     // inside string literal (before escaped quote)
			},
		},
		{
			name:    "double-quoted identifiers are extracted",
			sql:     `SELECT "MyColumn" FROM "My Table"`,
			wantSet: map[string]bool{"select": true, "from": true, "mycolumn": true, "my table": true},
		},
		{
			name: "double-quoted escaped quotes",
			sql:  `SELECT "col""name" FROM t`,
			wantSet: map[string]bool{
				"select":   true,
				"from":     true,
				"t":        true,
				`col"name`: true,
			},
		},
		{
			name:     "block comments are skipped",
			sql:      "SELECT /* this is id, name */ col1 FROM t",
			wantSet:  map[string]bool{"select": true, "col1": true, "from": true, "t": true},
			wantSkip: []string{"this", "is"},
		},
		{
			name:     "line comments are skipped",
			sql:      "SELECT col1 -- this is a comment\nFROM t",
			wantSet:  map[string]bool{"select": true, "col1": true, "from": true, "t": true},
			wantSkip: []string{"this", "is", "a", "comment"},
		},
		{
			name:    "empty input",
			sql:     "",
			wantSet: map[string]bool{},
		},
		{
			name:    "only whitespace",
			sql:     "   \t\n  ",
			wantSet: map[string]bool{},
		},
		{
			name:    "numbers and operators are skipped",
			sql:     "SELECT 123 + col FROM t WHERE x = 42",
			wantSet: map[string]bool{"select": true, "col": true, "from": true, "t": true, "where": true, "x": true},
		},
		{
			name:    "dotted identifiers become separate tokens",
			sql:     "SELECT t.col1, s.col2 FROM myschema.mytable t",
			wantSet: map[string]bool{"select": true, "t": true, "col1": true, "s": true, "col2": true, "from": true, "myschema": true, "mytable": true},
		},
		{
			name:    "underscored identifiers",
			sql:     "SELECT _private, my_col_2 FROM my_table_v3",
			wantSet: map[string]bool{"select": true, "_private": true, "my_col_2": true, "from": true, "my_table_v3": true},
		},
		{
			name: "complex Trino ES raw_query with embedded JSON",
			sql: `SELECT JSON_EXTRACT_SCALAR(doc, '$.location_type_id') AS location_type_id,
                    JSON_EXTRACT_SCALAR(doc, '$.total_amount') AS total_amount
             FROM TABLE(elasticsearch.default.raw_query(
                    'transactions',
                    '{ "size": 0, "aggs": { "by_loc": { "terms": { "field": "location_type_id" }, "aggs": { "total": { "sum": { "field": "total_amount" } } } } } }'
                  )) t(doc)`,
			wantSet: map[string]bool{
				"select":              true,
				"json_extract_scalar": true,
				"doc":                 true,
				"as":                  true,
				"location_type_id":    true,
				"total_amount":        true,
				"from":                true,
				"table":               true,
				"elasticsearch":       true,
				"default":             true,
				"raw_query":           true,
				"t":                   true,
			},
			wantSkip: []string{
				"size",  // inside JSON string literal
				"aggs",  // inside JSON string literal
				"field", // inside JSON string literal
			},
		},
		{
			name:    "CROSS JOIN UNNEST pattern",
			sql:     "SELECT t.id, u.val FROM mytable t CROSS JOIN UNNEST(t.items) AS u(val)",
			wantSet: map[string]bool{"select": true, "t": true, "id": true, "u": true, "val": true, "from": true, "mytable": true, "cross": true, "join": true, "unnest": true, "items": true, "as": true},
		},
		{
			name:     "unterminated string at end",
			sql:      "SELECT col FROM t WHERE name = 'unterminated",
			wantSet:  map[string]bool{"select": true, "col": true, "from": true, "t": true, "where": true, "name": true},
			wantSkip: []string{"unterminated"},
		},
		{
			name:     "unterminated block comment at end",
			sql:      "SELECT col /* still going",
			wantSet:  map[string]bool{"select": true, "col": true},
			wantSkip: []string{"still", "going"},
		},
		{
			name:    "unterminated double-quoted identifier",
			sql:     `SELECT "never_closed`,
			wantSet: map[string]bool{"select": true, "never_closed": true},
		},
		{
			name:    "mixed comments and strings",
			sql:     "SELECT /* skip 'this' */ col -- 'and this'\nFROM t WHERE x = 'literal'",
			wantSet: map[string]bool{"select": true, "col": true, "from": true, "t": true, "where": true, "x": true},
			wantSkip: []string{
				"skip",    // inside block comment
				"and",     // inside line comment
				"literal", // inside string
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractIdentifiers(tt.sql)

			for id := range tt.wantSet {
				assert.True(t, got[id], "expected identifier %q to be present", id)
			}
			for _, id := range tt.wantSkip {
				assert.False(t, got[id], "expected identifier %q to NOT be present", id)
			}
		})
	}
}

func TestExtractIdentifiers_EmptyResult(t *testing.T) {
	got := ExtractIdentifiers("42 + 3.14 / (2 - 1)")
	// Only operators and numbers — no identifiers
	assert.Empty(t, got)
}

func TestSkipSingleQuoted_EscapedQuotes(t *testing.T) {
	// Verify internal helper directly: 'it''s a test'
	sql := "'it''s a test' rest"
	pos := skipSingleQuoted(sql, 0, len(sql))
	// Should end right after the closing quote, before " rest"
	assert.Equal(t, len("'it''s a test'"), pos)
}

func TestReadDoubleQuoted_EscapedQuotes(t *testing.T) {
	sql := `"col""name" rest`
	id, pos := readDoubleQuoted(sql, 0, len(sql))
	assert.Equal(t, `col"name`, id)
	assert.Equal(t, len(`"col""name"`), pos)
}
