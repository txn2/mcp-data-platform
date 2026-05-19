//go:build integration

package platform_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/embedjobs"
)

// slowEmbedComputer drives the worker through several chunk
// boundaries so the progress publish path exercises real DB writes
// against the integration container. Each Compute call invokes the
// supplied progress callback once per synthetic "chunk" with a small
// sleep between them so the test can observe embedded_so_far moving
// before the final Complete commits.
type slowEmbedComputer struct {
	chunkDelay  time.Duration
	chunkCounts []int
	finalRows   []embedjobs.ComputedEmbedding
}

func (c *slowEmbedComputer) Compute(ctx context.Context, _, _ string, _ map[string]embedjobs.ExistingEmbedding, progress func(int)) ([]embedjobs.ComputedEmbedding, error) {
	for _, n := range c.chunkCounts {
		select {
		case <-time.After(c.chunkDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if progress != nil {
			progress(n)
		}
	}
	return c.finalRows, nil
}

// noopReloader satisfies the ConnectionReloader interface without
// touching a real toolkit registry: this integration test does not
// boot the api-gateway toolkit, only the job queue.
type noopReloader struct{}

func (*noopReloader) ReloadConnectionsByCatalog(_ string) {}

// inMemoryPersister substitutes for the real catalog persister.
// The integration test asserts on embedded_so_far (which lives on
// api_catalog_embedding_jobs); the embedding-vector rows themselves
// are not exercised here.
type inMemoryPersister struct{}

func (*inMemoryPersister) ListExisting(_ context.Context, _, _ string) (map[string]embedjobs.ExistingEmbedding, error) {
	return nil, nil
}

func (*inMemoryPersister) Upsert(_ context.Context, _, _ string, _ []embedjobs.ComputedEmbedding) error {
	return nil
}

func (*inMemoryPersister) StampOperationCount(_ context.Context, _, _ string, _ int) error {
	return nil
}

// staticResolver returns a fixed content blob for the (catalog,
// spec) the test enqueues.
type staticResolver struct{}

func (*staticResolver) GetSpecContent(_ context.Context, _, _ string) (string, error) {
	return "spec content (unused; slowEmbedComputer ignores it)", nil
}

// TestEmbedJobsProgress_EndToEnd verifies the wiring of the
// embedded_so_far counter from worker chunk callback through the
// Postgres UPDATE to the SpecStatuses read path used by the admin
// UI (#430).
func TestEmbedJobsProgress_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

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

	// A spec row is required for the FK on api_catalog_embedding_jobs.
	_, err = db.ExecContext(ctx, `
		INSERT INTO api_catalogs (id, name, display_name, version, description, created_at, updated_at)
		VALUES ('c1', 'c1', 'c1', 'v1', '', NOW(), NOW())
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO api_catalog_specs (catalog_id, spec_name, source_kind, content, operation_count, created_at, updated_at)
		VALUES ('c1', 's1', 'inline', 'irrelevant', 12, NOW(), NOW())
	`)
	require.NoError(t, err)

	store := embedjobs.NewPostgresStore(db)
	_, err = store.Enqueue(ctx, embedjobs.SpecKey{CatalogID: "c1", SpecName: "s1"}, embedjobs.KindSpecWrite)
	require.NoError(t, err)

	computer := &slowEmbedComputer{
		chunkDelay:  100 * time.Millisecond,
		chunkCounts: []int{4, 8, 12},
	}
	w := embedjobs.NewWorker(embedjobs.WorkerConfig{
		Store:     store,
		Resolver:  &staticResolver{},
		Computer:  computer,
		Persister: &inMemoryPersister{},
		Reloader:  &noopReloader{},
		WorkerID:  "test-worker",
		PollEvery: 25 * time.Millisecond,
	})
	w.Start(ctx)
	defer w.Stop()

	// Poll SpecStatuses while the worker runs. Assert embedded_so_far
	// is strictly increasing across at least two observations before
	// the job completes.
	var observations []int
	deadline := time.Now().Add(2 * time.Second)
	var lastJobStatus embedjobs.Status
	for time.Now().Before(deadline) {
		rows, statusErr := store.SpecStatuses(ctx, "c1")
		require.NoError(t, statusErr)
		require.Len(t, rows, 1)
		row := rows[0]
		if row.JobStatus == embedjobs.StatusRunning && row.EmbeddedSoFar > 0 {
			if len(observations) == 0 || observations[len(observations)-1] != row.EmbeddedSoFar {
				observations = append(observations, row.EmbeddedSoFar)
			}
		}
		lastJobStatus = row.JobStatus
		if row.JobStatus == embedjobs.StatusSucceeded {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	assert.Equal(t, embedjobs.StatusSucceeded, lastJobStatus, "job should reach succeeded")
	if len(observations) < 2 {
		t.Fatalf("expected at least 2 distinct embedded_so_far observations during running; got %v", observations)
	}
	// Each observation must be greater than the prior one: the
	// counter only moves forward inside a single Claim.
	for i := 1; i < len(observations); i++ {
		assert.Greater(t, observations[i], observations[i-1],
			"embedded_so_far must be strictly increasing during running: %v", observations)
	}
}
