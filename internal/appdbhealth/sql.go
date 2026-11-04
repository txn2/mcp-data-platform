package appdbhealth

import (
	"strconv"
	"time"
)

// The statements a report runs. Each reads PostgreSQL's own catalog and
// statistics views and writes nothing; each casts what it returns to a type
// the toolkit maps to a plain JSON value. They read the database the
// connection reaches, except connections, which counts the server's.

const serverSQL = `SELECT current_setting('server_version') AS version,
 current_setting('server_version_num')::int8 AS version_num,
 current_database() AS database,
 pg_database_size(current_database())::int8 AS size_bytes,
 current_setting('max_connections')::int8 AS max_connections,
 (SELECT age(datfrozenxid)::int8 FROM pg_database WHERE datname = current_database()) AS xid_age,
 (SELECT COALESCE(sum(blks_hit)::float8 / NULLIF(sum(blks_hit) + sum(blks_read), 0), 0)
    FROM pg_stat_database WHERE datname = current_database()) AS cache_hit_ratio,
 pg_is_in_recovery() AS in_recovery,
 (SELECT stats_reset FROM pg_stat_database WHERE datname = current_database()) AS stats_reset`

const extensionsSQL = `SELECT extname AS name, extversion AS version FROM pg_extension ORDER BY extname`

const connectionsSQL = `SELECT COALESCE(state, 'unknown') AS state, count(*)::int8 AS n
FROM pg_stat_activity WHERE backend_type = 'client backend' GROUP BY 1 ORDER BY 1`

// tablesSQL lists the largest user tables. A partitioned parent holds no
// rows and no transaction ids of its own, so its age is zero.
const tablesSQL = `SELECT s.schemaname AS schema, s.relname AS name,
 pg_total_relation_size(s.relid)::int8 AS size_bytes, GREATEST(c.reltuples, 0)::int8 AS row_estimate,
 s.n_live_tup::int8 AS live_tuples, s.n_dead_tup::int8 AS dead_tuples,
 s.seq_scan::int8 AS seq_scans, COALESCE(s.idx_scan, 0)::int8 AS index_scans,
 s.last_vacuum, s.last_autovacuum, s.last_analyze, s.last_autoanalyze,
 CASE WHEN c.relkind IN ('r', 'm', 't') THEN age(c.relfrozenxid)::int8 ELSE 0 END AS xid_age
FROM pg_stat_user_tables s JOIN pg_class c ON c.oid = s.relid
ORDER BY pg_total_relation_size(s.relid) DESC, s.schemaname, s.relname LIMIT 200`

// unusedIndexesSQL lists valid indexes never scanned since the statistics
// were reset that back no constraint: dropping one would not change what the
// table accepts.
const unusedIndexesSQL = `SELECT s.schemaname AS schema, s.relname AS table, s.indexrelname AS name,
 pg_relation_size(s.indexrelid)::int8 AS size_bytes, s.idx_scan::int8 AS scans
FROM pg_stat_user_indexes s JOIN pg_index i ON i.indexrelid = s.indexrelid
WHERE s.idx_scan = 0 AND i.indisvalid AND NOT i.indisunique AND NOT i.indisprimary
 AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = s.indexrelid)
ORDER BY 4 DESC, 1, 2, 3 LIMIT 100`

// duplicateIndexesSQL groups a table's indexes by what they index: the same
// columns, operator classes, collations, expressions and predicate.
const duplicateIndexesSQL = `SELECT n.nspname AS schema, t.relname AS table,
 array_agg(ic.relname::text ORDER BY ic.relname) AS names, min(pg_get_indexdef(i.indexrelid)) AS definition
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
GROUP BY n.nspname, t.relname, i.indrelid, i.indkey::text, i.indclass::text, i.indcollation::text,
 COALESCE(pg_get_expr(i.indexprs, i.indrelid), ''), COALESCE(pg_get_expr(i.indpred, i.indrelid), '')
HAVING count(*) > 1 ORDER BY 1, 2 LIMIT 100`

const invalidIndexesSQL = `SELECT n.nspname AS schema, t.relname AS table, ic.relname AS name,
 pg_relation_size(i.indexrelid)::int8 AS size_bytes, 0::int8 AS scans
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE NOT i.indisvalid ORDER BY 1, 2, 3 LIMIT 100`

// sessionsSQL lists this database's client backends running a statement past
// longRunning, and every one idle in a transaction, leaving out the backend
// reading it.
func sessionsSQL(longRunning time.Duration) string {
	secs := strconv.FormatInt(int64(longRunning.Seconds()), decimal)
	return `SELECT pid::int8 AS pid, usename::text, COALESCE(application_name, '') AS application, state,
 EXTRACT(EPOCH FROM now() - COALESCE(xact_start, query_start))::float8 AS seconds,
 EXTRACT(EPOCH FROM now() - state_change)::float8 AS state_seconds,
 COALESCE(wait_event_type || ':' || wait_event, '') AS wait, query
FROM pg_stat_activity
WHERE datname = current_database() AND backend_type = 'client backend' AND pid <> pg_backend_pid()
 AND ((state = 'active' AND now() - query_start >= make_interval(secs => ` + secs + `))
   OR state LIKE 'idle in transaction%')
ORDER BY seconds DESC LIMIT 100`
}

const blockingSQL = `SELECT pid::int8 AS pid, usename::text, pg_blocking_pids(pid)::int8[] AS blocked_by,
 EXTRACT(EPOCH FROM now() - COALESCE(query_start, backend_start))::float8 AS seconds
FROM pg_stat_activity
WHERE datname = current_database() AND cardinality(pg_blocking_pids(pid)) > 0
ORDER BY seconds DESC LIMIT 100`

const replicationSQL = `SELECT COALESCE(application_name, '') AS name, COALESCE(state, '') AS state,
 COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn), 0)::int8 AS lag_bytes,
 COALESCE(EXTRACT(EPOCH FROM replay_lag), 0)::float8 AS lag_seconds
FROM pg_stat_replication ORDER BY 1`

const statementsSQL = `SELECT queryid::text AS query_id, calls::int8 AS calls,
 total_exec_time::float8 AS total_ms, mean_exec_time::float8 AS mean_ms, rows::int8 AS rows, query
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC LIMIT 20`
