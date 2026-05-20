package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/embedjobs"
)

// TestWireAPIGatewayEmbedJobsFromDB_NoopEmbedderSkips proves the
// wiring-layer guard for #429: when the embedder is the noop
// placeholder AND a database is available, the entire job queue
// (store, worker, reaper, reconciler, listener) MUST NOT be wired.
// Standing it up against the noop would fill
// api_catalog_operation_embeddings with zero vectors that the catalog
// health endpoint reports as "indexed" while semantic ranking quietly
// degrades to nonsense.
//
// A non-nil sql.DB is required to bypass the earlier "no database"
// branch and reach the noop guard itself.
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
	if p.apiGatewayEmbedJobsStore != nil {
		t.Errorf("noop embedder must not wire the job store; got %T", p.apiGatewayEmbedJobsStore)
	}
	if p.apiGatewayEmbedJobsWorker != nil {
		t.Errorf("noop embedder must not start the worker")
	}
	if p.apiGatewayEmbedJobsReaper != nil {
		t.Errorf("noop embedder must not start the reaper")
	}
	if p.apiGatewayEmbedJobsReconciler != nil {
		t.Errorf("noop embedder must not start the reconciler")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_NilEmbedderSkips covers the
// existing nil-embedder branch, kept asserted alongside the noop
// branch so a future refactor that collapses them does not lose
// either guarantee.
func TestWireAPIGatewayEmbedJobsFromDB_NilEmbedderSkips(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	p := &Platform{db: db, embeddingProv: nil}
	p.WireAPIGatewayEmbedJobsFromDB()
	if p.apiGatewayEmbedJobsStore != nil {
		t.Errorf("nil embedder must not wire the job store")
	}
}

// TestWireAPIGatewayEmbedJobsFromDB_WiresWorkerWithConfiguredConcurrency
// proves the production-path branch (real DB + real embedder + real
// catalog store): the store, worker, reaper, and reconciler all get
// wired. The Concurrency value flows from APIGateway.EmbedJobs.Workers
// into the WorkerConfig (#430). lifecycle.OnStart hooks are registered
// but not invoked here so the goroutines never spawn.
func TestWireAPIGatewayEmbedJobsFromDB_WiresWorkerWithConfiguredConcurrency(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	cfg := &Config{}
	cfg.APIGateway.EmbedJobs.Workers = 3

	reg := registry.NewRegistry()
	// APIGatewayCatalogStore() reads through the registered
	// apigateway toolkit, so register one before wiring the store.
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

	if p.apiGatewayEmbedJobsStore == nil {
		t.Fatal("real embedder + DB + catalog store must wire the job store")
	}
	if p.apiGatewayEmbedJobsWorker == nil {
		t.Fatal("worker must be constructed")
	}
	if got := p.apiGatewayEmbedJobsWorker.Concurrency(); got != 3 {
		t.Errorf("Concurrency = %d; want 3 (the value flowed from apigateway.embed_jobs.workers)", got)
	}
	if p.apiGatewayEmbedJobsReaper == nil {
		t.Fatal("reaper must be constructed")
	}
	if p.apiGatewayEmbedJobsReconciler == nil {
		t.Fatal("reconciler must be constructed")
	}
}

// TestStopAPIGatewayEmbedJobs_CleanShutdownReturnsNil proves the happy
// path: when Worker, Reaper, and Reconciler are constructed but never
// Started, their Stop calls are immediate no-ops and the bounded
// helper returns nil. This is the path taken by the OnStop callback
// after a normal startup.
func TestStopAPIGatewayEmbedJobs_CleanShutdownReturnsNil(t *testing.T) {
	p := &Platform{}
	worker := embedjobs.NewWorker(embedjobs.WorkerConfig{})
	reaper := embedjobs.NewReaper(nil, time.Second)
	reconciler := embedjobs.NewReconciler(nil, time.Second)

	if err := p.stopAPIGatewayEmbedJobs(context.Background(), worker, reaper, reconciler); err != nil {
		t.Errorf("stopAPIGatewayEmbedJobs returned %v; want nil on clean shutdown", err)
	}
}

// TestStopAPIGatewayEmbedJobs_RespectsCanceledContext proves the
// safety-net path: a pre-canceled context propagates as ctx.Err() so
// a hung worker cannot exceed the K8s termination grace period.
// Worker.Stop on never-Started components is instant in practice, so
// to genuinely exercise the deadline race we pre-cancel the context.
// The select inside boundedStop will observe ctx.Done either before
// or after the inner fn completes; in either case the return must be
// either nil (race won by fn) or context.Canceled (race won by ctx).
func TestStopAPIGatewayEmbedJobs_RespectsCanceledContext(t *testing.T) {
	p := &Platform{}
	worker := embedjobs.NewWorker(embedjobs.WorkerConfig{})
	reaper := embedjobs.NewReaper(nil, time.Second)
	reconciler := embedjobs.NewReconciler(nil, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.stopAPIGatewayEmbedJobs(ctx, worker, reaper, reconciler)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("stopAPIGatewayEmbedJobs err = %v; want nil or context.Canceled", err)
	}
}
