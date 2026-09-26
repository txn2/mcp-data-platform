//go:build integration

package retention

// Real-Postgres tests for the retention sweeps (#1904). Each inserts a row the
// sweep should remove and one it should keep, runs the sweep through the loop
// (so the advisory lock is taken on the real server), and asserts which is
// left.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), q, args...)
	require.NoError(t, err)
}

func ids(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		out = append(out, id)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestMemoryArchived_RealDB_RemovesOnlyOldArchivedRecords(t *testing.T) {
	db := testdb.New(t)
	old := time.Now().UTC().AddDate(0, 0, -100)
	mustExec(t, db, `INSERT INTO memory_records (id, content, status, updated_at) VALUES
		('archived_old', 'x', 'archived', $1),
		('archived_recent', 'x', 'archived', NOW()),
		('active_old', 'x', 'active', $1)`, old)

	New(db, time.Hour, MemoryArchived(db, 90)...).RunOnce(context.Background())

	assert.ElementsMatch(t, []string{"archived_recent", "active_old"}, ids(t, db, `SELECT id FROM memory_records`))
}

func TestOrphanedProducers_RealDB_RemovesOnlyOldRowsWhoseFileIsGone(t *testing.T) {
	db := testdb.New(t)
	old := time.Now().UTC().AddDate(0, 0, -100)
	mustExec(t, db, `INSERT INTO resources (id, scope, scope_id, path, filename, display_name, description, mime_type, size_bytes, s3_key, uri, uploader_sub, uploader_email)
		VALUES ('res_live', 'global', NULL, 'p', 'f.csv', 'f', '', 'text/csv', 1, 'k', 'mcp://global/p/f.csv', 'u', 'u@example.com')`)
	mustExec(t, db, `INSERT INTO content_producers (target_kind, target_id, producer_kind, producer_id, last_write_at) VALUES
		('resource', 'res_gone', 'person', 'old_orphan', $1),
		('resource', 'res_gone', 'person', 'recent_orphan', NOW()),
		('resource', 'res_live', 'person', 'old_live', $1),
		('asset', 'asset_gone', 'person', 'old_asset_orphan', $1)`, old)

	New(db, time.Hour, OrphanedProducers(db, 90)...).RunOnce(context.Background())

	assert.ElementsMatch(t, []string{"recent_orphan", "old_live"}, ids(t, db, `SELECT producer_id FROM content_producers`))
}

func TestSupersededGraphQL_RealDB_RemovesOnlyAnotherSchemasVectors(t *testing.T) {
	db := testdb.New(t)
	mustExec(t, db, `INSERT INTO graphql_connection_schemas (connection, schema_hash, sdl_gzip) VALUES ('gh', 'current', '\x00')`)
	vec := "[" + zeros(768) + "]"
	mustExec(t, db, `INSERT INTO graphql_operation_embeddings (connection, schema_hash, operation_id, text_hash, embedding, dim) VALUES
		('gh', 'current', 'op_current', '\x01', $1, 768),
		('gh', 'superseded', 'op_superseded', '\x01', $1, 768),
		('no_schema_row', 'any', 'op_untouched', '\x01', $1, 768)`, vec)

	New(db, time.Hour, SupersededGraphQL(db)...).RunOnce(context.Background())

	assert.ElementsMatch(t, []string{"op_current", "op_untouched"}, ids(t, db, `SELECT operation_id FROM graphql_operation_embeddings`))
}

// A sweep another replica is running is skipped rather than run twice.
func TestLoop_RealDB_ASweepAnotherReplicaHoldsIsSkipped(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	holder, err := db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Close() }()
	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockMemoryArchived)
	require.NoError(t, err)

	old := time.Now().UTC().AddDate(0, 0, -100)
	mustExec(t, db, `INSERT INTO memory_records (id, content, status, updated_at) VALUES ('archived_old', 'x', 'archived', $1)`, old)
	New(db, time.Hour, MemoryArchived(db, 90)...).RunOnce(ctx)
	assert.Equal(t, []string{"archived_old"}, ids(t, db, `SELECT id FROM memory_records`), "the holder's replica runs it")

	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockMemoryArchived)
	require.NoError(t, err)
	New(db, time.Hour, MemoryArchived(db, 90)...).RunOnce(ctx)
	assert.Empty(t, ids(t, db, `SELECT id FROM memory_records`))
}

func zeros(n int) string {
	b := make([]byte, 0, 2*n)
	for i := range n {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '0')
	}
	return string(b)
}
