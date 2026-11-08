package sqltables

import (
	"slices"
	"testing"
)

func tableNames(sql string) []string {
	refs := Extract(sql)
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.FullPath)
	}
	return out
}

// FROM is also an argument keyword of a few functions, and the second half of
// IS DISTINCT FROM. What follows it there is a column (#2547).
func TestExtractDoesNotReadAFunctionsFromAsATable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sql  string
		want []string
	}{
		{"extract", "SELECT client, ceil(extract(epoch FROM ts) / 7200) AS bucket FROM myschema.samples", []string{"myschema.samples"}},
		{"extract upper case", "SELECT EXTRACT(YEAR FROM order_date) FROM sales.orders", []string{"sales.orders"}},
		{"substring", "SELECT substring(name FROM 1 FOR 3) FROM people", []string{"people"}},
		{"substring of a column", "SELECT substring(code FROM pos) FROM people", []string{"people"}},
		{"trim both", "SELECT trim(BOTH FROM label) FROM items", []string{"items"}},
		{"trim leading", "SELECT trim(LEADING 'x' FROM label) FROM items", []string{"items"}},
		{"overlay", "SELECT overlay(a PLACING b FROM start_at) FROM t1", []string{"t1"}},
		{"in a where clause", "SELECT 1 FROM t1 WHERE extract(year FROM created) = 2026", []string{"t1"}},
		{"nested calls", "SELECT round(extract(epoch FROM (a - b))) FROM t1", []string{"t1"}},
		{"inside a subquery", "SELECT * FROM (SELECT extract(day FROM d) AS x FROM inner_t) s", []string{"inner_t"}},
		{"is distinct from", "SELECT 1 FROM t1 WHERE a IS DISTINCT FROM b", []string{"t1"}},
		{"is not distinct from", "SELECT 1 FROM t1 WHERE a IS NOT DISTINCT FROM b", []string{"t1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tableNames(tc.sql); !slices.Equal(got, tc.want) {
				t.Errorf("tables = %v, want %v", got, tc.want)
			}
		})
	}
}

// A FROM inside parentheses still names a table when the parentheses hold a
// statement of their own.
func TestExtractKeepsTheFromOfANestedStatement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sql  string
		want []string
	}{
		{"subquery", "SELECT * FROM a WHERE id IN (SELECT id FROM b)", []string{"a", "b"}},
		{"cte", "WITH c AS (SELECT * FROM a) SELECT * FROM c JOIN b ON true", []string{"a", "b"}},
		{"delete in a cte", "WITH d AS (DELETE FROM a RETURNING id) SELECT * FROM d", []string{"a"}},
		{"top level delete", "DELETE FROM a WHERE id = 1", []string{"a"}},
		{"parenthesis in a literal", "SELECT '(' FROM a", []string{"a"}},
		{"extract beside a subquery", "SELECT extract(year FROM d), (SELECT max(x) FROM b) FROM a", []string{"b", "a"}},
		{"comment holding a parenthesis", "SELECT 1 /* ( */ FROM a", []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tableNames(tc.sql); !slices.Equal(got, tc.want) {
				t.Errorf("tables = %v, want %v", got, tc.want)
			}
		})
	}
}
