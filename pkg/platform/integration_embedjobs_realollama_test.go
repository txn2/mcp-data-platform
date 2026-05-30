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
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

// TestEmbedJobs_RealOllama_BatchedPathCompletes drives the production
// embedding path end-to-end against a real CPU-only Ollama running in
// testcontainers. The test exists specifically because the batched
// /api/embed timeout regression in v1.64.0 (#445) shipped undetected:
// every prior integration test used synthetic delays in stub embedders,
// none exercised actual Ollama batch latency.
//
// Acceptance: a spec with enough operations to trigger the batched
// path (one POST per chunk of up to 32 texts) completes within the
// worker's Ollama timeout (5m), and the api-catalog Sink persists one
// vector per operation. Runs through the real indexjobs worker with
// the api-catalog Source + Sink, so it also covers the source_kind
// routing and the framework-owned embed loop.
func TestEmbedJobs_RealOllama_BatchedPathCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()

	ollama := testollama.Get(t)

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

	// Seed a catalog with one 30-operation spec. Forces the batched
	// path (default batch size 32); a 30-text batch is the exact shape
	// that timed out in #445.
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

	// Wire the real catalog store + index-jobs queue against the real
	// Ollama provider with the worker's 5m embed timeout (mirrors what
	// the production worker constructs for Ollama).
	prov := ollama.Provider(5 * time.Minute)
	catStore := apigatewaycatalog.NewPostgresStore(db)
	jobStore := indexjobs.NewPostgresStore(db)

	reg := indexjobs.NewRegistry()
	require.NoError(t, reg.Register(&catalogTestSource{store: catStore}, catalogindex.NewSink(catStore)))

	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store:    jobStore,
		Registry: reg,
		Embedder: prov,
		WorkerID: "real-ollama-test-worker",
	})
	worker.Start(ctx)
	defer worker.Stop()

	_, err = jobStore.Enqueue(ctx,
		indexjobs.Key{SourceKind: catalogindex.SourceKind, SourceID: catalogindex.EncodeSourceID(catalogID, specName)},
		indexjobs.TriggerWrite)
	require.NoError(t, err)

	admin := catalogindex.NewAdminStore(jobStore, db)
	deadline := time.Now().Add(10 * time.Minute)
	var final catalogindex.SpecStatusRow
	for time.Now().Before(deadline) {
		rows, qerr := admin.SpecStatuses(ctx, catalogID)
		require.NoError(t, qerr)
		require.Len(t, rows, 1)
		final = rows[0]
		if final.JobStatus == catalogindex.StatusSucceeded {
			break
		}
		if final.JobStatus == catalogindex.StatusFailed {
			t.Fatalf("embed job failed: attempts=%d last_error=%q", final.JobAttempts, final.JobLastError)
		}
		time.Sleep(3 * time.Second)
	}

	require.Equalf(t, catalogindex.StatusSucceeded, final.JobStatus,
		"job did not succeed within deadline: status=%s attempts=%d last_error=%q",
		final.JobStatus, final.JobAttempts, final.JobLastError)
	require.Equal(t, ops, final.EmbeddingCount,
		"expected %d vectors persisted, got %d", ops, final.EmbeddingCount)
}

// catalogTestSource bridges the indexjobs worker to the real catalog
// store + OpenAPI parser. The production equivalent (platform's
// unexported catalogSource) is identical in shape; redeclared here
// because this test runs in package platform_test.
type catalogTestSource struct {
	store apigatewaycatalog.Store
}

func (*catalogTestSource) Kind() string { return catalogindex.SourceKind }

func (s *catalogTestSource) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	catalogID, specName, ok := catalogindex.DecodeSourceID(sourceID)
	if !ok {
		return nil, fmt.Errorf("catalogTestSource: malformed source_id %q", sourceID)
	}
	spec, err := s.store.GetSpec(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogTestSource: %w", err)
	}
	ops, err := apigateway.BuildOperationItems(spec.Content, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogTestSource: %w", err)
	}
	items := make([]indexjobs.Item, len(ops))
	for i, op := range ops {
		items[i] = indexjobs.Item{ItemID: op.OperationID, Text: op.Text}
	}
	return items, nil
}

func (*catalogTestSource) OnSucceeded(_ string) {}

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
