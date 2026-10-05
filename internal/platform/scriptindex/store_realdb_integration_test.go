//go:build integration

package scriptindex

// Real-Postgres tests for the managed-script chunk index (#2027): the chunk
// table round-trips the raw digest the worker writes (a TEXT column could not
// hold one, #1365), and the gap and coverage queries read the hashes a save
// and the worker write.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// seedRow inserts one enabled script directly, so this package's real-DB proof
// depends on the schema rather than on the request-path store that writes it.
func seedRow(t *testing.T, store *Store, name string) string {
	t.Helper()
	var id string
	require.NoError(t, store.db.QueryRowContext(context.Background(), `
		INSERT INTO scripts (name, display_name, description, source_code, params, owner_email, tags, status)
		VALUES ($1, 'Daily Sales Report', 'Summarize yesterday''s sales by region',
		        '# churn counts a customer gone ninety days'||chr(10)||'print(1)',
		        '[{"name":"report_date","type":"date","required":true}]'::jsonb,
		        'jane@example.com', ARRAY['revenue'], 'active')
		RETURNING id`, name).Scan(&id))
	return id
}

// embed runs the worker's half for one script: load its items, write a
// vector per item, stamp the set.
func embed(t *testing.T, store *Store, id, model string) {
	t.Helper()
	ctx := context.Background()
	items, err := NewSource(store, 6000).LoadItems(ctx, id)
	require.NoError(t, err)
	rows := make([]indexjobs.Vector, 0, len(items))
	for _, it := range items {
		rows = append(rows, indexjobs.Vector{ItemID: it.ItemID, Embedding: make([]float32, 768), Model: model, TextHash: indexjobs.TextHash(it.Text)})
	}
	require.NoError(t, store.ReplaceVectors(ctx, id, rows))
	require.NoError(t, NewSink(store, model).StampExpected(ctx, indexjobs.Key{SourceKind: SourceKind, SourceID: id}, len(rows)))
}

func TestRealDB_ChunksRoundTripARawDigest(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()
	id := seedRow(t, store, "daily-sales")

	items, err := NewSource(store, 6000).LoadItems(ctx, id)
	require.NoError(t, err)
	require.Len(t, items, 2, "the card, then the source")
	assert.Contains(t, items[1].Text, "ninety days", "the source and its comments are indexed")

	hash := indexjobs.TextHash(items[1].Text)
	require.Len(t, hash, 32)
	require.NoError(t, store.UpsertVectors(ctx, id, []indexjobs.Vector{
		{ItemID: items[1].ItemID, Embedding: make([]float32, 768), Model: "test-model", TextHash: hash},
	}))
	got, err := store.ListVectors(ctx, id)
	require.NoError(t, err)
	require.Contains(t, got, id+":1")
	assert.Equal(t, hash, got[id+":1"].TextHash, "the digest round-trips byte for byte")
	assert.Equal(t, 768, got[id+":1"].Dim)

	// A replace with only the card prunes the source chunk.
	require.NoError(t, store.ReplaceVectors(ctx, id, []indexjobs.Vector{
		{ItemID: id + ":0", Embedding: make([]float32, 768), Model: "test-model", TextHash: hash},
	}))
	got, err = store.ListVectors(ctx, id)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Contains(t, got, id+":0")
}

// TestRealDB_GapsAndCoverageAgreeOnTheSameRows proves the two queries the
// queue and the admin surfaces read report the same population: a built
// script is neither a gap nor uncovered; a save that moves its text, or a
// model swap, makes it both again.
func TestRealDB_GapsAndCoverageAgreeOnTheSameRows(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()
	id := seedRow(t, store, "daily-sales")

	gaps, err := store.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, id, "a script never built is a gap the reconciler owes")
	indexed, expected, err := store.Coverage(ctx, "test-model")
	require.NoError(t, err)
	assert.Equal(t, 0, indexed)
	assert.Equal(t, 1, expected)

	embed(t, store, id, "test-model")
	gaps, err = store.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.NotContains(t, gaps, id)
	indexed, _, err = store.Coverage(ctx, "test-model")
	require.NoError(t, err)
	assert.Equal(t, 1, indexed)

	gaps, err = store.FindGaps(ctx, "a-different-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, id, "a model swap owes the script again")

	// A save records a new hash for what the script is indexed on.
	_, err = db.ExecContext(ctx, `UPDATE scripts SET index_text_hash = $2 WHERE id = $1`, id, indexjobs.TextHash("moved"))
	require.NoError(t, err)
	gaps, err = store.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, id, "text that moved since the build is owed")
	indexed, _, err = store.Coverage(ctx, "test-model")
	require.NoError(t, err)
	assert.Equal(t, 0, indexed)
}

// TestRealDB_DisabledScriptIsNothingToIndex proves the Source's clean-completion
// path against the real predicate: a disabled row yields no item rather than a
// failing job that retries forever, and is never counted as missing coverage.
func TestRealDB_DisabledScriptIsNothingToIndex(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	id := seedRow(t, store, "daily-sales")
	_, err := db.ExecContext(ctx, `UPDATE scripts SET enabled = false WHERE id = $1`, id)
	require.NoError(t, err)

	items, err := NewSource(store, 6000).LoadItems(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, items)

	_, expected, err := store.Coverage(ctx, "test-model")
	require.NoError(t, err)
	assert.Equal(t, 0, expected)
}
