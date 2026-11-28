package sqltables

import (
	"reflect"
	"strings"
	"testing"
)

// Test constants for SQL extraction tests.
const (
	sqlTestSourceFROM    = "FROM"
	sqlTestCatalogES     = "elasticsearch"
	sqlTestSchemaDefault = "default"
	sqlTestSourceTF      = "TABLE_FUNCTION"
)

// assertTableRef validates a single Ref against expected values.
func assertTableRef(t *testing.T, idx int, got, exp Ref) {
	t.Helper()
	if got.Catalog != exp.Catalog {
		t.Errorf("table[%d] catalog: expected %q, got %q", idx, exp.Catalog, got.Catalog)
	}
	if got.Schema != exp.Schema {
		t.Errorf("table[%d] schema: expected %q, got %q", idx, exp.Schema, got.Schema)
	}
	if got.Table != exp.Table {
		t.Errorf("table[%d] table: expected %q, got %q", idx, exp.Table, got.Table)
	}
	if got.FullPath != exp.FullPath {
		t.Errorf("table[%d] fullPath: expected %q, got %q", idx, exp.FullPath, got.FullPath)
	}
	if got.Source != exp.Source {
		t.Errorf("table[%d] source: expected %q, got %q", idx, exp.Source, got.Source)
	}
}

func TestExtractTablesFromSQL(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected []Ref
	}{
		{
			name: "simple SELECT",
			sql:  "SELECT * FROM users",
			expected: []Ref{
				{Table: "users", FullPath: "users", Source: sqlTestSourceFROM},
			},
		},
		{
			name: "two-part name (schema.table)",
			sql:  "SELECT * FROM public.users",
			expected: []Ref{
				{Schema: "public", Table: "users", FullPath: "public.users", Source: sqlTestSourceFROM},
			},
		},
		{
			name: "three-part name (catalog.schema.table)",
			sql:  "SELECT * FROM cassandra.prod_fuse.location",
			expected: []Ref{
				{Catalog: "cassandra", Schema: "prod_fuse", Table: "location", FullPath: "cassandra.prod_fuse.location", Source: sqlTestSourceFROM},
			},
		},
		{
			name: "JOIN query",
			sql:  "SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id",
			expected: []Ref{
				{Table: "orders", FullPath: "orders", Source: sqlTestSourceFROM},
				{Table: "customers", FullPath: "customers", Source: sqlTestSourceFROM},
			},
		},
		{
			name: "multiple JOINs with qualified names",
			sql:  "SELECT * FROM catalog1.schema1.table1 t1 JOIN catalog2.schema2.table2 t2 ON t1.id = t2.id",
			expected: []Ref{
				{Catalog: "catalog1", Schema: "schema1", Table: "table1", FullPath: "catalog1.schema1.table1", Source: sqlTestSourceFROM},
				{Catalog: "catalog2", Schema: "schema2", Table: "table2", FullPath: "catalog2.schema2.table2", Source: sqlTestSourceFROM},
			},
		},
		{
			name:     "ES raw_query single index",
			sql:      "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'default', index => 'jakes-sale-2025', query => '{}'))",
			expected: []Ref{{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "jakes-sale-2025", FullPath: "elasticsearch.default.jakes-sale-2025", Source: sqlTestSourceTF}},
		},
		{
			name: "ES raw_query multiple indices",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'sales', index => 'idx1,idx2,idx3', query => '{}'))",
			expected: []Ref{
				{Catalog: sqlTestCatalogES, Schema: "sales", Table: "idx1", FullPath: "elasticsearch.sales.idx1", Source: sqlTestSourceTF},
				{Catalog: sqlTestCatalogES, Schema: "sales", Table: "idx2", FullPath: "elasticsearch.sales.idx2", Source: sqlTestSourceTF},
				{Catalog: sqlTestCatalogES, Schema: "sales", Table: "idx3", FullPath: "elasticsearch.sales.idx3", Source: sqlTestSourceTF},
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
			expected: []Ref{
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "jakes-sale-2024", FullPath: "elasticsearch.default.jakes-sale-2024", Source: sqlTestSourceTF},
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "jakes-sale-2025", FullPath: "elasticsearch.default.jakes-sale-2025", Source: sqlTestSourceTF},
				{Catalog: "cassandra", Schema: "prod_fuse", Table: "location", FullPath: "cassandra.prod_fuse.location", Source: sqlTestSourceFROM},
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
			result := Extract(tt.sql)

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d tables, got %d: %+v", len(tt.expected), len(result), result)
				return
			}

			for i, exp := range tt.expected {
				assertTableRef(t, i, result[i], exp)
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
		// The forms between a name and its body #2840 found reported as tables.
		{
			name:     "materialized",
			sql:      "WITH b AS MATERIALIZED (SELECT 1 AS id) SELECT * FROM b",
			expected: map[string]bool{"b": true},
		},
		{
			name:     "not materialized, no space before the body",
			sql:      "WITH b AS NOT MATERIALIZED(SELECT 1) SELECT * FROM b",
			expected: map[string]bool{"b": true},
		},
		{
			name:     "recursive with a column list",
			sql:      "WITH RECURSIVE tree (id, parent) AS (SELECT 1, 0) SELECT * FROM tree",
			expected: map[string]bool{"tree": true},
		},
		{
			name:     "quoted name, folded",
			sql:      `WITH "Batch" AS (SELECT 1), Up AS(SELECT 2) SELECT * FROM up`,
			expected: map[string]bool{"batch": true, "up": true},
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
		expected []Ref
	}{
		{
			name: "basic raw_query",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(schema => 'default', index => 'my-index', query => '{}'))",
			expected: []Ref{
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "my-index", FullPath: "elasticsearch.default.my-index", Source: sqlTestSourceTF},
			},
		},
		{
			name: "comma-separated indices",
			sql:  "SELECT * FROM TABLE(elasticsearch.system.raw_query(index => 'idx1, idx2', query => '{}'))",
			expected: []Ref{
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "idx1", FullPath: "elasticsearch.default.idx1", Source: sqlTestSourceTF},
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "idx2", FullPath: "elasticsearch.default.idx2", Source: sqlTestSourceTF},
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
			expected: []Ref{
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "idx1", FullPath: "elasticsearch.default.idx1", Source: sqlTestSourceTF},
				{Catalog: sqlTestCatalogES, Schema: sqlTestSchemaDefault, Table: "idx2", FullPath: "elasticsearch.default.idx2", Source: sqlTestSourceTF},
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

// TestExtract_DataModifyingWithStatement is #2840's statement: names its WITH
// clause defines are not tables, whatever form defines them, and the tables
// its CTEs read are.
func TestExtract_DataModifyingWithStatement(t *testing.T) {
	sql := `WITH b AS MATERIALIZED (SELECT gen_random_uuid() AS id),
old AS (SELECT r.container_id, jsonb_build_object('a', r.a) AS v FROM container_rate r JOIN container_grid g ON g.id = r.grid_id),
up AS (
    INSERT INTO container_rate (container_id, a) SELECT c.id, 1 FROM container c
    ON CONFLICT (container_id) DO UPDATE SET a = EXCLUDED.a
    RETURNING container_id, a
)
INSERT INTO rate_change (batch_id, container_id, old_values, new_values)
SELECT b.id, up.container_id, old.v, jsonb_build_object('a', up.a)
  FROM up
 CROSS JOIN B
  LEFT JOIN old ON old.container_id = up.container_id
RETURNING container_id, batch_id::text AS batch_id`
	refs := Extract(sql)
	got := make([]string, 0, len(refs))
	for _, r := range refs {
		got = append(got, r.FullPath)
	}
	want := []string{"container_rate", "container_grid", "container"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Extract = %v, want %v", got, want)
	}
}

// TestExtract_QuotedNames is #2849's finding: a table named with
// double-quoted parts is a table the statement reads, its parts kept as
// written, a dot inside a quoted part its own.
func TestExtract_QuotedNames(t *testing.T) {
	tests := []struct {
		sql  string
		want []Ref
	}{
		{`SELECT * FROM "Sales"."Orders"`, []Ref{{Schema: `"Sales"`, Table: `"Orders"`, FullPath: `"Sales"."Orders"`, Source: "FROM"}}},
		{`SELECT * FROM hive.hr."employees" JOIN "hive" . "hr" . "dept" ON true`, []Ref{
			{Catalog: "hive", Schema: "hr", Table: `"employees"`, FullPath: `hive.hr."employees"`, Source: "FROM"},
			{Catalog: `"hive"`, Schema: `"hr"`, Table: `"dept"`, FullPath: `"hive"."hr"."dept"`, Source: "FROM"},
		}},
		{`SELECT * FROM "odd.name"`, []Ref{{Table: `"odd.name"`, FullPath: `"odd.name"`, Source: "FROM"}}},
		{`SELECT * FROM "a""b"`, []Ref{{Table: `"a""b"`, FullPath: `"a""b"`, Source: "FROM"}}},
		{`WITH "q" AS (SELECT 1) SELECT * FROM "q"`, nil},
	}
	for _, tt := range tests {
		if got := Extract(tt.sql); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Extract(%s) = %#v, want %#v", tt.sql, got, tt.want)
		}
	}
}
