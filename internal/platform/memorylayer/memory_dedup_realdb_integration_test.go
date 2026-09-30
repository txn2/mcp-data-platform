//go:build integration

package memorylayer

// Real-Postgres acceptance test for #762 and #1987: rapid restatements of the
// same fact consolidate to a single active record, the newest, and the capture
// that stores each one never waits on the embedder. It wires the real pipeline
// the platform assembles -- the memory toolkit and its recallChecker over the
// postgres store with pgvector, the memory write-path producer bound to the
// real job store, and the real indexjobs worker running the memory consumer
// whose sink runs the recall check once it writes a vector. Captures are
// stored first and embedded afterwards in whatever order the worker claims
// them, which is why the rule is "the newer record wins" rather than "the
// capture being made wins". Run under `make test-realdb`.

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/memoryindex"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
)

func TestRealDB_MemoryCaptureDedup_ConsolidatesRestatements(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	store := memory.NewPostgresStore(db)
	vec := make([]float32, 768)
	vec[0] = 1
	embedder := &countingVecEmbedder{vec: vec}

	tk, err := memorykit.New("memory", store, embedder)
	require.NoError(t, err)
	tk.SetRecallChecker(&recallChecker{store: store})
	jobs := indexjobs.NewPostgresStore(db)
	producer := indexjobs.NewProducer(memoryindex.SourceKind)
	producer.Bind(jobs)
	tk.SetIndexNotifier(producer)

	const owner = "dedup@example.com"
	capture := func(content string) string {
		res, err := tk.AutoCapture(ctx, memorykit.AutoCaptureInput{
			SinkClass: memory.SinkBusinessKnowledge,
			Content:   content,
			CreatedBy: owner,
		})
		require.NoError(t, err)
		return res.ID
	}

	// Three restatements are stored before any is embedded: the captures made
	// no embed call, and each queued its own write-path job.
	r1 := capture("The orders feed refreshes nightly at 2am UTC.")
	r2 := capture("The orders feed is refreshed each night at 2am UTC.")
	r3 := capture("Orders data refreshes nightly at 2am UTC.")
	assert.Zero(t, embedder.calls.Load(), "a capture must not wait on the embedder (#1987)")
	for _, id := range []string{r1, r2, r3} {
		queued, err := jobs.List(ctx, indexjobs.ListFilter{SourceKind: memoryindex.SourceKind, SourceID: id})
		require.NoError(t, err)
		require.Len(t, queued, 1)
		assert.Equal(t, indexjobs.TriggerWrite, queued[0].Trigger)
	}

	stop := startMemoryWorker(ctx, t, db, embedder, tk.SettleRecall)
	defer stop()
	awaitRecallDone(ctx, t, store, r1, r2, r3)

	active := activeRecords(ctx, t, store, owner)
	require.Len(t, active, 1, "rapid restatements must consolidate to a single active record (#762)")
	assert.Equal(t, r3, active[0].ID, "the newest restatement wins, whichever was embedded first")

	// Stale records must remain supersedable: a restatement is exactly how a
	// stale record gets corrected (superseded rows are the only recall
	// exclusion beyond archived).
	require.NoError(t, store.MarkStale(ctx, []string{r3}, "schema drift"))
	r4 := capture("Orders data is refreshed nightly at 2am UTC.")
	awaitRecallDone(ctx, t, store, r4)
	superseded, err := store.Get(ctx, r3)
	require.NoError(t, err)
	assert.Equal(t, memory.StatusSuperseded, superseded.Status, "a capture restating a stale record must supersede it")
	active = activeRecords(ctx, t, store, owner)
	require.Len(t, active, 1)
	assert.Equal(t, r4, active[0].ID)
}

// countingVecEmbedder returns one fixed vector for every text, so every
// capture embeds identically (cosine 1.0), and counts its calls.
type countingVecEmbedder struct {
	vec   []float32
	calls atomic.Int64
}

func (f *countingVecEmbedder) Embed(context.Context, string) ([]float32, error) {
	f.calls.Add(1)
	return f.vec, nil
}

func (f *countingVecEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	f.calls.Add(1)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}
func (f *countingVecEmbedder) Dimension() int { return len(f.vec) }
func (*countingVecEmbedder) Kind() string     { return "test" }

// startMemoryWorker runs the real indexjobs worker over the real memory
// consumer, with the sink's post-write hook, and no reconciler: every job it
// runs was enqueued by a capture.
func startMemoryWorker(ctx context.Context, t *testing.T, db *sql.DB, embedder embedding.Provider, onEmbedded memoryindex.Embedded) func() {
	t.Helper()
	registry := indexjobs.NewRegistry()
	memStore := memoryindex.NewStore(db)
	require.NoError(t, registry.Register(
		memoryindex.NewSource(memStore),
		memoryindex.NewSink(memStore, embedding.ModelName(embedder), onEmbedded),
	))
	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store: indexjobs.NewPostgresStore(db), Registry: registry, Embedder: embedder,
		LeaseDuration: time.Minute, PollEvery: 50 * time.Millisecond, Concurrency: 2,
	})
	worker.Start(ctx)
	return worker.Stop
}

// awaitRecallDone waits until each record's recall check has run, which is
// the convergence signal: the worker wrote the vector and the sink's hook ran.
func awaitRecallDone(ctx context.Context, t *testing.T, store memory.Store, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for _, id := range ids {
		for {
			rec, err := store.Get(ctx, id)
			require.NoError(t, err)
			if rec.Metadata[memory.MetaKeyRecallCheck] == memory.RecallCheckDone {
				break
			}
			require.True(t, time.Now().Before(deadline), "recall check for %s never ran", id)
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func activeRecords(ctx context.Context, t *testing.T, store memory.Store, owner string) []memory.Record {
	t.Helper()
	active, _, err := store.List(ctx, memory.Filter{CreatedBy: owner, Status: memory.StatusActive, Limit: 50})
	require.NoError(t, err)
	return active
}
