package middleware

import (
	"testing"
)

func TestExtractTablesFromSQL(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected []TableRef
	}{
		{
			name: "simple SELECT",
			sql:  "SELECT * FROM users",
			expected: []TableRef{
				{Table: "users", FullPath: "users", Source: "FROM"},
			},
		},
		{
			name: "two-part name (schema.table)",
			sql:  "SELECT * FROM public.users",
			expected: []TableRef{
				{Schema: "public", Table: "users", FullPath: "public.users", Source: "FROM"},
			},
		},
		{
			name: "three-part name (catalog.schema.table)",
			sql:  "SELECT * FROM cassandra.prod_fuse.location",
			expected: []TableRef{
				{Catalog: "cassandra", Schema: "prod_fuse", Table: "location", FullPath: "cassandra.prod_fuse.location", Source: "FROM"},
			},
		},
		{
			name: "SELECT with alias",
			sql:  "SELECT u.name FROM users u",
			expected: []TableRef{
				{Table: "users", FullPath: "users", Source: "FROM"},
			},
		},
		{
			name: "JOIN query",
			sql:  "SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id",
			expected: []TableRef{
				{Table: "orders", FullPath: "orders", Source: "FROM"},
				{Table: "customers", FullPath: "customers", Source: "FROM"},
			},
		},
		{
			name: "multiple JOINs",
			sql:  "SELECT * FROM a JOIN b ON a.id = b.a_id JOIN c ON b.id = c.b_id",
			expected: []TableRef{
				{Table: "a", FullPath: "a", Source: "FROM"},
				{Table: "b", FullPath: "b", Source: "FROM"},
				{Table: "c", FullPath: "c", Source: "FROM"},
			},
		},
		{
			name: "LEFT JOIN",
			sql:  "SELECT * FROM users LEFT JOIN orders ON users.id = orders.user_id",
			expected: []TableRef{
				{Table: "users", FullPath: "users", Source: "FROM"},
				{Table: "orders", FullPath: "orders", Source: "FROM"},
			},
		},
		{
			name: "qualified names in JOIN",
			sql:  "SELECT * FROM catalog1.schema1.table1 t1 JOIN catalog2.schema2.table2 t2 ON t1.id = t2.id",
			expected: []TableRef{
				{Catalog: "catalog1", Schema: "schema1", Table: "table1", FullPath: "catalog1.schema1.table1", Source: "FROM"},
				{Catalog: "catalog2", Schema: "schema2", Table: "table2", FullPath: "catalog2.schema2.table2", Source: "FROM"},
			},
		},
		{
			name: "subquery in FROM not yet supported",
			sql:  "SELECT * FROM (SELECT * FROM inner_table) AS subq",
			expected: []TableRef{
				{Table: "inner_table", FullPath: "inner_table", Source: "FROM"},
			},
		},
		{
			name:     "ES raw_query single index",
			sql:      "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'default', index => 'jakes-sale-2025', query => '{}'))",
			expected: []TableRef{{Catalog: "elasticsearch", Schema: "default", Table: "jakes-sale-2025", FullPath: "elasticsearch.default.jakes-sale-2025", Source: "TABLE_FUNCTION"}},
		},
		{
			name: "ES raw_query multiple indices",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'sales', index => 'idx1,idx2,idx3', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "sales", Table: "idx1", FullPath: "elasticsearch.sales.idx1", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "sales", Table: "idx2", FullPath: "elasticsearch.sales.idx2", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "sales", Table: "idx3", FullPath: "elasticsearch.sales.idx3", Source: "TABLE_FUNCTION"},
			},
		},
		{
			name: "ES raw_query default schema",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(index => 'my-index', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "my-index", FullPath: "elasticsearch.default.my-index", Source: "TABLE_FUNCTION"},
			},
		},
		{
			name:     "invalid SQL returns empty",
			sql:      "NOT VALID SQL AT ALL",
			expected: nil,
		},
		{
			name:     "empty SQL",
			sql:      "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractTablesFromSQL(tt.sql)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d tables, got %d: %+v", len(tt.expected), len(result), result)
				return
			}

			for i, exp := range tt.expected {
				got := result[i]
				if got.Catalog != exp.Catalog {
					t.Errorf("table[%d] catalog: expected %q, got %q", i, exp.Catalog, got.Catalog)
				}
				if got.Schema != exp.Schema {
					t.Errorf("table[%d] schema: expected %q, got %q", i, exp.Schema, got.Schema)
				}
				if got.Table != exp.Table {
					t.Errorf("table[%d] table: expected %q, got %q", i, exp.Table, got.Table)
				}
				if got.FullPath != exp.FullPath {
					t.Errorf("table[%d] fullPath: expected %q, got %q", i, exp.FullPath, got.FullPath)
				}
				if got.Source != exp.Source {
					t.Errorf("table[%d] source: expected %q, got %q", i, exp.Source, got.Source)
				}
			}
		})
	}
}

func TestExtractESRawQuery(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected []TableRef
	}{
		{
			name: "basic raw_query",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'default', index => 'my-index', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "my-index", FullPath: "elasticsearch.default.my-index", Source: "TABLE_FUNCTION"},
			},
		},
		{
			name: "comma-separated indices",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(index => 'idx1, idx2', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "idx1", FullPath: "elasticsearch.default.idx1", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "default", Table: "idx2", FullPath: "elasticsearch.default.idx2", Source: "TABLE_FUNCTION"},
			},
		},
		{
			name: "with spaces in comma list",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(index => 'a,  b,c  ', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "a", FullPath: "elasticsearch.default.a", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "default", Table: "b", FullPath: "elasticsearch.default.b", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "default", Table: "c", FullPath: "elasticsearch.default.c", Source: "TABLE_FUNCTION"},
			},
		},
		{
			name: "custom schema",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'analytics', index => 'events', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "analytics", Table: "events", FullPath: "elasticsearch.analytics.events", Source: "TABLE_FUNCTION"},
			},
		},
		{
			name:     "not a raw_query",
			sql:      "SELECT * FROM regular_table",
			expected: nil,
		},
		{
			name:     "raw_query without index",
			sql:      "SELECT * FROM TABLE(elasticsearch.system.raw_query(query => '{}'))",
			expected: nil,
		},
		{
			name: "case insensitive",
			sql:  "SELECT * FROM TABLE(ELASTICSEARCH.SYSTEM.RAW_QUERY(INDEX => 'test', QUERY => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "test", FullPath: "elasticsearch.default.test", Source: "TABLE_FUNCTION"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractESRawQuery(tt.sql)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d refs, got %d: %+v", len(tt.expected), len(result), result)
				return
			}

			for i, exp := range tt.expected {
				got := result[i]
				if got.Catalog != exp.Catalog {
					t.Errorf("ref[%d] catalog: expected %q, got %q", i, exp.Catalog, got.Catalog)
				}
				if got.Schema != exp.Schema {
					t.Errorf("ref[%d] schema: expected %q, got %q", i, exp.Schema, got.Schema)
				}
				if got.Table != exp.Table {
					t.Errorf("ref[%d] table: expected %q, got %q", i, exp.Table, got.Table)
				}
				if got.FullPath != exp.FullPath {
					t.Errorf("ref[%d] fullPath: expected %q, got %q", i, exp.FullPath, got.FullPath)
				}
			}
		})
	}
}

func TestSplitCatalogSchema(t *testing.T) {
	tests := []struct {
		input       string
		wantCatalog string
		wantSchema  string
	}{
		{"schema", "", "schema"},
		{"catalog.schema", "catalog", "schema"},
		{"a.b.c", "a", "b.c"}, // Only first dot splits
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotCatalog, gotSchema := splitCatalogSchema(tt.input)
			if gotCatalog != tt.wantCatalog {
				t.Errorf("catalog: expected %q, got %q", tt.wantCatalog, gotCatalog)
			}
			if gotSchema != tt.wantSchema {
				t.Errorf("schema: expected %q, got %q", tt.wantSchema, gotSchema)
			}
		})
	}
}

func TestBuildFullPath(t *testing.T) {
	tests := []struct {
		catalog  string
		schema   string
		table    string
		expected string
	}{
		{"", "", "table", "table"},
		{"", "schema", "table", "schema.table"},
		{"catalog", "schema", "table", "catalog.schema.table"},
		{"catalog", "", "table", "catalog.table"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := buildFullPath(tt.catalog, tt.schema, tt.table)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
