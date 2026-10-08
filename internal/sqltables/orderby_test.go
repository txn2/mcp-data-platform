package sqltables

import "testing"

func TestHasTopLevelOrderBy(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want bool
	}{
		{"SELECT id FROM t ORDER BY id", true},
		{"select id from t order\n  by id limit 5", true},
		{"SELECT a FROM x UNION ALL SELECT a FROM y ORDER BY a", true},
		{"SELECT id FROM t ORDER /* the key */ BY id", true},
		{"SELECT id FROM t", false},
		{"SELECT id FROM (SELECT id FROM t ORDER BY id) s", false},
		{"WITH s AS (SELECT id FROM t ORDER BY id) SELECT id FROM s", false},
		{"SELECT id, row_number() OVER (ORDER BY id) FROM t", false},
		{"SELECT 'order by' AS note FROM t", false},
		{"SELECT 'it''s order by' FROM t", false},
		{`SELECT "order by" FROM t`, false},
		{"SELECT id FROM t -- ORDER BY id", false},
		{"SELECT id FROM t /* ORDER BY id */", false},
		{"SELECT id FROM t /* unterminated ORDER BY id", false},
		{"SELECT 'unterminated ORDER BY id", false},
		{"SELECT id FROM t -- trailing\nORDER BY id", true},
		{"SELECT reorder by FROM t", false},
		{"SELECT id FROM t) ORDER BY id", true},
		{"", false},
	} {
		if got := HasTopLevelOrderBy(tc.sql); got != tc.want {
			t.Errorf("HasTopLevelOrderBy(%q) = %v; want %v", tc.sql, got, tc.want)
		}
	}
}
