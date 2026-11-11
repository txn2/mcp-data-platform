package sqlident

import "testing"

func TestReadsEveryColumn(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM hive.sales.customers", true},
		{"select\n*\nfrom t", true},
		{"SELECT DISTINCT * FROM t", true},
		{"SELECT id, * FROM t", true},
		{"SELECT c.* FROM t c JOIN u ON c.id = u.id", true},
		{"SELECT a.id, b.* FROM a JOIN b ON a.id = b.id", true},
		{"SELECT region, count(*) FROM t GROUP BY region", false},
		{"SELECT count( * ) FROM t", false},
		{"SELECT price * qty AS total FROM t", false},
		{"SELECT (price) * 2 FROM t", false},
		{"SELECT '*' AS star, 'a, *' FROM t", false},
		{`SELECT "*" FROM t`, false},
		{"SELECT id FROM t -- select *\n", false},
		{"SELECT id /* select * */ FROM t", false},
		{"SELECT id FROM t", false},
		{"", false},
		{"*", false},
	}
	for _, tt := range tests {
		if got := ReadsEveryColumn(tt.sql); got != tt.want {
			t.Errorf("ReadsEveryColumn(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}
