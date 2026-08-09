//go:build integration

package knowledgepageindex_test

import (
	"context"
	"database/sql"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// embedDim matches the vector(768) column migration 000097 declares.
const embedDim = 768

// budget is the input budget the fake provider reports and enforces. It is far
// below the production default so the fixture body stays small enough to read,
// while exercising exactly the code path a ~6 KB cap exercises in production.
const budget = 1200

// bagOfWordsEmbedder is a deterministic, content-sensitive embedding provider:
// it hashes each word into a dimension, so two texts that share vocabulary are
// close under cosine and two that do not are far apart. That is enough for a
// ranking assertion to mean something, unlike hand-fed vectors.
//
// It also enforces the contract this feature exists to establish: no text handed
// to the provider may exceed its input budget. In production, over-budget input
// is silently trimmed (pkg/embedding/ollama.go), which is precisely how a page's
// tail became invisible; here it fails the test.
type bagOfWordsEmbedder struct {
	t   *testing.T
	mu  sync.Mutex
	got []string
}

func (e *bagOfWordsEmbedder) record(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, text)
	assert.LessOrEqual(e.t, len(text), budget,
		"the provider was handed text over its input budget; production would trim it silently")
}

func (e *bagOfWordsEmbedder) texts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.got...)
}

func (e *bagOfWordsEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.record(text)
	return vectorize(text), nil
}

func (e *bagOfWordsEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		e.record(t)
		out[i] = vectorize(t)
	}
	return out, nil
}

func (*bagOfWordsEmbedder) Dimension() int     { return embedDim }
func (*bagOfWordsEmbedder) Kind() string       { return embedding.KindOllama }
func (*bagOfWordsEmbedder) Model() string      { return "bag-of-words" }
func (*bagOfWordsEmbedder) MaxInputBytes() int { return budget }

// vectorize maps text to a unit vector by accumulating one dimension per
// distinct word, so cosine similarity tracks vocabulary overlap.
func vectorize(text string) []float32 {
	v := make([]float32, embedDim)
	for _, word := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(strings.Trim(word, ".,#`")))
		v[h.Sum32()%embedDim]++
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return v
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

// filler is generic prose every fixture page shares, sized so the distinctive
// tail sits past the provider's input budget.
func filler(sections int) string {
	var b strings.Builder
	for i := 0; i < sections; i++ {
		b.WriteString("## Routine step\n\nfollow the standard pipeline procedure and record the outcome\n\n")
	}
	return b.String()
}

// TestKnowledgePageChunkedIndex_RealDB is the acceptance gate for #1242, run
// against real Postgres and the real indexjobs pipeline (source, sink, worker,
// job store): a fact that appears ONLY past the embedding provider's input
// budget is reachable through the semantic arm of search.
//
// The decoy page carries the same head text and none of the tail, so a design
// that embedded only the head — the pre-#1242 behavior — could not separate the
// two. The separation is the measurement.
func TestKnowledgePageChunkedIndex_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const needle = "quarantine hold released only by the duty archivist"
	head := filler(20)

	target := knowledgepage.Page{
		ID: knowledgepage.NewID(), Slug: "ingest-runbook", Title: "Ingest Runbook",
		Body: head + "## Quirk\n\n" + needle + "\n", Tags: []string{"ops"},
	}
	decoy := knowledgepage.Page{
		ID: knowledgepage.NewID(), Slug: "export-runbook", Title: "Export Runbook",
		Body: head, Tags: []string{"ops"},
	}
	pages := knowledgepage.NewPostgresStoreSearcher(db)
	require.NoError(t, pages.Insert(ctx, target))
	require.NoError(t, pages.Insert(ctx, decoy))
	require.Greater(t, len(knowledgepage.IndexText(target.Title, target.Body, target.Tags)), budget,
		"the fixture must exceed the provider budget or it proves nothing")

	embedder := &bagOfWordsEmbedder{t: t}
	runIndexPipeline(ctx, t, db, embedder)

	// Several chunks were embedded for a page that used to be one vector.
	chunks := chunkCount(ctx, t, db, target.ID)
	assert.Greater(t, chunks, 1, "an over-budget page must be stored as several chunks")
	assert.GreaterOrEqual(t, len(embedder.texts()), chunks)

	// The keystone claim: a purely semantic query for the tail fact finds the
	// page, and ranks it above the decoy that shares the entire head.
	hits, err := pages.SemanticSearch(ctx, vectorize(needle), 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, target.ID, hits[0].Page.ID,
		"a fact past the input budget must rank its page first")
	require.Len(t, hits, 2)
	assert.Greater(t, hits[0].Score, hits[1].Score,
		"the tail match must separate the page from a decoy sharing only the head")

	// Hybrid search returns it too, with no lexical overlap carrying the result:
	// the query text is deliberately absent from both pages.
	ranked, err := pages.Search(ctx, knowledgepage.SearchQuery{
		Embedding: vectorize(needle), QueryText: "zzznonmatchingtoken", Limit: 10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, ranked)
	assert.Equal(t, target.ID, ranked[0].Page.ID)
}

// TestKnowledgePageChunkedIndex_EditDropsStaleChunks_RealDB proves the write
// path and the index agree: editing a page's body in the request path removes
// every vector computed from the old text in the same transaction, and the
// reconciler's gap query then owes the page a fresh chunk set.
func TestKnowledgePageChunkedIndex_EditDropsStaleChunks_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	page := knowledgepage.Page{
		ID: knowledgepage.NewID(), Slug: "fiscal", Title: "Fiscal Calendar",
		Body: filler(20) + "## Quirk\n\nthe fiscal year opens in february\n", Tags: []string{"finance"},
	}
	pages := knowledgepage.NewPostgresStoreSearcher(db)
	require.NoError(t, pages.Insert(ctx, page))

	store := knowledgepageindex.NewStore(db)
	gaps, err := store.FindGaps(ctx, "bag-of-words")
	require.NoError(t, err)
	assert.Contains(t, gaps, page.ID, "a new page owes a chunk set")

	runIndexPipeline(ctx, t, db, &bagOfWordsEmbedder{t: t})
	require.Greater(t, chunkCount(ctx, t, db, page.ID), 1)

	gaps, err = store.FindGaps(ctx, "bag-of-words")
	require.NoError(t, err)
	assert.NotContains(t, gaps, page.ID, "a converged page is not re-enqueued every sweep")

	indexed, expected, err := store.Coverage(ctx, "bag-of-words")
	require.NoError(t, err)
	assert.Equal(t, expected, indexed)
	assert.Positive(t, expected)

	newBody := "the fiscal year opens in march"
	require.NoError(t, pages.Update(ctx, page.ID, knowledgepage.Update{
		Body: &newBody, UpdatedBy: "bob@example.com", ChangeSummary: "shift start",
	}))
	assert.Zero(t, chunkCount(ctx, t, db, page.ID),
		"a content edit must drop the vectors computed from the old text")

	gaps, err = store.FindGaps(ctx, "bag-of-words")
	require.NoError(t, err)
	assert.Contains(t, gaps, page.ID, "an edited page owes a fresh chunk set")

	// A model swap invalidates the whole corpus, which is what makes a provider
	// change self-heal instead of leaving vectors from two spaces side by side.
	gaps, err = store.FindGaps(ctx, "some-other-model")
	require.NoError(t, err)
	assert.Contains(t, gaps, page.ID)
}

// runIndexPipeline drains the real queue: it enqueues every gap the reconciler
// would find and runs the real worker until no page owes work.
func runIndexPipeline(ctx context.Context, t *testing.T, db *sql.DB, embedder embedding.Provider) {
	t.Helper()
	jobs := indexjobs.NewPostgresStore(db)
	registry := indexjobs.NewRegistry()
	require.NoError(t, knowledgepageindex.RegisterConsumer(registry, db,
		embedding.ModelName(embedder), embedding.MaxInputBytes(embedder)))

	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store: jobs, Registry: registry, Embedder: embedder,
		LeaseDuration: time.Minute, PollEvery: 50 * time.Millisecond,
	})
	worker.Start(ctx)
	defer worker.Stop()

	store := knowledgepageindex.NewStore(db)
	gaps, err := store.FindGaps(ctx, embedding.ModelName(embedder))
	require.NoError(t, err)
	for _, id := range gaps {
		_, err := jobs.Enqueue(ctx, indexjobs.Key{
			SourceKind: knowledgepageindex.SourceKind, SourceID: id,
		}, indexjobs.TriggerWrite)
		require.NoError(t, err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		remaining, err := store.FindGaps(ctx, embedding.ModelName(embedder))
		require.NoError(t, err)
		if len(remaining) == 0 {
			return
		}
		require.True(t, time.Now().Before(deadline),
			"index pipeline did not converge; %d page(s) still owe vectors", len(remaining))
		time.Sleep(50 * time.Millisecond)
	}
}

func chunkCount(ctx context.Context, t *testing.T, db *sql.DB, pageID string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_knowledge_page_embedding_chunks WHERE page_id = $1`, pageID).Scan(&n))
	return n
}
