package migrate

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq" // postgres driver for the real-database gate

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dropScriptInlineEmbeddingVersion is the migration under test: the one that
// drops what 000177 kept for replicas on the previous image (#2029).
const dropScriptInlineEmbeddingVersion = 178

// TestMigrationsAgainstRealPostgres_DropScriptInlineEmbedding proves 000178
// removes the inline embedding columns, their HNSW index and the two older
// script_fts overloads while leaving the seven-argument call and the index
// built on it, and that its down migration restores every one of them.
//
// The file sorts before migrate_realpg_test.go for the reason
// migrate_output_key_realpg_test.go records: the full-lifecycle test runs last
// and hands the shared template a schema at head.
func TestMigrationsAgainstRealPostgres_DropScriptInlineEmbedding(t *testing.T) {
	dsn := os.Getenv("MIGRATE_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATE_TEST_DSN not set; skipping real-Postgres migration gate (run via `make migrate-check`)")
	}
	migratorFactory = newMigrator

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open test database")
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(t.Context()), "ping test database")
	resetSchema(t, db)

	require.NoError(t, Steps(db, dropScriptInlineEmbeddingVersion-1), "migrate to the prior revision")
	assertScriptInlineEmbedding(t, db, true, "at 000177")

	require.NoError(t, Steps(db, 1), "apply the drop")
	assertScriptInlineEmbedding(t, db, false, "after 000178")
	assert.Equal(t, 1, scriptFTSOverloads(t, db, 7), "the seven-argument script_fts search calls stays")
	assert.True(t, indexExists(t, db, "idx_scripts_search_fts"), "the lexical index built on the seven-argument call stays")

	require.NoError(t, Steps(db, -1), "roll the drop back")
	assertScriptInlineEmbedding(t, db, true, "after rolling 000178 back")

	// 000177's own down rebuilds its index on the six-argument overload, so it
	// has to run cleanly on what this down restored.
	require.NoError(t, Steps(db, -1), "roll 000177 back on the restored schema")
}

// assertScriptInlineEmbedding checks the presence (or absence) of everything
// 000178 drops.
func assertScriptInlineEmbedding(t *testing.T, db *sql.DB, present bool, when string) {
	t.Helper()
	for _, col := range []string{"embedding", "embedding_model", "embedding_text_hash"} {
		assert.Equal(t, present, scriptsColumnExists(t, db, col), "scripts.%s present=%v %s", col, present, when)
	}
	assert.Equal(t, present, indexExists(t, db, "idx_scripts_embedding_hnsw"), "idx_scripts_embedding_hnsw present=%v %s", present, when)
	want := 0
	if present {
		want = 1
	}
	assert.Equal(t, want, scriptFTSOverloads(t, db, 5), "five-argument script_fts %s", when)
	assert.Equal(t, want, scriptFTSOverloads(t, db, 6), "six-argument script_fts %s", when)
}

func scriptsColumnExists(t *testing.T, db *sql.DB, column string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'scripts' AND column_name = $1`, column).Scan(&n))
	return n == 1
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, name).Scan(&n))
	return n == 1
}

func scriptFTSOverloads(t *testing.T, db *sql.DB, args int) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(t.Context(), `
		SELECT count(*) FROM pg_proc p JOIN pg_namespace ns ON ns.oid = p.pronamespace
		WHERE ns.nspname = current_schema() AND p.proname = 'script_fts' AND p.pronargs = $1`, args).Scan(&n))
	return n
}
