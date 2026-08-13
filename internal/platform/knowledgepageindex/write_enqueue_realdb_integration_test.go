//go:build integration

package knowledgepageindex_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/knowledgepageindex"
	"github.com/txn2/mcp-data-platform/internal/platform/portalstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// TestKnowledgePageWriteEnqueuesIndexJob_RealDB is the acceptance gate for
// #1256, against real Postgres and the real assembled write path: a page
// created through the portal store layer is returned by ranked search without a
// reconciler sweep.
//
// The measurement is the absence of the reconciler. Nothing in this test
// constructs one, and nothing enqueues on the page's behalf: the only producer
// of the job is the store's own write path, which the assertion on the job row's
// trigger ('write', not 'reconciler') pins. Before this change the same
// sequence returned no ranked hit until a sweep enqueued the page, up to one
// full ReconcilerInterval later.
func TestKnowledgePageWriteEnqueuesIndexJob_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	embedder := &bagOfWordsEmbedder{t: t}

	// The real portal store layer: it builds the page store and the write-path
	// producer together, exactly as the platform does at startup.
	layer := portalstore.New(db, nil, embedder, portalstore.Config{Name: "portal"})
	require.NotNil(t, layer)
	pages, ok := layer.KnowledgePageStore().(knowledgepage.StoreSearcher)
	require.True(t, ok, "the layer's page store must also rank pages")

	// The queue side: bind the layer's producers to the real job store, the step
	// indexqueue performs once the queue exists.
	jobs := indexjobs.NewPostgresStore(db)
	for _, p := range layer.IndexProducers() {
		p.Bind(jobs)
	}

	const needle = "quarantine hold released only by the duty archivist"
	page := knowledgepage.Page{
		ID: knowledgepage.NewID(), Slug: "ingest-runbook", Title: "Ingest Runbook",
		Body: "## Quirk\n\n" + needle + "\n", Tags: []string{"ops"},
		CreatedBy: "alice@example.com", CreatedEmail: "alice@example.com",
	}
	require.NoError(t, pages.Insert(ctx, page))

	// The write itself produced the job, before any worker or sweep ran.
	pending, err := jobs.List(ctx, indexjobs.ListFilter{
		SourceKind: knowledgepageindex.SourceKind, SourceID: page.ID,
	})
	require.NoError(t, err)
	require.Len(t, pending, 1, "the page write must enqueue exactly one index job")
	assert.Equal(t, indexjobs.TriggerWrite, pending[0].Trigger,
		"the job must come from the write path, not a reconciler sweep")
	assert.Equal(t, indexjobs.StatusPending, pending[0].Status)

	// Only the worker runs — no reconciler exists in this process.
	stopWorker := startWorker(ctx, t, db, embedder)
	defer stopWorker()
	awaitChunks(ctx, t, db, page.ID)

	hits, err := pages.SemanticSearch(ctx, vectorize(needle), 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits, "a page created this second must be reachable by ranked search")
	assert.Equal(t, page.ID, hits[0].Page.ID)

	ranked, err := pages.Search(ctx, knowledgepage.SearchQuery{
		Embedding: vectorize(needle), QueryText: needle, Limit: 10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, ranked)
	assert.Equal(t, page.ID, ranked[0].Page.ID)

	// An edit is the same contract: the new text is searchable without a sweep,
	// and the fact that is no longer on the page stops ranking it.
	const revised = "quarantine hold released only by the shift supervisor"
	newBody := "## Quirk\n\n" + revised + "\n"
	require.NoError(t, pages.Update(ctx, page.ID, knowledgepage.Update{
		Body: &newBody, UpdatedBy: "bob@example.com", ChangeSummary: "reassign",
	}))

	editJobs, err := jobs.List(ctx, indexjobs.ListFilter{
		SourceKind: knowledgepageindex.SourceKind, SourceID: page.ID,
		Trigger: indexjobs.TriggerWrite,
	})
	require.NoError(t, err)
	assert.Len(t, editJobs, 2, "the edit must enqueue its own job rather than wait for a sweep")

	awaitChunks(ctx, t, db, page.ID)
	editHits, err := pages.SemanticSearch(ctx, vectorize(revised), 10)
	require.NoError(t, err)
	require.NotEmpty(t, editHits)
	assert.Equal(t, page.ID, editHits[0].Page.ID,
		"the edited text must be semantically reachable without a reconciler sweep")
}

// startWorker runs the real indexjobs worker over the real knowledge-pages
// consumer, with no reaper and no reconciler, and returns its stop func. The
// worker is the only background component in the test, so every job it runs was
// enqueued by a write path.
func startWorker(ctx context.Context, t *testing.T, db *sql.DB, embedder embedding.Provider) func() {
	t.Helper()
	registry := indexjobs.NewRegistry()
	require.NoError(t, knowledgepageindex.RegisterConsumer(registry, db,
		embedding.ModelName(embedder), embedding.MaxInputBytes(embedder)))

	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store: indexjobs.NewPostgresStore(db), Registry: registry, Embedder: embedder,
		LeaseDuration: time.Minute, PollEvery: 50 * time.Millisecond,
	})
	worker.Start(ctx)
	return worker.Stop
}

// awaitChunks waits until the page's chunk set is current under the test's
// model, which is the convergence signal the gap query reads.
func awaitChunks(ctx context.Context, t *testing.T, db *sql.DB, pageID string) {
	t.Helper()
	store := knowledgepageindex.NewStore(db)
	deadline := time.Now().Add(30 * time.Second)
	for {
		gaps, err := store.FindGaps(ctx, "bag-of-words")
		require.NoError(t, err)
		if !contains(gaps, pageID) && chunkCount(ctx, t, db, pageID) > 0 {
			return
		}
		require.True(t, time.Now().Before(deadline),
			"the write-path job did not converge the page's chunk set")
		time.Sleep(50 * time.Millisecond)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
