//go:build integration

package scriptstore

// The real-schema proof for the request-path half of the scripts index
// (#1370, #2027). Two things here are invisible to sqlmock:
//
//   - the save's RETURNING clause decides whether a write owes the script an
//     embedding and enqueues an index job. sqlmock returns whatever the test tells
//     it to, so only Postgres can say whether the expression is the one that
//     answers.
//   - the hybrid arms need pgvector's `<=>` operator over the chunk table's
//     hnsw index, and the script_fts GIN index, in one UNION.

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptindex"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// embedScript runs the worker's half for one script, every chunk on the given
// vector: load its items, write them, stamp the set.
func embedScript(t *testing.T, idx *scriptindex.Store, id string, vec []float32) {
	t.Helper()
	ctx := context.Background()
	items, err := scriptindex.NewSource(idx, 6000).LoadItems(ctx, id)
	require.NoError(t, err)
	rows := make([]indexjobs.Vector, 0, len(items))
	for _, it := range items {
		rows = append(rows, indexjobs.Vector{ItemID: it.ItemID, Embedding: vec, Model: "test-model", TextHash: indexjobs.TextHash(it.Text)})
	}
	require.NoError(t, idx.ReplaceVectors(ctx, id, rows))
	require.NoError(t, scriptindex.NewSink(idx, "test-model").StampExpected(ctx,
		indexjobs.Key{SourceKind: scriptindex.SourceKind, SourceID: id}, len(rows)))
}

// TestRealDB_AnIndexedChangeOwesARebuild is the staleness property: a script
// whose source or card changed owes an embedding (#2027), its previous chunks
// kept so the worker re-embeds only the ones whose text moved; a write that
// changed neither owes nothing.
func TestRealDB_AnIndexedChangeOwesARebuild(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	sc := seedScript(t, s, "daily-sales", "jane@example.com", nil)
	embedScript(t, idx, sc.ID, unitVector(0.9))
	owed := func() bool {
		gaps, err := idx.FindGaps(ctx, "test-model")
		require.NoError(t, err)
		return slices.Contains(gaps, sc.ID)
	}
	require.False(t, owed())

	// An owner transfer is not part of the document.
	sc.OwnerEmail = "carol@example.com"
	require.NoError(t, s.Update(ctx, sc))
	assert.False(t, owed(), "a change to nothing indexed owes nothing")

	// The source is indexed, so editing it owes a rebuild, and the chunks
	// stay for the worker's reuse until it lands.
	sc.Source = "# churn is ninety days without an order\nprint(2)\n"
	require.NoError(t, s.Update(ctx, sc))
	assert.True(t, owed(), "an edited source owes an embedding")
	kept, err := idx.ListVectors(ctx, sc.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, kept, "the previous chunks are kept for reuse")

	embedScript(t, idx, sc.ID, unitVector(0.9))
	assert.False(t, owed(), "a rebuild of the current text converges")
	got, err := idx.ListVectors(ctx, sc.ID)
	require.NoError(t, err)
	assert.Len(t, got, 2, "the rebuilt set is the card and the one source chunk")

	sc.Description = "Summarize last quarter's refunds instead"
	require.NoError(t, s.Update(ctx, sc))
	assert.True(t, owed(), "the card is indexed too")
}

// TestRealDB_StatusChangeOwesARebuild covers the write nobody would guess
// moves the document: a lifecycle change rewrites the card's last line (the
// execution note reads the status), and that line is what tells a caller
// whether the hit is something to run.
func TestRealDB_StatusChangeOwesARebuild(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	sc := seedScript(t, s, "daily-sales", "jane@example.com", nil)
	embedScript(t, idx, sc.ID, unitVector(0.9))

	sc.Status = script.StatusDeprecated
	require.NoError(t, s.Update(ctx, sc))

	gaps, err := idx.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, sc.ID, "the deprecated card says nothing will run it; the built one says run_script will")

	indexed, err := idx.GetIndexed(ctx, sc.ID)
	require.NoError(t, err)
	assert.Contains(t, script.IndexText(indexed), "deprecated")
}

// TestRealDB_ALibraryConverges proves the worker hashes a library the way its
// save does: the execution note reads the library flag, so a projection
// without it would leave a library owing an embedding forever.
func TestRealDB_ALibraryConverges(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	lib := &script.Script{
		Name: "helpers", Description: "Shared helpers.", Source: "def double(x):\n    return x * 2\n",
		OwnerEmail: "jane@example.com", Enabled: true,
	}
	require.NoError(t, s.Create(ctx, lib, testAuthor))
	require.True(t, lib.Library, "a source with no main() is a library")
	embedScript(t, idx, lib.ID, unitVector(0.5))

	gaps, err := idx.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.NotContains(t, gaps, lib.ID)
}

// TestRealDB_HybridSearchRanksSemanticallyAndLexically runs both arms against
// the real indexes, for a caller who owns none of the scripts: a script's
// definition is everyone signed in's to read (#2027).
func TestRealDB_HybridSearchRanksSemanticallyAndLexically(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	// The script whose words nobody typed, but whose vector is near the query.
	near := seedScript(t, s, "near-neighbour", "jane@example.com", nil)
	near.Description = "Refresh the numbers finance looks at every morning"
	require.NoError(t, s.Update(ctx, near))
	embedScript(t, idx, near.ID, unitVector(1))

	// The script nothing has embedded yet, findable only by its words. It
	// proves the lexical arm still surfaces rows the vector arm cannot see,
	// which keeps a freshly written script findable while its job is queued.
	worded := seedScript(t, s, "kubernetes-ingress", "carol@example.com", nil)

	hybrid, err := s.Search(ctx, script.SearchQuery{Embedding: unitVector(1), QueryText: "revenue by region"})
	require.NoError(t, err)
	ids := scriptIDs(hybrid)
	assert.Contains(t, ids, near.ID, "a semantic match with no shared term must still rank")
	assert.Contains(t, ids, worded.ID, "an unembedded row must still reach the caller through the lexical arm")

	lexical, err := s.Search(ctx, script.SearchQuery{QueryText: "revenue by region"})
	require.NoError(t, err)
	assert.NotContains(t, scriptIDs(lexical), near.ID)
	assert.Contains(t, scriptIDs(lexical), worded.ID)
}

// TestRealDB_SearchFindsAPhraseOnlyInTheSource proves the lexical arm reads the
// source, comments included (#2027): a word that appears nowhere on the card
// finds the script.
func TestRealDB_SearchFindsAPhraseOnlyInTheSource(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := seedScript(t, s, "churn", "jane@example.com", nil)
	sc.Source = "# the quokkafence window counts a customer gone ninety days\nprint(1)\n"
	require.NoError(t, s.Update(ctx, sc))
	other := seedScript(t, s, "other", "carol@example.com", nil)
	other.Description = "quokkafence appears on this card"
	require.NoError(t, s.Update(ctx, other))

	got, err := s.Search(ctx, script.SearchQuery{QueryText: "quokkafence"})
	require.NoError(t, err)
	ids := scriptIDs(got)
	require.Contains(t, ids, sc.ID)
	require.Equal(t, other.ID, ids[0], "a card match outranks a source-only match")
}

// TestRealDB_CoverageCountsEveryEnabledScript proves the admin index-jobs
// surfaces count scripts with no per-kind special case.
func TestRealDB_CoverageCountsEveryEnabledScript(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	embedded := seedScript(t, s, "embedded", "jane@example.com", nil)
	seedScript(t, s, "pending", "jane@example.com", nil)
	off := seedScript(t, s, "disabled", "jane@example.com", nil)
	off.Enabled = false
	require.NoError(t, s.Update(ctx, off))

	embedScript(t, idx, embedded.ID, unitVector(1))

	indexed, expected, err := idx.Coverage(ctx, "test-model")
	require.NoError(t, err)
	assert.Equal(t, 1, indexed)
	assert.Equal(t, 2, expected, "a disabled script is never embedded and never counted as missing")
}

// embeddingDim is the vector width migration 000177 declares, matching every
// sibling embedding column.
const embeddingDim = 768

// unitVector returns a 768-wide vector whose first component is v, which is
// enough to make two vectors near or far without pretending to be an embedder.
func unitVector(v float32) []float32 {
	out := make([]float32, embeddingDim)
	out[0] = v
	return out
}

// scriptIDs projects a ranked result set onto the ids it contains.
func scriptIDs(scored []script.ScoredScript) []string {
	out := make([]string, 0, len(scored))
	for i := range scored {
		out = append(out, scored[i].Script.ID)
	}
	return out
}
