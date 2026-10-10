package capacity

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// Tables is every platform table that grows with use and is reported by name.
// A table the platform keeps small (settings, definitions, the OAuth state) is
// left out; a table a migration has not created yet reads as absent, never as
// an error. The names are the migrations' own, which is what bounds the table
// label.
//
//nolint:gochecknoglobals // a read-only list.
var Tables = []string{
	"audit_logs",
	"call_records",
	"script_runs",
	"script_versions",
	"script_recordings",
	"notifications",
	"index_jobs",
	"connection_auth_events",
	"memory_records",
	"portal_knowledge_pages",
	"portal_knowledge_page_versions",
	"knowledge_changesets",
	"portal_assets",
	"portal_asset_versions",
	"resources",
	"resource_versions",
	"prompt_versions",
	"webhook_windows",
	"webhook_rejections",
	"config_changelog",
	"content_producers",
	"tool_embeddings",
	"api_catalog_operation_embeddings",
	"graphql_operation_embeddings",
	"portal_knowledge_page_embedding_chunks",
	"script_embedding_chunks",
	"catalog_datasets",
}

// tablesQuery reads, per listed table, its size on disk with its indexes and
// TOAST, and the planner's row estimate. A partitioned table (audit_logs) is
// summed over its leaf partitions, since the parent holds no data;
// pg_partition_tree answers nothing for an ordinary table, which is read as
// itself. reltuples is -1 before the first ANALYZE and contributes nothing.
// COUNT(*) is never run.
const tablesQuery = `SELECT t.name,
       COALESCE(SUM(pg_total_relation_size(c.oid)), 0)::bigint,
       COALESCE(SUM(GREATEST(c.reltuples, 0)), 0)::bigint
FROM unnest($1::text[]) AS t(name)
JOIN pg_class root ON root.oid = to_regclass(t.name)
JOIN LATERAL (
    SELECT root.oid AS relid WHERE root.relkind <> 'p'
    UNION ALL
    SELECT pt.relid FROM pg_partition_tree(root.oid) pt WHERE root.relkind = 'p' AND pt.isleaf
) leaf ON TRUE
JOIN pg_class c ON c.oid = leaf.relid
GROUP BY t.name`

// vectorIndexesQuery reads the size of every HNSW index in the schema. The
// names are the migrations' own.
const vectorIndexesQuery = `SELECT i.relname, pg_relation_size(i.oid)::bigint
FROM pg_class i
JOIN pg_am a ON a.oid = i.relam
JOIN pg_namespace n ON n.oid = i.relnamespace
WHERE i.relkind = 'i' AND a.amname = 'hnsw' AND n.nspname = current_schema()`

// xidAgeQuery is how many transactions old the database's oldest unfrozen
// transaction id is. PostgreSQL stops accepting writes as it nears 2^31.
const xidAgeQuery = `SELECT age(datfrozenxid)::bigint FROM pg_database WHERE datname = current_database()`

// SampleTables reads the table sizes, the vector index sizes and the
// transaction id age. Each read is a catalog lookup; none scans a table.
func SampleTables(ctx context.Context, db *sql.DB) (observability.DatabaseCapacity, error) {
	var out observability.DatabaseCapacity
	rows, err := db.QueryContext(ctx, tablesQuery, pq.Array(Tables))
	if err != nil {
		return out, fmt.Errorf("reading table sizes: %w", err)
	}
	if err := scanRows(rows, func(r *sql.Rows) error {
		var s observability.TableCapacity
		if err := r.Scan(&s.Table, &s.Bytes, &s.Rows); err != nil {
			return err //nolint:wrapcheck // wrapped by scanRows' caller
		}
		out.Tables = append(out.Tables, s)
		return nil
	}); err != nil {
		return out, fmt.Errorf("reading table sizes: %w", err)
	}
	rows, err = db.QueryContext(ctx, vectorIndexesQuery)
	if err != nil {
		return out, fmt.Errorf("reading vector index sizes: %w", err)
	}
	if err := scanRows(rows, func(r *sql.Rows) error {
		var s observability.IndexCapacity
		if err := r.Scan(&s.Index, &s.Bytes); err != nil {
			return err //nolint:wrapcheck // wrapped by scanRows' caller
		}
		out.VectorIndexes = append(out.VectorIndexes, s)
		return nil
	}); err != nil {
		return out, fmt.Errorf("reading vector index sizes: %w", err)
	}
	if err := db.QueryRowContext(ctx, xidAgeQuery).Scan(&out.TransactionIDAge); err != nil {
		return out, fmt.Errorf("reading transaction id age: %w", err)
	}
	out.Known = true
	return out, nil
}

// scanRows calls scan for every row and closes rows.
func scanRows(rows *sql.Rows, scan func(*sql.Rows) error) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err() //nolint:wrapcheck // wrapped by the caller
}
