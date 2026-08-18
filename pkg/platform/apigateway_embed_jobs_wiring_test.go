package platform

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/indexqueue"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptindex"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlayer"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// TestResolveEmbedJobsTuning covers the config-defaulting helper:
// unset config falls back to the package defaults, explicit values
// flow through, and the embed_timeout >= lease_duration ordering logs
// a warning (exercised here for the branch, not the log output).
func TestResolveEmbedJobsTuning(t *testing.T) {
	t.Parallel()
	// Defaults when unset.
	p := &Platform{config: &Config{}}
	lease, batch := p.resolveEmbedJobsTuning()
	if lease != indexjobs.DefaultLeaseDuration || batch != indexjobs.DefaultEmbedBatchSize {
		t.Errorf("defaults = (%v, %d); want (%v, %d)", lease, batch, indexjobs.DefaultLeaseDuration, indexjobs.DefaultEmbedBatchSize)
	}
	// Explicit values flow through, and embed_timeout >= lease warns.
	p2 := &Platform{config: &Config{}}
	p2.config.APIGateway.EmbedJobs.LeaseDuration = 2 * time.Minute
	p2.config.APIGateway.EmbedJobs.BatchSize = 16
	p2.config.APIGateway.EmbedJobs.EmbedTimeout = 3 * time.Minute // >= lease -> warn branch
	lease, batch = p2.resolveEmbedJobsTuning()
	if lease != 2*time.Minute || batch != 16 {
		t.Errorf("explicit = (%v, %d); want (2m, 16)", lease, batch)
	}
}

// TestResolveRetentionDays covers the retention-window defaulting: unset
// config falls back to the package default, an explicit positive value
// flows through, and a negative value passes through to signal "disabled".
func TestResolveRetentionDays(t *testing.T) {
	t.Parallel()
	p := &Platform{config: &Config{}}
	if got := p.resolveRetentionDays(); got != indexjobs.DefaultRetentionDays {
		t.Errorf("unset retention_days = %d; want default %d", got, indexjobs.DefaultRetentionDays)
	}
	p.config.APIGateway.EmbedJobs.RetentionDays = 30
	if got := p.resolveRetentionDays(); got != 30 {
		t.Errorf("explicit retention_days = %d; want 30", got)
	}
	p.config.APIGateway.EmbedJobs.RetentionDays = -1
	if got := p.resolveRetentionDays(); got != -1 {
		t.Errorf("negative retention_days = %d; want -1 (disabled)", got)
	}
}

// TestWorkerEmbedder covers the worker-embedder selection: a non-Ollama
// platform embedder is reused verbatim; an Ollama embedder yields a dedicated
// longer-timeout provider (a distinct instance), so a batched embed on CPU-only
// Ollama does not exhaust the shared 30s request-path timeout.
func TestWorkerEmbedder(t *testing.T) {
	t.Parallel()
	shared := embedding.NewNoopProvider(8)
	p := &Platform{config: &Config{}, embeddingProv: shared}
	if got := p.workerEmbedder(); got != shared {
		t.Error("non-ollama embedder should be reused verbatim")
	}

	p.config.Memory.Embedding.Provider = "ollama"
	if got := p.workerEmbedder(); got == nil || got == shared {
		t.Error("ollama embedder should yield a distinct dedicated worker provider")
	}
}

// TestIndexJobsPreconditions_AlreadyWired covers the idempotency
// guard: a second wiring attempt is refused once the queue handle is set.
func TestIndexJobsPreconditions_AlreadyWired(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	p := &Platform{indexQueue: indexqueue.New(indexqueue.Config{DB: db, Embedder: embedding.NewOllamaProvider(embedding.OllamaConfig{})})}
	if p.indexJobsPreconditions() {
		t.Error("already-wired platform should refuse to re-wire")
	}
}

// TestIndexJobsPreconditions_NoDatabase and _NoEmbedder cover the two skip
// branches: without a database, or with an unconfigured (noop) embedder, the
// queue must not wire.
func TestIndexJobsPreconditions_NoDatabase(t *testing.T) {
	t.Parallel()
	p := &Platform{embeddingProv: embedding.NewOllamaProvider(embedding.OllamaConfig{})}
	if p.indexJobsPreconditions() {
		t.Error("no database should skip wiring")
	}
}

func TestIndexJobsPreconditions_NoEmbedder(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	p := &Platform{db: db, embeddingProv: embedding.NewNoopProvider(768)}
	if p.indexJobsPreconditions() {
		t.Error("noop embedder should skip wiring")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_NoCatalogStore proves the delegation path
// with no api-catalog store: the queue still wires (tools consumer alone), the
// handle is set, but no admin store is exposed. Assembly detail (registered
// kinds, retainer) is covered in pkg/platform/indexqueue.
func TestWireAPIGatewayEmbedJobsFromDB_NoCatalogStore(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	p := &Platform{
		db:              db,
		embeddingProv:   embedding.NewOllamaProvider(embedding.OllamaConfig{}),
		config:          &Config{},
		toolkitRegistry: registry.NewRegistry(), // no apigateway toolkit -> no catalog store
		lifecycle:       &Lifecycle{},
	}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexQueue == nil {
		t.Fatal("queue should wire for the tools consumer even without a catalog store")
	}
	if p.APIGatewayEmbedJobsStore() != nil {
		t.Error("admin store should be nil with no catalog store")
	}
	if p.IndexJobsReporter() == nil {
		t.Error("reporter should be exposed once the queue is wired")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_NoopEmbedderSkips proves the wiring-layer
// guard for #429: with the noop placeholder embedder AND a database, the queue
// MUST NOT wire (no handle). Standing it up against the noop would fill the
// vector tables with zero vectors that health endpoints report as "indexed"
// while semantic ranking quietly degrades.
func TestWireAPIGatewayEmbedJobsFromDB_NoopEmbedderSkips(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{db: db, embeddingProv: embedding.NewNoopProvider(768)}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexQueue != nil {
		t.Error("noop embedder must not wire the queue")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_NilEmbedderSkips covers the nil-embedder
// branch, kept asserted alongside the noop branch so a future refactor that
// collapses them does not lose either guarantee.
func TestWireAPIGatewayEmbedJobsFromDB_NilEmbedderSkips(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{db: db, embeddingProv: nil}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexQueue != nil {
		t.Error("nil embedder must not wire the queue")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_WithCatalogStore proves the production path
// (real DB + real embedder + real catalog store): the queue wires, the handle
// is set, the admin store is exposed for the admin handler, and a second call
// is idempotent (the handle is not replaced).
func TestWireAPIGatewayEmbedJobsFromDB_WithCatalogStore(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	cfg := &Config{}
	cfg.APIGateway.EmbedJobs.Workers = 3

	reg := registry.NewRegistry()
	// APIGatewayCatalogStore() reads through the registered apigateway
	// toolkit, so register one before wiring the store.
	if err := reg.Register(apigatewaykit.New("test")); err != nil {
		t.Fatalf("register apigateway toolkit: %v", err)
	}
	p := &Platform{
		db:              db,
		embeddingProv:   embedding.NewOllamaProvider(embedding.OllamaConfig{}),
		config:          cfg,
		toolkitRegistry: reg,
		lifecycle:       &Lifecycle{},
	}
	p.WireAPIGatewayCatalogStore(apigatewaycatalog.NewMemoryStore())
	p.WireAPIGatewayEmbedJobsFromDB()

	if p.indexQueue == nil {
		t.Fatal("real embedder + DB + catalog store must wire the queue")
	}
	if p.APIGatewayEmbedJobsStore() == nil {
		t.Fatal("admin store must be exposed for the admin handler")
	}

	// Idempotent: a second call is a no-op (the precondition sees the handle).
	first := p.indexQueue
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexQueue != first {
		t.Error("second WireAPIGatewayEmbedJobsFromDB must not replace the handle")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_ToolEnumeratorSeam is the end-to-end
// integration test for the tool-enumeration seam (CLAUDE.md rule 5): it drives
// the real WireAPIGatewayEmbedJobsFromDB wiring against a live MCP server, then
// reaches the tools source the assembled queue registered and enumerates it.
// This proves platformToolEnumerator and platformFindToolsName actually reach
// the queue through indexqueue.New — a future edit that passed the wrong Config
// field or dropped DiscoveryToolName would fail here rather than silently
// indexing zero (or the discovery) tools.
func TestWireAPIGatewayEmbedJobsFromDB_ToolEnumeratorSeam(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	// A live in-memory MCP server carrying two real tools plus the discovery tool.
	srv := findToolsTestServer("alpha", "beta")
	p := &Platform{
		db:              db,
		mcpServer:       srv,
		embeddingProv:   embedding.NewOllamaProvider(embedding.OllamaConfig{}),
		config:          &Config{},
		toolkitRegistry: registry.NewRegistry(),
		lifecycle:       &Lifecycle{},
	}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexQueue == nil {
		t.Fatal("queue must wire for the tools consumer")
	}

	// Reach the tools source the assembled queue registered and drive it through
	// the real injected enumerator.
	src, _, ok := p.indexQueue.Registry().Lookup(toolsindex.SourceKind)
	if !ok {
		t.Fatal("assembled queue must register the tools source")
	}
	items, err := src.LoadItems(context.Background(), toolsindex.SourceID)
	if err != nil {
		t.Fatalf("tools source LoadItems: %v", err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.ItemID] = true
	}
	if !got["alpha"] || !got["beta"] {
		t.Errorf("live tool corpus not enumerated through the queue: got %v; want alpha+beta", got)
	}
	if got[platformFindToolsName] {
		t.Error("discovery tool must be excluded via the wired DiscoveryToolName")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_ScriptIndexProducerSeam is the end-to-end
// integration test for the managed-script index seam (CLAUDE.md rule 5). The
// producer is created inside the script tool layer, carried to the queue as one
// element of Config.Producers, and bound only because the scripts consumer
// registered. A unit test on either end would pass with the two never meeting;
// this drives the real wiring and then proves a script write reaches the job
// table, which is the whole point of the write-path path existing.
func TestWireAPIGatewayEmbedJobsFromDB_ScriptIndexProducerSeam(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{
		db:              db,
		embeddingProv:   embedding.NewOllamaProvider(embedding.OllamaConfig{}),
		config:          &Config{},
		toolkitRegistry: registry.NewRegistry(),
		lifecycle:       &Lifecycle{},
		scripts:         scriptlayer.New(scriptlayer.Config{DB: db}),
	}
	p.WireAPIGatewayEmbedJobsFromDB()

	if p.indexQueue == nil {
		t.Fatal("queue should wire with a database and a real embedder")
	}
	if !slices.Contains(p.indexQueue.Registry().Kinds(), scriptindex.SourceKind) {
		t.Fatalf("kinds = %v; want the scripts consumer registered", p.indexQueue.Registry().Kinds())
	}

	// The bound producer reaches the queue's own job store: the insert lands on
	// this connection, which is what proves the two ends met.
	mock.ExpectQuery("INSERT INTO index_jobs").
		WithArgs(scriptindex.SourceKind, "script-1", string(indexjobs.TriggerWrite)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec("SELECT pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))

	p.scripts.IndexProducer().NotifyWrite(context.Background(), "script-1")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the script write did not reach the job store: %v", err)
	}
}
