//go:build integration

package platform_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/internal/testollama"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/embedjobs"
)

// TestEmbedJobs_RealOllama_BatchedPathCompletes drives the production
// embedding path end-to-end against a real CPU-only Ollama running in
// testcontainers. The test exists specifically because the batched
// /api/embed timeout regression in v1.64.0 (#445) shipped undetected:
// every prior integration test used synthetic delays in stub Computers,
// none exercised actual Ollama batch latency.
//
// Acceptance: a spec with enough operations to trigger the batched
// path (one POST per chunk of up to 32 texts) completes within the
// platform's default Ollama timeout. The pre-#445 default of 30s would
// fail this test on a typical CI runner because a 30-text batch on
// CPU-only Ollama can take 60+ seconds; the post-fix default of 5m
// covers it with margin.
func TestEmbedJobs_RealOllama_BatchedPathCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()

	ollama := testollama.Get(t)

	// pgvector container for the catalog + embedding-jobs tables.
	pgContainer, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err)
	defer func() { _ = pgContainer.Terminate(ctx) }()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	require.NoError(t, migrate.Run(db))

	// Seed a catalog with one spec that has 30 operations. Forces the
	// batched path (default batch size is 32). A 30-text batch is the
	// exact shape that timed out in #445.
	const (
		catalogID = "real-ollama-test"
		specName  = "wide"
		ops       = 30
	)
	_, err = db.ExecContext(ctx, `
		INSERT INTO api_catalogs (id, name, display_name, version, description, created_at, updated_at)
		VALUES ($1, $1, $1, 'v1', '', NOW(), NOW())
	`, catalogID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO api_catalog_specs (catalog_id, spec_name, source_kind, content, operation_count, created_at, updated_at)
		VALUES ($1, $2, 'inline', $3, $4, NOW(), NOW())
	`, catalogID, specName, syntheticSpec(ops), ops)
	require.NoError(t, err)

	// Wire the platform's real catalog store + embed-jobs against the
	// real Ollama provider. The worker constructs a dedicated provider
	// with apigateway.embed_jobs.embed_timeout (default 5m); the test
	// mirrors that here so the test exercises the same effective HTTP
	// timeout the production worker would see. Request-path consumers
	// still get the shorter embedding.DefaultTimeout (30s) via the
	// shared Provider, which this test deliberately does not exercise.
	prov := ollama.Provider(5 * time.Minute)
	catStore := apigatewaycatalog.NewPostgresStore(db)
	jobStore := embedjobs.NewPostgresStore(db)

	worker := embedjobs.NewWorker(embedjobs.WorkerConfig{
		Store:     jobStore,
		Resolver:  &storeSpecResolver{store: catStore},
		Computer:  &realComputer{embedder: prov},
		Persister: &storeEmbedPersister{store: catStore},
		WorkerID:  "real-ollama-test-worker",
	})
	worker.Start(ctx)
	defer worker.Stop()

	_, err = jobStore.Enqueue(ctx, embedjobs.SpecKey{CatalogID: catalogID, SpecName: specName}, embedjobs.KindSpecWrite)
	require.NoError(t, err)

	// Poll for completion. 10-minute outer deadline gives generous
	// margin over the 5-minute HTTP timeout the worker uses so a slow
	// CI runner whose batched POST takes near the timeout limit still
	// has time to observe success without false-negative-timing-out.
	deadline := time.Now().Add(10 * time.Minute)
	var final embedjobs.SpecStatusRow
	for time.Now().Before(deadline) {
		rows, qerr := jobStore.SpecStatuses(ctx, catalogID)
		require.NoError(t, qerr)
		require.Len(t, rows, 1)
		final = rows[0]
		if final.JobStatus == embedjobs.StatusSucceeded {
			break
		}
		if final.JobStatus == embedjobs.StatusFailed {
			t.Fatalf("embed job failed: attempts=%d last_error=%q", final.JobAttempts, final.JobLastError)
		}
		time.Sleep(3 * time.Second)
	}

	require.Equalf(t, embedjobs.StatusSucceeded, final.JobStatus,
		"job did not succeed within deadline: status=%s attempts=%d last_error=%q",
		final.JobStatus, final.JobAttempts, final.JobLastError)
	require.Equal(t, ops, final.EmbeddingCount,
		"expected %d vectors persisted, got %d", ops, final.EmbeddingCount)
}

// storeSpecResolver / storeEmbedPersister / realComputer are
// test-package-local adapters that bridge the embedjobs Worker to the
// real apigatewaycatalog.Store + apigateway.ComputeOperationEmbeddings.
// The production wiring at pkg/platform/apigateway_embed_jobs.go has
// equivalent unexported types; we redeclare here because this test
// runs in package platform_test.

type storeSpecResolver struct {
	store apigatewaycatalog.Store
}

func (r *storeSpecResolver) GetSpecContent(ctx context.Context, catalogID, specName string) (string, error) {
	spec, err := r.store.GetSpec(ctx, catalogID, specName)
	if err != nil {
		return "", fmt.Errorf("storeSpecResolver: %w", err)
	}
	return spec.Content, nil
}

type storeEmbedPersister struct {
	store apigatewaycatalog.Store
}

func (p *storeEmbedPersister) ListExisting(ctx context.Context, catalogID, specName string) (map[string]embedjobs.ExistingEmbedding, error) {
	rows, err := p.store.ListOperationEmbeddings(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("storeEmbedPersister: %w", err)
	}
	out := make(map[string]embedjobs.ExistingEmbedding, len(rows))
	for _, r := range rows {
		out[r.OperationID] = embedjobs.ExistingEmbedding{
			OperationID: r.OperationID, TextHash: r.TextHash, Embedding: r.Embedding,
			Model: r.Model, Dim: r.Dim,
		}
	}
	return out, nil
}

func (p *storeEmbedPersister) Upsert(ctx context.Context, catalogID, specName string, rows []embedjobs.ComputedEmbedding) error {
	catRows := make([]apigatewaycatalog.OperationEmbedding, len(rows))
	for i, r := range rows {
		catRows[i] = apigatewaycatalog.OperationEmbedding{
			OperationID: r.OperationID, TextHash: r.TextHash, Embedding: r.Embedding,
			Model: r.Model, Dim: r.Dim,
		}
	}
	if err := p.store.UpsertOperationEmbeddings(ctx, catalogID, specName, catRows); err != nil {
		return fmt.Errorf("storeEmbedPersister upsert: %w", err)
	}
	return nil
}

func (p *storeEmbedPersister) StampOperationCount(ctx context.Context, catalogID, specName string, count int) error {
	if err := p.store.SetOperationCount(ctx, catalogID, specName, count); err != nil {
		return fmt.Errorf("storeEmbedPersister stamp: %w", err)
	}
	return nil
}

type realComputer struct {
	embedder embedding.Provider
}

func (c *realComputer) Compute(ctx context.Context, content, specName string, existing map[string]embedjobs.ExistingEmbedding, progress func(int)) ([]embedjobs.ComputedEmbedding, error) {
	catalogExisting := make(map[string]apigatewaycatalog.OperationEmbedding, len(existing))
	for k, v := range existing {
		catalogExisting[k] = apigatewaycatalog.OperationEmbedding{
			OperationID: v.OperationID, TextHash: v.TextHash, Embedding: v.Embedding,
			Model: v.Model, Dim: v.Dim,
		}
	}
	rows, err := apigateway.ComputeOperationEmbeddings(ctx, c.embedder, content, specName, catalogExisting, progress)
	if err != nil {
		return nil, fmt.Errorf("realComputer: %w", err)
	}
	out := make([]embedjobs.ComputedEmbedding, len(rows))
	for i, r := range rows {
		out[i] = embedjobs.ComputedEmbedding{
			OperationID: r.OperationID, TextHash: r.TextHash, Embedding: r.Embedding,
			Model: r.Model, Dim: r.Dim,
		}
	}
	return out, nil
}

// syntheticSpec builds an OpenAPI document with n distinct operations.
// Operation IDs and summaries vary so the embedding text hashes are
// non-degenerate.
func syntheticSpec(n int) string {
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.0\ninfo:\n  title: synthetic\n  version: '1'\npaths:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "  /op%d:\n    get:\n      operationId: op_%d\n      summary: Operation %d for batch-timeout regression coverage\n      responses:\n        '200':\n          description: ok\n", i, i, i)
	}
	return sb.String()
}
