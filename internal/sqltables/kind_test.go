package sqltables

import "testing"

func TestStatementKind(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM t":                        "select",
		"  show schemas":                         "show",
		"INSERT INTO t VALUES (1)":               "insert",
		"WITH x AS (SELECT 1) SELECT * FROM x":   "with",
		"(SELECT 1) UNION (SELECT 2)":            "select",
		"-- a note\nUPDATE t SET a = 'secret'":   "update",
		"/* hint */ DELETE FROM t WHERE id = 42": "delete",
		"begin":                                  "begin",
		"COMMIT;":                                "commit",
		"FROBNICATE t":                           KindOther,
		"'quoted' SELECT":                        KindOther,
		"-- only a comment":                      KindOther,
		"/* unterminated":                        KindOther,
		"":                                       KindOther,
	}
	for sql, want := range cases {
		if got := StatementKind(sql); got != want {
			t.Errorf("StatementKind(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestSummary(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM hive.sales.orders WHERE id = 'x-123'":                      "SELECT hive.sales.orders",
		"SELECT * FROM a JOIN b ON a.id = b.id":                                   "SELECT a b",
		"SELECT * FROM a JOIN b ON 1=1 JOIN c ON 1=1 JOIN d ON 1=1 JOIN e ON 1=1": "SELECT a b c",
		"CREATE TABLE x (a int)":                                                  "CREATE",
		"FROBNICATE 'secret-text'":                                                "OTHER",
	}
	for sql, want := range cases {
		if got := Summary(sql); got != want {
			t.Errorf("Summary(%q) = %q, want %q", sql, got, want)
		}
	}
}
