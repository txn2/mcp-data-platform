//go:build integration

package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/indexqueue"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// zeroEmbedder satisfies embedding.Provider with fixed zero vectors.
// The source-gone path resolves before any embed call, so it is never
// actually invoked; it exists to satisfy the worker's config.
type zeroEmbedder struct{ dim int }

func (e *zeroEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, e.dim), nil
}

func (e *zeroEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dim)
	}
	return out, nil
}

func (e *zeroEmbedder) Dimension() int { return e.dim }
func (*zeroEmbedder) Kind() string     { return "zero" }

// sourceGoneSpec is a one-operation OpenAPI document so the seeded
// spec is valid until the test deletes it out from under the queue.
const sourceGoneSpec = `openapi: 3.0.0
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: a
      responses:
        "200":
          description: ok`

// TestIndexJobs_SourceGoneSelfHeals_RealDB proves the self-healing
// half of #998 through the real assembled queue (indexqueue.New with
// the real catalogSource, sink, worker, and Postgres store): a job
// orphaned by a spec delete that the handler-side cancel did not
// cover — the racing-write case — completes as source-gone instead of
// failing terminally, and its completion resolves the unit's earlier
// open failure. No operator dismiss, no permanent Degraded.
func TestIndexJobs_SourceGoneSelfHeals_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	catalogStore := apicatalog.NewPostgresStore(db)
	jobs := indexjobs.NewPostgresStore(db)
	key := indexjobs.Key{
		SourceKind: catalogindex.SourceKind,
		SourceID:   catalogindex.EncodeSourceID("cat1", "spec1"),
	}

	require.NoError(t, catalogStore.CreateCatalog(ctx, apicatalog.Catalog{ID: "cat1", Name: "cat1"}))
	require.NoError(t, catalogStore.UpsertSpec(ctx, "cat1", apicatalog.SpecEntry{
		SpecName: "spec1", Content: sourceGoneSpec, SourceKind: apicatalog.SourceInline, OperationCount: 1,
	}))

	// An earlier attempt failed while the spec still existed, leaving
	// an open failure for the unit.
	_, err := jobs.Enqueue(ctx, key, indexjobs.TriggerWrite)
	require.NoError(t, err)
	staleJob, err := jobs.Claim(ctx, "stale-worker")
	require.NoError(t, err)
	require.NoError(t, jobs.Fail(ctx, staleJob.ID, "stale-worker", "embed failed: provider down"))
	failures, err := jobs.ActiveFailures(ctx, catalogindex.SourceKind, 10)
	require.NoError(t, err)
	require.Len(t, failures, 1, "seeded open failure must be visible before the run")

	// A retry job is queued, and then the spec is deleted without the
	// handler-side cancel (the racing case the cancel cannot cover).
	_, err = jobs.Enqueue(ctx, key, indexjobs.TriggerWrite)
	require.NoError(t, err)
	require.NoError(t, catalogStore.DeleteSpec(ctx, "cat1", "spec1"))

	h := indexqueue.New(indexqueue.Config{
		DB:            db,
		Embedder:      &zeroEmbedder{dim: 4},
		ModelName:     "zero",
		LeaseDuration: time.Minute,
		CatalogStore:  catalogStore,
	})
	require.NotNil(t, h)
	require.NoError(t, h.Start(ctx))
	defer func() { _ = h.Stop(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, listErr := jobs.List(ctx, indexjobs.ListFilter{
			SourceKind: catalogindex.SourceKind,
			SourceID:   key.SourceID,
			Status:     indexjobs.StatusSucceeded,
		})
		require.NoError(t, listErr)
		if len(rows) == 1 {
			break
		}
		require.True(t, time.Now().Before(deadline),
			"orphaned job did not resolve as succeeded within the deadline")
		time.Sleep(50 * time.Millisecond)
	}

	failures, err = jobs.ActiveFailures(ctx, catalogindex.SourceKind, 10)
	require.NoError(t, err)
	require.Empty(t, failures, "the source-gone completion must resolve the unit's open failure")

	counts, err := jobs.Counts(ctx, catalogindex.SourceKind)
	require.NoError(t, err)
	require.Zero(t, counts.UnresolvedFailures, "api_catalog kind must report healthy")
}
