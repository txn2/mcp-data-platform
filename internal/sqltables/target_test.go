package sqltables

import "testing"

func TestWriteTarget(t *testing.T) {
	tests := []struct {
		sql  string
		want string
		ok   bool
	}{
		{"INSERT INTO hive.sales.daily SELECT * FROM hive.sales.orders", "hive.sales.daily", true},
		{"  insert into daily select 1", "daily", true},
		{"CREATE TABLE hive.s.t AS SELECT * FROM hive.s.u", "hive.s.t", true},
		{"CREATE TABLE IF NOT EXISTS s.t AS (SELECT 1)", "s.t", true},
		{"CREATE OR REPLACE TABLE a.b.c AS WITH x AS (SELECT 1) SELECT * FROM x", "a.b.c", true},
		{"CREATE VIEW a.b.v AS SELECT * FROM a.b.c", "a.b.v", true},
		{`CREATE TABLE "hive"."My Schema"."t" AS SELECT 1`, "hive.My Schema.t", true},
		{"MERGE INTO a.b.c t USING a.b.d s ON t.id = s.id WHEN MATCHED THEN DELETE", "a.b.c", true},
		{"CREATE TABLE a.b.c (id bigint)", "", false},
		{"SELECT * FROM a.b.c", "", false},
		{"DELETE FROM a.b.c", "", false},
		{"DROP TABLE a.b.c", "", false},
	}
	for _, tt := range tests {
		got, ok := WriteTarget(tt.sql)
		if ok != tt.ok {
			t.Errorf("WriteTarget(%q) ok = %v, want %v", tt.sql, ok, tt.ok)
			continue
		}
		if ok && got.FullPath != tt.want {
			t.Errorf("WriteTarget(%q) = %q, want %q", tt.sql, got.FullPath, tt.want)
		}
	}
}
