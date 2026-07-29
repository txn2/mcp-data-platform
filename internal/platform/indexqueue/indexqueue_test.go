package indexqueue

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/goleak"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceindex"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// TestMain fails the package if any test leaks a goroutine. The worker, reaper,
// reconciler, and retention sweep all run background goroutines, so this guards
// the Start/Stop shutdown contract: every Handle a test starts must be fully
// stopped.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// testEmbedder returns a configured (non-noop) provider. New never invokes it in
// these tests (the worker is not started), so any real provider suffices.
func testEmbedder() embedding.Provider {
	return embedding.NewOllamaProvider(embedding.OllamaConfig{})
}

// stubStore is a benign indexjobs.Store: Claim reports the idle state and every
// other method returns zero values. It lets the worker/reaper/reconciler run
// their loops in the Start/Stop goleak tests without a live database.
type stubStore struct{}

func (stubStore) Enqueue(context.Context, indexjobs.Key, indexjobs.Trigger) (bool, error) {
	return false, nil
}

func (stubStore) Claim(context.Context, string) (*indexjobs.Job, error) {
	return nil, indexjobs.ErrNoJob
}
func (stubStore) Complete(context.Context, int64, string) error            { return nil }
func (stubStore) UpdateProgress(context.Context, int64, string, int) error { return nil }
func (stubStore) Retry(context.Context, int64, string, string) error       { return nil }
func (stubStore) Fail(context.Context, int64, string, string) error        { return nil }
func (stubStore) ReleaseExpiredLeases(context.Context) (int, error)        { return 0, nil }
func (stubStore) RenewLease(context.Context, int64, string, time.Duration) error {
	return nil
}

func (stubStore) Get(context.Context, int64) (*indexjobs.Job, error) {
	return nil, indexjobs.ErrNotFound
}

func (stubStore) CancelPending(context.Context, indexjobs.Key) (int, error) { return 0, nil }

func (stubStore) List(context.Context, indexjobs.ListFilter) ([]indexjobs.Job, error) {
	return nil, nil
}

func (stubStore) Counts(context.Context, string) (*indexjobs.KindCounts, error) {
	return &indexjobs.KindCounts{}, nil
}

func (stubStore) ActiveFailures(context.Context, string, int) ([]indexjobs.FailedUnit, error) {
	return nil, nil
}
func (stubStore) ResolveFailures(context.Context, indexjobs.Key) (int, error) { return 0, nil }
func (stubStore) PurgeTerminal(context.Context, int) (int, error)             { return 0, nil }

// fakeListener stands in for *indexjobs.Listener so the Start listener-failure
// fallback is exercised without pq's background reconnect goroutine. startErr,
// when non-nil, simulates the missing-LISTEN-privilege failure.
type fakeListener struct {
	startErr error
	started  bool
	stopped  bool
}

func (f *fakeListener) Start(context.Context) error {
	f.started = true
	return f.startErr
}
func (f *fakeListener) Stop() { f.stopped = true }

func TestNew_ToolsOnly(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), ModelName: "m", Workers: 3, RetentionDays: 7})
	if h == nil {
		t.Fatal("tools consumer always registers; New must return a handle")
	}
	if kinds := h.registry.Kinds(); len(kinds) != 1 || kinds[0] != "tools" {
		t.Errorf("kinds = %v; want [tools]", kinds)
	}
	if got := h.worker.Concurrency(); got != 3 {
		t.Errorf("worker concurrency = %d; want 3 (flowed from Config.Workers)", got)
	}
	if h.adminStore != nil {
		t.Error("no catalog store → no admin store")
	}
	if h.toolsStore == nil {
		t.Error("tools store must be wired")
	}
	if h.retainer == nil {
		t.Error("positive retention must wire a retainer")
	}
	if h.listener != nil {
		t.Error("empty DSN → no listener")
	}
}

func TestNew_WithCatalog(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{
		DB:            db,
		Embedder:      testEmbedder(),
		ModelName:     "m",
		CatalogStore:  apigatewaycatalog.NewMemoryStore(),
		RetentionDays: 7,
	})
	if h == nil {
		t.Fatal("New must return a handle")
	}
	if kinds := h.registry.Kinds(); len(kinds) != 2 || kinds[0] != "api_catalog" || kinds[1] != "tools" {
		t.Errorf("kinds = %v; want [api_catalog tools]", kinds)
	}
	if h.adminStore == nil || h.AdminStore() == nil {
		t.Error("catalog store present → admin store must be exposed")
	}
}

func TestNew_ConsumerGating(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{
		DB:        db,
		Embedder:  testEmbedder(),
		ModelName: "m",
		Consumers: Consumers{
			Memory:               true,
			Prompts:              true,
			PortalAssets:         true,
			PortalCollections:    true,
			PortalKnowledgePages: true,
			Resources:            true,
		},
		ResourceBucket: "resources",
	})
	if h == nil {
		t.Fatal("New must return a handle")
	}
	// tools + memory + prompts + assets + collections + knowledge pages + resources.
	if kinds := h.registry.Kinds(); len(kinds) != 7 {
		t.Errorf("kinds = %v; want 7 (tools + 6 gated consumers)", kinds)
	}
	if !slices.Contains(h.registry.Kinds(), resourceindex.SourceKind) {
		t.Errorf("kinds = %v; want the resources consumer registered", h.registry.Kinds())
	}
}

// A deployment with no resource store registers no resources consumer, so the
// worker never sweeps a table nothing writes to.
func TestNew_ResourcesConsumerGatedOff(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), ModelName: "m"})
	if h == nil {
		t.Fatal("New must return a handle")
	}
	if slices.Contains(h.registry.Kinds(), resourceindex.SourceKind) {
		t.Errorf("kinds = %v; resources must not register without a store", h.registry.Kinds())
	}
}

func TestNew_NegativeRetention_NoRetainer(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), RetentionDays: -1})
	if h == nil {
		t.Fatal("New must return a handle")
	}
	if h.retainer != nil {
		t.Error("non-positive retention must not wire a retainer")
	}
}

func TestNew_WithDSN_WiresListener(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), DSN: "postgres://ignored", RetentionDays: 7})
	if h == nil {
		t.Fatal("New must return a handle")
	}
	// NewListener does not connect until Start; the field is set but no goroutine
	// runs, so goleak stays green.
	if h.listener == nil {
		t.Error("non-empty DSN → listener must be wired")
	}
}

func TestHandle_NilSafeAccessors(t *testing.T) {
	t.Parallel()
	var h *Handle
	if h.Reporter() != nil {
		t.Error("nil Handle → nil Reporter")
	}
	if h.AdminStore() != nil {
		t.Error("nil Handle → nil AdminStore")
	}
	if h.ToolsIndexStore() != nil {
		t.Error("nil Handle → nil ToolsIndexStore")
	}
	if h.Registry() != nil {
		t.Error("nil Handle → nil Registry")
	}
}

func TestHandle_ReporterAndToolsStore(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), RetentionDays: 7})
	if h.Reporter() == nil {
		t.Error("store + registry present → Reporter must be non-nil")
	}
	if h.ToolsIndexStore() == nil {
		t.Error("tools consumer registered → ToolsIndexStore must be non-nil")
	}
}

// TestHandle_StartStop_ListenerFailureFallsBackToPoll proves the LISTEN-privilege
// fallback: when the listener's Start fails, Start clears it (poll-only) and
// still succeeds; Stop then reaps every started goroutine. TestMain's goleak
// check asserts no goroutine outlives the test.
func TestHandle_StartStop_ListenerFailureFallsBackToPoll(t *testing.T) {
	store := stubStore{}
	reg := indexjobs.NewRegistry()
	h := &Handle{
		registry:   reg,
		worker:     indexjobs.NewWorker(indexjobs.WorkerConfig{Store: store, Registry: reg}),
		reaper:     indexjobs.NewReaper(store, time.Hour),
		reconciler: indexjobs.NewReconciler(store, reg, time.Hour),
		listener:   &fakeListener{startErr: errors.New("no LISTEN privilege")},
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.listener != nil {
		t.Error("listener whose Start failed must be cleared to nil (poll-only fallback)")
	}
	if err := h.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestHandle_StartStop_RetainerAndListener covers the happy path: the retainer
// and a successfully-started listener are both driven and torn down.
func TestHandle_StartStop_RetainerAndListener(t *testing.T) {
	store := stubStore{}
	reg := indexjobs.NewRegistry()
	fl := &fakeListener{}
	h := &Handle{
		registry:   reg,
		worker:     indexjobs.NewWorker(indexjobs.WorkerConfig{Store: store, Registry: reg}),
		reaper:     indexjobs.NewReaper(store, time.Hour),
		reconciler: indexjobs.NewReconciler(store, reg, time.Hour),
		retainer:   indexjobs.NewRetainer(store, 7, time.Hour),
		listener:   fl,
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !fl.started || h.listener == nil {
		t.Error("a listener that starts cleanly must be retained")
	}
	if err := h.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if !fl.stopped {
		t.Error("Stop must stop the listener")
	}
}

func TestBootstrapToolsIndex(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup
	h := &Handle{
		store:      indexjobs.NewPostgresStore(db),
		toolsStore: toolsindex.NewStore(db),
	}
	mock.ExpectQuery("INSERT INTO index_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))
	h.bootstrapToolsIndex(context.Background())

	// No-op when not wired (no store / no tools store).
	(&Handle{}).bootstrapToolsIndex(context.Background())
}
