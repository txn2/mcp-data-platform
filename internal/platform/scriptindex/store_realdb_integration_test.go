//go:build integration

package scriptindex

// Real-Postgres round-trip test for the managed-script embedding write path.
//
// indexjobs.TextHash returns a raw SHA-256, and raw digest bytes are not a
// UTF-8 string: PostgreSQL rejects a NUL outright and rejects any other invalid
// sequence on the encoding check. 000107 declared call_records.embedding_text_hash
// TEXT, so that kind's UpsertVectors failed on every write and it never indexed
// a single record (#1365). Migration 000113 declares this column BYTEA for that
// reason; sqlmock cannot see the difference, so the write runs here against the
// real column type.

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
		        'print(1)', '[{"name":"report_date","type":"date","required":true}]'::jsonb,
		        'jane@example.com', ARRAY['revenue'], 'active')
		RETURNING id`, name).Scan(&id))
	return id
}

func TestRealDB_UpsertVectorsRoundTripsARawDigest(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	id := seedRow(t, store, "daily-sales")

	text, err := store.GetIndexText(ctx, id)
	require.NoError(t, err)
	assert.Contains(t, text, "Daily Sales Report")
	assert.Contains(t, text, "parameters: report_date (required)")
	assert.Contains(t, text, "revenue")
	assert.NotContains(t, text, "print(1)", "the source is never part of the indexed document")

	// The hash the worker actually writes: 32 raw octets, which is what a TEXT
	// column can never hold.
	hash := indexjobs.TextHash(text)
	require.Len(t, hash, 32)

	require.NoError(t, store.UpsertVectors(ctx, id, []indexjobs.Vector{
		{ItemID: id, Embedding: make([]float32, 768), Model: "test-model", TextHash: hash},
	}))

	got, err := store.ListVectors(ctx, id)
	require.NoError(t, err)
	require.Contains(t, got, id)
	assert.Equal(t, hash, got[id].TextHash, "the digest round-trips byte for byte")
	assert.Equal(t, "test-model", got[id].Model)
	assert.Equal(t, 768, got[id].Dim)
}

// TestRealDB_GapsAndCoverageAgreeOnTheSameRows proves the two queries the queue
// and the admin surfaces read report the same population: an embedded row is
// neither a gap nor missing coverage, and a model swap makes it both again.
func TestRealDB_GapsAndCoverageAgreeOnTheSameRows(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	id := seedRow(t, store, "daily-sales")

	gaps, err := store.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, id, "an unembedded script is a gap the reconciler owes")

	indexed, expected, err := store.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, indexed)
	assert.Equal(t, 1, expected)

	require.NoError(t, store.UpsertVectors(ctx, id, []indexjobs.Vector{
		{ItemID: id, Embedding: make([]float32, 768), Model: "test-model",
			TextHash: indexjobs.TextHash("whatever the worker embedded")},
	}))

	gaps, err = store.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.NotContains(t, gaps, id)

	indexed, _, err = store.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, indexed)

	// A provider model swap invalidates the corpus, which is the other half of
	// what makes gap detection condition-based rather than count-based.
	gaps, err = store.FindGaps(ctx, "a-different-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, id)
}

// TestRealDB_DisabledScriptIsNothingToIndex proves the Source's clean-completion
// path against the real predicate: a disabled row yields no item rather than a
// failing job that retries forever.
func TestRealDB_DisabledScriptIsNothingToIndex(t *testing.T) {
	db := testdb.New(t)
	store := NewStore(db)
	ctx := context.Background()

	id := seedRow(t, store, "daily-sales")
	_, err := db.ExecContext(ctx, `UPDATE scripts SET enabled = false WHERE id = $1`, id)
	require.NoError(t, err)

	items, err := NewSource(store).LoadItems(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, items)

	_, expected, err := store.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, expected, "a disabled script is never counted as missing coverage")
}
