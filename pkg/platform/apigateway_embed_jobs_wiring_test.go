package platform

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
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
