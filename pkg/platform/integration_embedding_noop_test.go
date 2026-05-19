//go:build integration

package platform_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// TestEmbeddingNoop_EndToEnd is the integration counterpart to the
// unit-level guards added for #429. With a real Postgres (pgvector
// enabled) and no memory.embedding.provider configured, the platform
// MUST:
//
//  1. Start successfully so non-embedding features stay available.
//  2. Wire a noop embedder (Kind() == KindNoop).
//  3. Refuse to start the apigateway embed-job queue.
//  4. Not write any rows to api_catalog_operation_embeddings even
//     after a spec is upserted into the catalog store.
//  5. Report status="unconfigured" from /api/v1/admin/embedding/status.
//
// This proves the structural property that no zero-vector row can
// reach the embeddings table while in the unconfigured state.
func TestEmbeddingNoop_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// pgvector/pgvector:pg16 is required so migration 000044's
	// `CREATE EXTENSION vector` succeeds. The base postgres:16-alpine
	// image other integration tests use does not carry the extension.
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
	require.NoError(t, err, "start postgres container")
	defer func() { _ = pgContainer.Terminate(ctx) }()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Build a Platform with DB but NO memory.embedding block. The
	// initMemory default branch picks the noop placeholder; the WARN
	// logged here is the operator's signal that semantic features
	// are off (see platform.go initMemory).
	cfg := &platform.Config{
		Server: platform.ServerConfig{Name: "integration-test"},
		Database: platform.DatabaseConfig{
			DSN:          dsn,
			MaxOpenConns: 5,
		},
		// Memory.Embedding.Provider left as zero value → noop fallback.
		// Memory layer is enabled implicitly when DB is available.
	}

	p, err := platform.New(platform.WithConfig(cfg))
	require.NoError(t, err, "build platform")
	defer p.Close()

	// (1) Startup succeeded: the deferred Close above runs only if
	//     New returned without error. Also assert that non-embedding
	//     wiring is in place.
	require.NotNil(t, p.MCPServer(), "MCP server must initialize even without an embedder")

	// (2) Embedder is the noop placeholder.
	prov := p.EmbeddingProvider()
	require.NotNil(t, prov, "platform must wire some Provider; noop is the documented fallback")
	assert.Equal(t, embedding.KindNoop, prov.Kind(), "default branch must select the noop placeholder")
	assert.False(t, embedding.IsConfigured(prov), "noop must report as not configured")

	// (3) Wire the apigateway catalog store from DB, then attempt to
	//     wire the embed-job queue. The queue MUST refuse to start
	//     because the embedder is noop.
	p.WireAPIGatewayCatalogStoreFromDB()
	p.WireAPIGatewayEmbedJobsFromDB()
	assert.Nil(t, p.APIGatewayEmbedJobsStore(),
		"embed-job queue must not start under noop embedder (#429)")

	// (4) Insert a spec through the catalog store directly so the
	//     test does not depend on the admin HTTP layer. If the
	//     wiring guard at (3) is wrong, persisting a spec would
	//     trigger the worker to write zero vectors into
	//     api_catalog_operation_embeddings. Assert no rows appear.
	catStore := p.APIGatewayCatalogStore()
	require.NotNil(t, catStore, "catalog store must wire even when embedder is noop")

	const (
		catalogID = "test-catalog"
		specName  = "petstore"
		specYAML  = `openapi: 3.0.0
info:
  title: Petstore
  version: 1.0.0
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      responses:
        "200":
          description: ok
    post:
      operationId: createPet
      summary: Create pet
      responses:
        "201":
          description: created
`
	)

	require.NoError(t, catStore.CreateCatalog(ctx, apigatewaycatalog.Catalog{
		ID: catalogID, Name: catalogID, Version: "v1",
	}))
	require.NoError(t, catStore.UpsertSpec(ctx, catalogID, apigatewaycatalog.SpecEntry{
		SpecName:   specName,
		Content:    specYAML,
		SourceKind: "inline",
	}))

	// Give the (non-existent) worker a window to do anything it
	// might be tempted to do. Under the bug, this is when zero
	// vectors would land. Under the fix, the queue doesn't exist
	// so this window is purely defensive.
	time.Sleep(500 * time.Millisecond)

	// (4 cont.) Query api_catalog_operation_embeddings directly.
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	var rowCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_catalog_operation_embeddings WHERE catalog_id = $1 AND spec_name = $2`,
		catalogID, specName,
	).Scan(&rowCount)
	require.NoError(t, err, "query embeddings table")
	assert.Equal(t, 0, rowCount,
		"no zero-vector rows must reach api_catalog_operation_embeddings under noop (#429)")

	// (5) /api/v1/admin/embedding/status reports unconfigured.
	adminH := admin.NewHandler(admin.Deps{
		Embedder: prov,
	}, nil)
	srv := httptest.NewServer(adminH)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/admin/embedding/status") //nolint:gosec,noctx // test server, fixed URL
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status struct {
		Kind      string `json:"kind"`
		Model     string `json:"model"`
		Dimension int    `json:"dimension"`
		Status    string `json:"status"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	assert.Equal(t, embedding.KindNoop, status.Kind)
	assert.Equal(t, "unconfigured", status.Status)
}
