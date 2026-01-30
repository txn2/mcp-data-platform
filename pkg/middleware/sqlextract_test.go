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
			name: "JOIN query",
			sql:  "SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id",
			expected: []TableRef{
				{Table: "orders", FullPath: "orders", Source: "FROM"},
				{Table: "customers", FullPath: "customers", Source: "FROM"},
			},
		},
		{
			name: "multiple JOINs with qualified names",
			sql:  "SELECT * FROM catalog1.schema1.table1 t1 JOIN catalog2.schema2.table2 t2 ON t1.id = t2.id",
			expected: []TableRef{
				{Catalog: "catalog1", Schema: "schema1", Table: "table1", FullPath: "catalog1.schema1.table1", Source: "FROM"},
				{Catalog: "catalog2", Schema: "schema2", Table: "table2", FullPath: "catalog2.schema2.table2", Source: "FROM"},
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
			name: "ES raw_query with Cassandra JOIN (complex)",
			sql: `WITH es_response AS (
    SELECT result
    FROM TABLE(
        elasticsearch.system.raw_query(
            schema => 'default',
            index => 'jakes-sale-2024,jakes-sale-2025',
            query => '{}'
        )
    )
),
parsed_agg AS (
    SELECT * FROM es_response
)
SELECT * FROM parsed_agg agg
INNER JOIN cassandra.prod_fuse.location loc ON agg.location_id = loc.id`,
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "jakes-sale-2024", FullPath: "elasticsearch.default.jakes-sale-2024", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "default", Table: "jakes-sale-2025", FullPath: "elasticsearch.default.jakes-sale-2025", Source: "TABLE_FUNCTION"},
				{Catalog: "cassandra", Schema: "prod_fuse", Table: "location", FullPath: "cassandra.prod_fuse.location", Source: "FROM"},
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

func TestExtractCTENames(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected map[string]bool
	}{
		{
			name:     "no CTEs",
			sql:      "SELECT * FROM users",
			expected: map[string]bool{},
		},
		{
			name:     "single CTE",
			sql:      "WITH temp AS (SELECT 1) SELECT * FROM temp",
			expected: map[string]bool{"temp": true},
		},
		{
			name: "multiple CTEs",
			sql:  "WITH cte1 AS (SELECT 1), cte2 AS (SELECT 2) SELECT * FROM cte1 JOIN cte2",
			expected: map[string]bool{
				"cte1": true,
				"cte2": true,
			},
		},
		{
			name: "nested CTEs with different formatting",
			sql: `WITH es_response AS (
				SELECT * FROM elasticsearch
			),
			parsed_agg AS (
				SELECT * FROM es_response
			)
			SELECT * FROM parsed_agg`,
			expected: map[string]bool{
				"es_response": true,
				"parsed_agg":  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractCTENames(tt.sql)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d CTEs, got %d: %v", len(tt.expected), len(result), result)
				return
			}

			for name := range tt.expected {
				if !result[name] {
					t.Errorf("expected CTE %q to be found", name)
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
			name:     "not a raw_query",
			sql:      "SELECT * FROM regular_table",
			expected: nil,
		},
		{
			name:     "raw_query without index parameter",
			sql:      "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'default', query => '{}'))",
			expected: nil,
		},
		{
			name: "indices with empty entries after split",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(index => 'idx1, , idx2', query => '{}'))",
			expected: []TableRef{
				{Catalog: "elasticsearch", Schema: "default", Table: "idx1", FullPath: "elasticsearch.default.idx1", Source: "TABLE_FUNCTION"},
				{Catalog: "elasticsearch", Schema: "default", Table: "idx2", FullPath: "elasticsearch.default.idx2", Source: "TABLE_FUNCTION"},
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
			}
		})
	}
}

func TestExtractTablesWithRegex(t *testing.T) {
	t.Run("duplicate tables deduplicated", func(t *testing.T) {
		sql := "SELECT * FROM users u1 JOIN users u2 ON u1.id = u2.id"
		result := extractTablesWithRegex(sql)
		// Should only have one entry for "users"
		if len(result) != 1 {
			t.Errorf("expected 1 table (deduplicated), got %d: %+v", len(result), result)
		}
		if len(result) > 0 && result[0].Table != "users" {
			t.Errorf("expected users, got %s", result[0].Table)
		}
	})

	t.Run("no matches returns nil", func(t *testing.T) {
		sql := "SELECT 1 + 1"
		result := extractTablesWithRegex(sql)
		if result != nil {
			t.Errorf("expected nil, got %+v", result)
		}
	})
}
