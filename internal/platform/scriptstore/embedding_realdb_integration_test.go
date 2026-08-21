//go:build integration

package scriptstore

// The real-schema proof for #1370's request-path half. Two things here are
// invisible to sqlmock, and each of them has a precedent for shipping broken
// (the embedding write itself is proved in internal/platform/scriptindex):
//
//   - the invalidation CASE and its RETURNING clause decide whether a write
//     enqueues an index job. sqlmock returns whatever the test tells it to, so
//     only Postgres can say whether the expression is the one that answers.
//   - the hybrid arms need pgvector's `<=>` operator, the hnsw index, and the
//     script_fts GIN index in one UNION.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptindex"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestRealDB_IndexedTextChangeClearsTheVector is the staleness property: a
// re-described script must not keep ranking on a vector built from the words it
// no longer carries, and a write that left the card alone must not throw away a
// vector for nothing.
func TestRealDB_IndexedTextChangeClearsTheVector(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	sc := seedScript(t, s, "daily-sales", script.ScopeGlobal, "jane@example.com", nil, nil)
	require.NoError(t, idx.UpsertVectors(ctx, sc.ID, []indexjobs.Vector{{
		ItemID: sc.ID, Embedding: unitVector(0.9), Model: "test-model",
		TextHash: indexjobs.TextHash(script.IndexText(sc)),
	}}))

	// A source edit is not part of the document, so the vector must survive it.
	sc.Source = "print(2)\n"
	require.NoError(t, s.Update(ctx, sc))
	kept, err := idx.ListVectors(ctx, sc.ID)
	require.NoError(t, err)
	assert.Contains(t, kept, sc.ID, "the source is not indexed, so editing it must not re-embed the corpus")

	// The description is the document, so changing it must drop the vector.
	sc.Description = "Summarize last quarter's refunds instead"
	require.NoError(t, s.Update(ctx, sc))
	cleared, err := idx.ListVectors(ctx, sc.ID)
	require.NoError(t, err)
	assert.Empty(t, cleared, "a stale vector would rank the script on a description it no longer has")

	// Cleared means the reconciler owes it a job, which is what closes the loop.
	gaps, err := idx.FindGaps(ctx, "test-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, sc.ID)
}

// TestRealDB_StatusChangeClearsTheVector covers the write nobody would guess
// moves the document: a lifecycle change rewrites the card's last line (the
// execution note reads the status), and that line is what tells a caller
// whether the hit is something to run.
func TestRealDB_StatusChangeClearsTheVector(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	sc := seedScript(t, s, "daily-sales", script.ScopeGlobal, "jane@example.com", nil, nil)
	require.NoError(t, idx.UpsertVectors(ctx, sc.ID, []indexjobs.Vector{{
		ItemID: sc.ID, Embedding: unitVector(0.9), Model: "test-model",
		TextHash: indexjobs.TextHash(script.IndexText(sc)),
	}}))

	sc.Status = script.StatusDeprecated
	require.NoError(t, s.Update(ctx, sc))

	got, err := idx.ListVectors(ctx, sc.ID)
	require.NoError(t, err)
	assert.Empty(t, got, "the deprecated card says nothing will run it; the old vector says run_script will")

	// And the text the next embed is built from now says so.
	text, err := idx.GetIndexText(ctx, sc.ID)
	require.NoError(t, err)
	assert.Contains(t, text, "deprecated")
}

// TestRealDB_HybridSearchRanksSemanticallyAndLexically runs both arms against
// the real indexes: the vector arm needs pgvector's cosine operator and the
// lexical arm needs the script_fts GIN expression, in one UNION.
func TestRealDB_HybridSearchRanksSemanticallyAndLexically(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	// The script whose words nobody typed, but whose vector is near the query.
	near := seedScript(t, s, "near-neighbour", script.ScopeGlobal, "jane@example.com", nil, nil)
	near.Description = "Refresh the numbers finance looks at every morning"
	require.NoError(t, s.Update(ctx, near))
	require.NoError(t, idx.UpsertVectors(ctx, near.ID, []indexjobs.Vector{{
		ItemID: near.ID, Embedding: unitVector(1), Model: "test-model",
		TextHash: indexjobs.TextHash("whatever the worker embedded"),
	}}))

	// The script nothing has embedded yet, findable only by its words. It proves
	// the lexical arm still surfaces rows the vector arm cannot see, which is
	// what keeps a freshly written script findable while its job is queued.
	worded := seedScript(t, s, "kubernetes-ingress", script.ScopeGlobal, "jane@example.com", nil, nil)

	hybrid, err := s.Search(ctx, script.SearchQuery{
		Embedding: unitVector(1), QueryText: "revenue by region",
	})
	require.NoError(t, err)
	ids := scriptIDs(hybrid)
	assert.Contains(t, ids, near.ID, "a semantic match with no shared term must still rank")
	assert.Contains(t, ids, worded.ID, "an unembedded row must still reach the caller through the lexical arm")

	// The same query with no vector is the lexical-only path a deployment with
	// no embedding provider gets: the semantic-only script drops out, and
	// nothing else changes.
	lexical, err := s.Search(ctx, script.SearchQuery{QueryText: "revenue by region"})
	require.NoError(t, err)
	assert.NotContains(t, scriptIDs(lexical), near.ID)
	assert.Contains(t, scriptIDs(lexical), worded.ID)
}

// TestRealDB_HybridSearchAppliesTheSameVisibility proves the scope predicate is
// on BOTH arms: a vector arm without it would return another owner's personal
// script to anyone whose query happened to sit near it.
func TestRealDB_HybridSearchAppliesTheSameVisibility(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	theirs := seedScript(t, s, "their-private", script.ScopePersonal, "carol@example.com", nil, nil)
	require.NoError(t, idx.UpsertVectors(ctx, theirs.ID, []indexjobs.Vector{{
		ItemID: theirs.ID, Embedding: unitVector(1), Model: "test-model",
		TextHash: indexjobs.TextHash(script.IndexText(theirs)),
	}}))

	got, err := s.Search(ctx, script.SearchQuery{
		Embedding: unitVector(1), QueryText: "revenue by region",
		OwnerEmail: "jane@example.com",
	})
	require.NoError(t, err)
	assert.NotContains(t, scriptIDs(got), theirs.ID,
		"a script the caller cannot see must cost neither a row nor a decision, on either arm")

	mine, err := s.Search(ctx, script.SearchQuery{
		Embedding: unitVector(1), QueryText: "revenue by region",
		OwnerEmail: "carol@example.com",
	})
	require.NoError(t, err)
	assert.Contains(t, scriptIDs(mine), theirs.ID)
}

// TestRealDB_CoverageCountsEveryEnabledScript proves the admin index-jobs
// surfaces count scripts with no per-kind special case: every enabled script is
// expected to carry exactly one vector.
func TestRealDB_CoverageCountsEveryEnabledScript(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()
	idx := scriptindex.NewStore(db)

	embedded := seedScript(t, s, "embedded", script.ScopeGlobal, "jane@example.com", nil, nil)
	seedScript(t, s, "pending", script.ScopeGlobal, "jane@example.com", nil, nil)
	off := seedScript(t, s, "disabled", script.ScopeGlobal, "jane@example.com", nil, nil)
	off.Enabled = false
	require.NoError(t, s.Update(ctx, off))

	require.NoError(t, idx.UpsertVectors(ctx, embedded.ID, []indexjobs.Vector{{
		ItemID: embedded.ID, Embedding: unitVector(1), Model: "test-model",
		TextHash: indexjobs.TextHash(script.IndexText(embedded)),
	}}))

	indexed, expected, err := idx.Coverage(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, indexed)
	assert.Equal(t, 2, expected, "a disabled script is never embedded and never counted as missing")
}

// embeddingDim is the vector width migration 000113 declares, matching every
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
