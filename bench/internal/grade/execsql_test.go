package grade

import "testing"

func TestNormalizeCell(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "\x00null"},
		{10.0, "10"},
		{10.5, "10.5"},
		{float32(3), "3"},
		{7, "7"},
		{int64(8), "8"},
		{true, "true"},
		{"  East  ", "East"},
		{[]int{1, 2}, "[1 2]"},
	}
	for _, c := range cases {
		if got := normalizeCell(c.in); got != c.want {
			t.Errorf("normalizeCell(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractSQL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"bare", "FINAL ANSWER: SELECT * FROM t", "SELECT * FROM t", true},
		{"trailing semicolon", "FINAL ANSWER: SELECT 1;", "SELECT 1", true},
		{"code fence", "FINAL ANSWER:\n```sql\nSELECT a FROM t\n```", "SELECT a FROM t", true},
		{"code fence no lang", "FINAL ANSWER: ```\nSELECT a\n```", "SELECT a", true},
		{"fenced multiline", "FINAL ANSWER:\n```sql\nSELECT a,\n  b\nFROM t\n```", "SELECT a,\n  b\nFROM t", true},
		{"unfenced trailing prose dropped", "FINAL ANSWER: SELECT status FROM t\n(one row per status)", "SELECT status FROM t", true},
		{"two fenced blocks take first", "FINAL ANSWER:\n```sql\nSELECT a FROM t\n```\n```\nexplanation\n```", "SELECT a FROM t", true},
		{"inline single backticks", "FINAL ANSWER: `SELECT a FROM t`", "SELECT a FROM t", true},
		{"glued keyword not a lang tag", "FINAL ANSWER:\n```SELECT a\nFROM t\n```", "SELECT a\nFROM t", true},
		{"with cte", "FINAL ANSWER: WITH x AS (SELECT 1) SELECT * FROM x", "WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"prose not sql", "FINAL ANSWER: the orders table", "", false},
		{"empty", "FINAL ANSWER:", "", false},
		{"last marker wins", "FINAL ANSWER: nope\nFINAL ANSWER: SELECT 2", "SELECT 2", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExtractSQL(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Errorf("ExtractSQL(%q) = %q,%v want %q,%v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestResultSetsEqual(t *testing.T) {
	row := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	tests := []struct {
		name      string
		got, want []map[string]any
		wantEqual bool
	}{
		{"identical", []map[string]any{row("region", "East", "n", 10.0)}, []map[string]any{row("region", "East", "n", 10.0)}, true},
		{"int vs float", []map[string]any{row("n", 10.0)}, []map[string]any{row("n", int64(10))}, true},
		{"row order insensitive",
			[]map[string]any{row("r", "A"), row("r", "B")},
			[]map[string]any{row("r", "B"), row("r", "A")}, true},
		{"column name insensitive",
			[]map[string]any{row("region", "East", "order_count", 5.0)},
			[]map[string]any{row("r", "East", "cnt", 5.0)}, true},
		{"different length", []map[string]any{row("n", 1.0)}, []map[string]any{row("n", 1.0), row("n", 2.0)}, false},
		{"different value", []map[string]any{row("n", 1.0)}, []map[string]any{row("n", 2.0)}, false},
		{"multiset counts",
			[]map[string]any{row("n", 1.0), row("n", 1.0)},
			[]map[string]any{row("n", 1.0), row("n", 2.0)}, false},
		{"both empty", []map[string]any{}, []map[string]any{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResultSetsEqual(tc.got, tc.want); got != tc.wantEqual {
				t.Errorf("ResultSetsEqual = %v, want %v", got, tc.wantEqual)
			}
		})
	}
}
