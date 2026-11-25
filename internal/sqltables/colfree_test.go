package sqltables

import "testing"

func TestColumnFree(t *testing.T) {
	tests := []struct {
		sql   string
		shape string
		ok    bool
	}{
		{"SELECT * FROM t LIMIT 80", ShapeStar, true},
		{"select * from hive.sales.orders", ShapeStar, true},
		{`SELECT * FROM "hive"."sales"."Orders" o LIMIT 5;`, ShapeStar, true},
		{"SELECT * FROM t OFFSET 10 LIMIT 5", ShapeStar, true},
		{"SELECT * FROM t FETCH FIRST 10 ROWS ONLY", ShapeStar, true},
		{"-- look first\nSELECT * /* all */ FROM t", ShapeStar, true},
		{"SELECT count(*) FROM t", ShapeCount, true},
		{"SELECT COUNT( * ) AS n FROM sales.orders", ShapeCount, true},
		{"SELECT * FROM t WHERE id = 1", "", false},
		{"SELECT * FROM t limit 5 where x", "", false},
		{"SELECT * FROM a JOIN b ON a.id = b.id", "", false},
		{"SELECT * FROM a, b", "", false},
		{"SELECT id FROM t", "", false},
		{"SELECT count(id) FROM t", "", false},
		{"SELECT * FROM t ORDER BY 1", "", false},
		{"SELECT count(*) FROM t GROUP BY region", "", false},
		{"SELECT * FROM t UNION SELECT * FROM u", "", false},
		{"SELECT * FROM (SELECT id FROM t)", "", false},
		{"INSERT INTO t SELECT * FROM u", "", false},
		{"SELECT * FROM t where", "", false},
		{"SELECT '*' FROM t", "", false},
	}
	for _, tt := range tests {
		shape, ok := ColumnFree(tt.sql)
		if ok != tt.ok || shape != tt.shape {
			t.Errorf("ColumnFree(%q) = %q, %v; want %q, %v", tt.sql, shape, ok, tt.shape, tt.ok)
		}
	}
}
