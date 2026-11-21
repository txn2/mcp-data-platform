package sqltables

import "testing"

func TestIsRead(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                               true,
		"  -- note\n/* c */ ( select * from t )": true,
		"WITH x AS (SELECT 1) SELECT * FROM x":   true,
		"VALUES (1), (2)":                        true,
		"TABLE hive.s.t":                         true,
		"SHOW TABLES":                            true,
		"DESCRIBE hive.s.t":                      true,
		"EXPLAIN SELECT 1":                       true,
		"EXPLAIN (TYPE IO, FORMAT JSON) INSERT INTO t SELECT 1": true,
		"explain /* c */ (type logical) select 1":               true,
		"EXPLAIN INSERT INTO t VALUES (1)":                      true,

		"EXPLAIN ANALYZE INSERT INTO t SELECT 1":                  false,
		"explain analyze verbose create table t as select 1":      false,
		"EXPLAIN /* c */ ANALYZE SELECT 1":                        false,
		"EXPLAIN EXPLAIN ANALYZE INSERT INTO t VALUES (1)":        false,
		"EXPLAIN (TYPE IO) /* c */ EXPLAIN ANALYZE DELETE FROM t": false,
		"EXPLAIN /* c */ (TYPE IO) EXPLAIN ANALYZE DELETE FROM t": false,
		"EXPLAIN (TYPE IO) ANALYZE t":                             false,
		"EXPLAIN (TYPE IO":                                        false,
		"EXPLAIN":                                                 false,
		"REFRESH MATERIALIZED VIEW mv":                            false,
		"COMMENT ON TABLE t IS 'x'":                               false,
		"ANALYZE t":                                               false,
		"DENY SELECT ON t TO u":                                   false,
		"INSERT INTO t VALUES (1)":                                false,
		"CALL system.sync_partition_metadata('s','t','ADD')":      false,
		"SET SESSION query_max_run_time = '1h'":                   false,
		"USE hive.s":                                              false,
		"/* never ends SELECT 1":                                  false,
		"":                                                        false,
		"   ":                                                     false,
	}
	for sql, want := range cases {
		if got := IsRead(sql); got != want {
			t.Errorf("IsRead(%q) = %v, want %v", sql, got, want)
		}
	}
}
