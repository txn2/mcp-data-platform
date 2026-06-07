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
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// slowEmbedder is an embedding.Provider whose EmbedBatch sleeps so
// the worker crosses several chunk boundaries with observable delay,
// letting the test watch items_done advance before the final Complete
// commits (#430). It returns fixed-dimension zero vectors.
type slowEmbedder struct {
	dim        int
	batchDelay time.Duration
}

func (e *slowEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, e.dim), nil
}

func (e *slowEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case <-time.After(e.batchDelay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dim)
	}
	return out, nil
}

func (e *slowEmbedder) Dimension() int { return e.dim }
func (*slowEmbedder) Kind() string     { return "slow" }

// fixedItemsSource returns a fixed set of items so the embed pass has
// a known number of chunks to publish progress across.
type fixedItemsSource struct{ count int }

func (*fixedItemsSource) Kind() string { return "test_progress" }

func (s *fixedItemsSource) LoadItems(_ context.Context, _ string) ([]indexjobs.Item, error) {
	items := make([]indexjobs.Item, s.count)
	for i := range items {
		items[i] = indexjobs.Item{ItemID: string(rune('a' + i)), Text: "text"}
	}
	return items, nil
}

func (*fixedItemsSource) OnSucceeded(_ string) {}

// inMemorySink discards vectors; the test asserts only on items_done
// in index_jobs, not on persisted vectors.
type inMemorySink struct{}

func (*inMemorySink) Kind() string { return "test_progress" }
func (*inMemorySink) ListExisting(_ context.Context, _ indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return nil, nil
}

func (*inMemorySink) Upsert(_ context.Context, _ indexjobs.Key, _ []indexjobs.Vector) error {
	return nil
}

func (*inMemorySink) UpsertBatch(_ context.Context, _ indexjobs.Key, _ []indexjobs.Vector) error {
	return nil
}
func (*inMemorySink) StampExpected(_ context.Context, _ indexjobs.Key, _ int) error { return nil }
func (*inMemorySink) FindGaps(_ context.Context) ([]string, error)                  { return nil, nil }

// TestIndexJobsProgress_EndToEnd verifies the wiring of the items_done
// counter from the worker's chunk callback through the Postgres
// UPDATE to the List read path (#430), now against the generic
// index_jobs queue.
func TestIndexJobsProgress_EndToEnd(t *testing.T) {
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

	store := indexjobs.NewPostgresStore(db)
	_, err = store.Enqueue(ctx, indexjobs.Key{SourceKind: "test_progress", SourceID: "unit1"}, indexjobs.TriggerWrite)
	require.NoError(t, err)

	reg := indexjobs.NewRegistry()
	require.NoError(t, reg.Register(&fixedItemsSource{count: 12}, &inMemorySink{}))

	w := indexjobs.NewWorker(indexjobs.WorkerConfig{
		Store:     store,
		Registry:  reg,
		Embedder:  &slowEmbedder{dim: 4, batchDelay: 100 * time.Millisecond},
		BatchSize: 4, // 12 items / 4 = 3 chunks -> progress at 4, 8, 12
		WorkerID:  "test-worker",
	})
	w.Start(ctx)
	defer w.Stop()

	var observations []int
	deadline := time.Now().Add(5 * time.Second)
	var lastStatus indexjobs.Status
	for time.Now().Before(deadline) {
		jobs, listErr := store.List(ctx, indexjobs.ListFilter{SourceKind: "test_progress"})
		require.NoError(t, listErr)
		require.Len(t, jobs, 1)
		job := jobs[0]
		if job.Status == indexjobs.StatusRunning && job.ItemsDone > 0 {
			if len(observations) == 0 || observations[len(observations)-1] != job.ItemsDone {
				observations = append(observations, job.ItemsDone)
			}
		}
		lastStatus = job.Status
		if job.Status == indexjobs.StatusSucceeded {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	assert.Equal(t, indexjobs.StatusSucceeded, lastStatus, "job should reach succeeded")
	if len(observations) < 2 {
		t.Fatalf("expected at least 2 distinct items_done observations during running; got %v", observations)
	}
	for i := 1; i < len(observations); i++ {
		assert.Greater(t, observations[i], observations[i-1],
			"items_done must be strictly increasing during running: %v", observations)
	}
}
