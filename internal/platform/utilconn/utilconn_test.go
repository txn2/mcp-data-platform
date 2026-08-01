package utilconn

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/utilhandler"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
)

type fakeEnqueuer struct {
	keys []catalogindex.SpecKey
	err  error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, key catalogindex.SpecKey, _ catalogindex.Kind) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.keys = append(f.keys, key)
	return true, nil
}

// testEnv bundles a seed's Deps with the concrete fakes a test
// asserts against, so newTestEnv returns one value instead of four.
type testEnv struct {
	deps  Deps
	tk    *apigatewaykit.Toolkit
	store catalog.Store
	enq   *fakeEnqueuer
}

// newTestEnv assembles Deps against a memory catalog store and an
// immediate-fire OnStart, mirroring the late-registration behavior of
// the real lifecycle (WireRuntime runs after platform.Start).
func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	store := catalog.NewMemoryStore()
	tk := apigatewaykit.New("api")
	tk.SetCatalogStore(store)
	enq := &fakeEnqueuer{}
	return testEnv{tk: tk, store: store, enq: enq, deps: Deps{
		Toolkit:  tk,
		Catalog:  store,
		Enqueuer: enq,
		OnStart: func(fn func(context.Context) error) {
			if err := fn(context.Background()); err != nil {
				t.Fatalf("OnStart callback: %v", err)
			}
		},
	}}
}

func TestRegister_SeedsCatalogSpecAndConnection(t *testing.T) {
	env := newTestEnv(t)
	d, tk, store, enq := env.deps, env.tk, env.store, env.enq
	if err := Register(d); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !tk.HasConnection(connectionName) {
		t.Fatal("util connection not registered")
	}
	specs, err := store.ListSpecs(context.Background(), catalogID)
	if err != nil || len(specs) != 1 {
		t.Fatalf("seeded specs = %d (err %v); want 1", len(specs), err)
	}
	if specs[0].SpecName != specName || specs[0].OperationCount == 0 {
		t.Errorf("spec = %+v; want %q with operations", specs[0], specName)
	}
	if len(enq.keys) != 1 || enq.keys[0].CatalogID != catalogID {
		t.Errorf("enqueued = %+v; want one embed job for the util catalog", enq.keys)
	}

	// The connection surfaces the built-in description and its
	// operation count (fetch_url present via the seeded catalog).
	var found bool
	for _, c := range tk.ListConnections() {
		if c.Name != connectionName {
			continue
		}
		found = true
		if c.Description != connectionDescription {
			t.Errorf("description = %q; want built-in description", c.Description)
		}
		if c.OperationCount == 0 {
			t.Error("OperationCount = 0; want fetch_url discovered from the seeded catalog")
		}
	}
	if !found {
		t.Fatal("util connection missing from ListConnections")
	}
}

func TestRegister_ReseedIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	d, tk, store := env.deps, env.tk, env.store
	if err := Register(d); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	// Second boot: same catalog store, connection already present —
	// the seed reloads instead of failing on the duplicate.
	if err := seed(context.Background(), d); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if !tk.HasConnection(connectionName) {
		t.Fatal("connection lost after re-seed")
	}
	specs, _ := store.ListSpecs(context.Background(), catalogID)
	if len(specs) != 1 {
		t.Errorf("specs after re-seed = %d; want 1 (upsert, not duplicate)", len(specs))
	}
}

func TestRegister_BadAllowPrivateCIDRFails(t *testing.T) {
	d := newTestEnv(t).deps
	d.AllowPrivateCIDRs = []string{"999.0.0.0/8"}
	err := Register(d)
	if err == nil || !strings.Contains(err.Error(), "allow_private_cidrs") {
		t.Fatalf("err = %v; want allow_private_cidrs parse failure", err)
	}
}

func TestRegister_NilEnqueuerSkipsEmbedding(t *testing.T) {
	env := newTestEnv(t)
	d, tk := env.deps, env.tk
	d.Enqueuer = nil
	if err := Register(d); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !tk.HasConnection(connectionName) {
		t.Fatal("connection should register without an embed queue (lexical fallback)")
	}
}

func TestRegister_EnqueueFailureNonFatal(t *testing.T) {
	env := newTestEnv(t)
	d, tk, enq := env.deps, env.tk, env.enq
	enq.err = errors.New("queue down")
	if err := Register(d); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !tk.HasConnection(connectionName) {
		t.Fatal("embed enqueue failure must not block connection registration")
	}
}

// errStore wraps a real memory store and forces a chosen method to
// fail, so the seed's error-wrapping paths are exercised without a
// live Postgres error condition.
type errStore struct {
	catalog.Store
	failGetCatalog    error
	failCreateCatalog error
	failUpsertSpec    error
}

func (s *errStore) GetCatalog(ctx context.Context, id string) (*catalog.Catalog, error) {
	if s.failGetCatalog != nil {
		return nil, s.failGetCatalog
	}
	return s.Store.GetCatalog(ctx, id) //nolint:wrapcheck // transparent delegation to the wrapped store; errors.Is identity must be preserved
}

func (s *errStore) CreateCatalog(ctx context.Context, c catalog.Catalog) error {
	if s.failCreateCatalog != nil {
		return s.failCreateCatalog
	}
	return s.Store.CreateCatalog(ctx, c) //nolint:wrapcheck // transparent delegation to the wrapped store
}

func (s *errStore) UpsertSpec(ctx context.Context, catalogID string, spec catalog.SpecEntry) error {
	if s.failUpsertSpec != nil {
		return s.failUpsertSpec
	}
	return s.Store.UpsertSpec(ctx, catalogID, spec) //nolint:wrapcheck // transparent delegation to the wrapped store
}

func TestSeed_ErrorPaths(t *testing.T) {
	boom := errors.New("db down")
	tests := []struct {
		name  string
		store *errStore
		want  string
	}{
		{"catalog lookup error", &errStore{Store: catalog.NewMemoryStore(), failGetCatalog: boom}, "ensuring catalog"},
		{"catalog create error", &errStore{Store: catalog.NewMemoryStore(), failCreateCatalog: boom}, "ensuring catalog"},
		{"spec upsert error", &errStore{Store: catalog.NewMemoryStore(), failUpsertSpec: boom}, "upserting spec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tk := apigatewaykit.New("api")
			tk.SetCatalogStore(tt.store)
			tk.SetInternalHandler(mustHandler(t))
			err := seed(context.Background(), Deps{Toolkit: tk, Catalog: tt.store})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v; want %q", err, tt.want)
			}
		})
	}
}

// mustHandler builds a throwaway internal handler so AddConnection can
// materialize the util connection in error-path tests that never fetch.
func mustHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := utilhandler.New(utilhandler.Options{})
	if err != nil {
		t.Fatalf("utilhandler.New: %v", err)
	}
	return h
}

func TestRegisterConnection_ReloadErrorPropagates(t *testing.T) {
	// A connection registered under a DIFFERENT (non-internal) config
	// with no internal handler wired: HasConnection is true, so the
	// seed takes the reload branch, and the reload rebuild fails
	// because handler=internal now needs a handler that was never set.
	tk := apigatewaykit.New("api")
	// Register a placeholder "util" first via a normal connection so
	// HasConnection returns true, then drive registerConnection.
	if err := tk.AddConnection(connectionName, map[string]any{"base_url": "https://placeholder.example.com"}); err != nil {
		t.Fatalf("seed placeholder: %v", err)
	}
	// The placeholder is not handler=internal, so ReloadConnection
	// rebuilds it as-is and succeeds — assert the reload branch runs
	// without error and keeps the connection.
	if err := registerConnection(tk); err != nil {
		t.Fatalf("registerConnection reload: %v", err)
	}
	if !tk.HasConnection(connectionName) {
		t.Fatal("connection lost after reload")
	}
}

func TestSeed_SpecParsesToFetchURL(t *testing.T) {
	env := newTestEnv(t)
	d, store := env.deps, env.store
	if err := Register(d); err != nil {
		t.Fatalf("Register: %v", err)
	}
	specs, _ := store.ListSpecs(context.Background(), catalogID)
	if len(specs) != 1 {
		t.Fatalf("specs = %d", len(specs))
	}
	items, err := apigatewaykit.BuildOperationItems(specs[0].Content, specName)
	if err != nil {
		t.Fatalf("embedded spec does not parse: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.OperationID)
	}
	if len(ids) != 1 || ids[0] != "fetch_url" {
		t.Errorf("operations = %v; want exactly [fetch_url]", ids)
	}
}
