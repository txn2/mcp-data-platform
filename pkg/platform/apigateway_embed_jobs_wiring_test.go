package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
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

// TestIndexJobsPreconditions_AlreadyWired covers the idempotency
// guard: a second wiring attempt is refused.
func TestIndexJobsPreconditions_AlreadyWired(t *testing.T) {
	t.Parallel()
	p := &Platform{indexJobsStore: indexjobs.NewPostgresStore(nil)}
	if _, ok := p.indexJobsPreconditions(); ok {
		t.Error("already-wired platform should refuse to re-wire")
	}
}

// TestIndexJobsPreconditions_NoCatalogStore covers the branch where a
// database and embedder are present but no api-catalog toolkit is
// registered, so there is no catalog store to read specs from.
func TestIndexJobsPreconditions_NoCatalogStore(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	p := &Platform{
		db:              db,
		embeddingProv:   embedding.NewOllamaProvider(embedding.OllamaConfig{}),
		toolkitRegistry: registry.NewRegistry(), // no apigateway toolkit -> no catalog store
	}
	if _, ok := p.indexJobsPreconditions(); ok {
		t.Error("missing catalog store should refuse to wire")
	}
}

// TestCatalogSource_OnSucceeded_WithRegistry covers the reload path:
// with a registered api-gateway toolkit, OnSucceeded walks the
// registry and invokes the toolkit's connection reload (a no-op with
// zero connections, but the loop and type assertion run).
func TestCatalogSource_OnSucceeded_WithRegistry(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	if err := reg.Register(apigatewaykit.New("test")); err != nil {
		t.Fatalf("register: %v", err)
	}
	s := &catalogSource{registry: reg}
	s.OnSucceeded(catalogindex.EncodeSourceID("cat", "spec")) // must not panic; reloads the catalog
}

// TestWireAPIGatewayEmbedJobsFromDB_NoopEmbedderSkips proves the
// wiring-layer guard for #429: when the embedder is the noop
// placeholder AND a database is available, the entire index-jobs
// queue (store, worker, reaper, reconciler, listener) MUST NOT be
// wired. Standing it up against the noop would fill the vector
// tables with zero vectors that the health endpoints report as
// "indexed" while semantic ranking quietly degrades to nonsense.
func TestWireAPIGatewayEmbedJobsFromDB_NoopEmbedderSkips(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{
		db:            db,
		embeddingProv: embedding.NewNoopProvider(768),
	}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexJobsStore != nil {
		t.Errorf("noop embedder must not wire the job store; got %T", p.indexJobsStore)
	}
	if p.indexJobsWorker != nil {
		t.Errorf("noop embedder must not start the worker")
	}
	if p.indexJobsReaper != nil {
		t.Errorf("noop embedder must not start the reaper")
	}
	if p.indexJobsReconciler != nil {
		t.Errorf("noop embedder must not start the reconciler")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_NilEmbedderSkips covers the
// nil-embedder branch, kept asserted alongside the noop branch so a
// future refactor that collapses them does not lose either guarantee.
func TestWireAPIGatewayEmbedJobsFromDB_NilEmbedderSkips(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{db: db, embeddingProv: nil}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.indexJobsStore != nil {
		t.Errorf("nil embedder must not wire the job store")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_WiresWorkerWithConfiguredConcurrency
// proves the production-path branch (real DB + real embedder + real
// catalog store): the store, registry, worker, reaper, reconciler, and
// admin store all get wired, the api_catalog kind is registered, and
// the Concurrency value flows from APIGateway.EmbedJobs.Workers into
// the WorkerConfig. lifecycle.OnStart hooks are registered but not
// invoked here so the goroutines never spawn.
func TestWireAPIGatewayEmbedJobsFromDB_WiresWorkerWithConfiguredConcurrency(t *testing.T) {
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

	if p.indexJobsStore == nil {
		t.Fatal("real embedder + DB + catalog store must wire the job store")
	}
	if p.indexJobsRegistry == nil {
		t.Fatal("registry must be wired")
	}
	if kinds := p.indexJobsRegistry.Kinds(); len(kinds) != 1 || kinds[0] != "api_catalog" {
		t.Errorf("registry kinds = %v; want [api_catalog]", kinds)
	}
	if p.APIGatewayEmbedJobsStore() == nil {
		t.Fatal("admin store must be exposed for the admin handler")
	}
	if p.indexJobsWorker == nil {
		t.Fatal("worker must be constructed")
	}
	if got := p.indexJobsWorker.Concurrency(); got != 3 {
		t.Errorf("Concurrency = %d; want 3 (the value flowed from apigateway.embed_jobs.workers)", got)
	}
	if p.indexJobsReaper == nil {
		t.Fatal("reaper must be constructed")
	}
	if p.indexJobsReconciler == nil {
		t.Fatal("reconciler must be constructed")
	}
}

// TestStopIndexJobs_CleanShutdownReturnsNil proves the happy path:
// when Worker, Reaper, and Reconciler are constructed but never
// Started, their Stop calls are immediate no-ops and the bounded
// helper returns nil. This is the path taken by the OnStop callback
// after a normal startup.
func TestStopIndexJobs_CleanShutdownReturnsNil(t *testing.T) {
	p := &Platform{}
	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{})
	reaper := indexjobs.NewReaper(nil, time.Second)
	reconciler := indexjobs.NewReconciler(nil, nil, time.Second)

	if err := p.stopIndexJobs(context.Background(), worker, reaper, reconciler); err != nil {
		t.Errorf("stopIndexJobs returned %v; want nil on clean shutdown", err)
	}
}

// TestStopIndexJobs_RespectsCanceledContext proves the safety-net
// path: a pre-canceled context propagates as ctx.Err() so a hung
// worker cannot exceed the K8s termination grace period. The select
// inside boundedStop observes ctx.Done either before or after the
// inner fn completes; in either case the return must be nil (race won
// by fn) or context.Canceled (race won by ctx).
func TestStopIndexJobs_RespectsCanceledContext(t *testing.T) {
	p := &Platform{}
	worker := indexjobs.NewWorker(indexjobs.WorkerConfig{})
	reaper := indexjobs.NewReaper(nil, time.Second)
	reconciler := indexjobs.NewReconciler(nil, nil, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.stopIndexJobs(ctx, worker, reaper, reconciler)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("stopIndexJobs err = %v; want nil or context.Canceled", err)
	}
}
