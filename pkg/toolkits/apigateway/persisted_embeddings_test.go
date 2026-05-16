package apigateway

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// trackingEmbedder counts calls so tests can prove the provider
// was NOT invoked at connection registration time (vectors come
// from the store) and was invoked exactly once per spec write.
type trackingEmbedder struct {
	dim         int
	batchCalls  atomic.Int32
	singleCalls atomic.Int32
}

func newTrackingEmbedder() *trackingEmbedder { return &trackingEmbedder{dim: 4} }

func (e *trackingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	e.singleCalls.Add(1)
	v := make([]float32, e.dim)
	v[0] = 1
	return v, nil
}

func (e *trackingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.batchCalls.Add(1)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dim)
		out[i][0] = float32(i + 1)
	}
	return out, nil
}

func (e *trackingEmbedder) Dimension() int { return e.dim }

// persistedEmbedTestSpec is a two-operation spec used by the
// acceptance tests. Exact ops don't matter; the tests assert on
// the persistence behavior, not on operation content.
const persistedEmbedTestSpec = `openapi: 3.0.0
info:
  title: t
  version: "1"
paths:
  /a:
    get:
      operationId: alpha
      summary: Alpha
      responses:
        "200":
          description: ok
  /b:
    get:
      operationId: bravo
      summary: Bravo
      responses:
        "200":
          description: ok
`

// seedCatalogWithEmbeddings writes a catalog, a spec, and the
// pre-computed embedding rows directly to the store — mirroring
// what the admin handler does in production after the operator
// saves a spec.
func seedCatalogWithEmbeddings(t *testing.T, store catalog.Store, embedder *trackingEmbedder, catalogID, specName string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateCatalog(ctx, catalog.Catalog{
		ID: catalogID, Name: catalogID, DisplayName: catalogID,
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(ctx, catalogID, catalog.SpecEntry{
		SpecName: specName, Content: persistedEmbedTestSpec, SourceKind: catalog.SourceInline,
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	rows, err := ComputeOperationEmbeddings(ctx, embedder, persistedEmbedTestSpec, specName, nil)
	if err != nil {
		t.Fatalf("ComputeOperationEmbeddings: %v", err)
	}
	if err := store.UpsertOperationEmbeddings(ctx, catalogID, specName, rows); err != nil {
		t.Fatalf("UpsertOperationEmbeddings: %v", err)
	}
}

// TestAddParsedConnection_ReadsEmbeddingsFromStore: AC "registering
// a connection whose catalog has pre-computed vectors populates
// conn.embedVectors without any provider call". The embedder is
// wired but the test resets its batch-call counter after the
// seed write — any additional call during registration would
// fail the assertion.
func TestAddParsedConnection_ReadsEmbeddingsFromStore(t *testing.T) {
	t.Parallel()
	tk := New("test")
	emb := newTrackingEmbedder()
	tk.SetEmbeddingProvider(emb)
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	seedCatalogWithEmbeddings(t, store, emb, "shared", "default")

	// Reset counter so we can prove registration adds zero calls.
	emb.batchCalls.Store(0)
	emb.singleCalls.Store(0)

	if err := tk.AddConnection("c1", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "shared",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if got := emb.batchCalls.Load(); got != 0 {
		t.Errorf("registration must not call embedder; got %d batch calls", got)
	}
	tk.mu.RLock()
	c := tk.connections["c1"]
	tk.mu.RUnlock()
	if len(c.embedVectors) != 2 {
		t.Errorf("conn.embedVectors should hold 2 pre-loaded rows; got %d", len(c.embedVectors))
	}
}

// TestTwoConnectionsShareCatalogEmbeddings: AC "two connections
// sharing one catalog: the embedding provider is invoked once per
// operation per spec write, not per connection". Register N
// connections after one seed write; assert the embedder batch
// count is unchanged.
func TestTwoConnectionsShareCatalogEmbeddings(t *testing.T) {
	t.Parallel()
	tk := New("test")
	emb := newTrackingEmbedder()
	tk.SetEmbeddingProvider(emb)
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	seedCatalogWithEmbeddings(t, store, emb, "shared", "default")

	emb.batchCalls.Store(0)

	for _, name := range []string{"c1", "c2", "c3"} {
		if err := tk.AddConnection(name, map[string]any{
			"base_url":   "https://api.example.com",
			"catalog_id": "shared",
		}); err != nil {
			t.Fatalf("AddConnection %s: %v", name, err)
		}
	}
	if got := emb.batchCalls.Load(); got != 0 {
		t.Errorf("N connections must share vectors; got %d additional batch calls", got)
	}
}

// TestRestartPreservesEmbeddings: AC "after process restart,
// api_list_endpoints(ranking=semantic) returns semantic results
// on the first call. No warm-up Note. No fallback to lexical."
// Simulated by constructing a fresh Toolkit against the same
// store, registering a new connection, and asserting semantic
// ranking works immediately.
func TestRestartPreservesEmbeddings(t *testing.T) {
	t.Parallel()
	store := catalog.NewMemoryStore()
	emb := newFakeEmbedder(32)
	// Seed the store with vectors. Use the fake embedder so the
	// post-restart ranking can find non-degenerate cosine
	// similarities.
	ctx := context.Background()
	_ = store.CreateCatalog(ctx, catalog.Catalog{ID: "shared", Name: "s", DisplayName: "s"})
	_ = store.UpsertSpec(ctx, "shared", catalog.SpecEntry{
		SpecName: "default", Content: persistedEmbedTestSpec, SourceKind: catalog.SourceInline,
	})
	rows, err := ComputeOperationEmbeddings(ctx, emb, persistedEmbedTestSpec, "default", nil)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if err := store.UpsertOperationEmbeddings(ctx, "shared", "default", rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// "Restart": a brand-new Toolkit picks up the same store.
	tk := New("test-after-restart")
	tk.SetCatalogStore(store)
	tk.SetEmbeddingProvider(emb)
	if err := tk.AddConnection("c", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "shared",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["c"]
	tk.mu.RUnlock()
	if len(c.embedVectors) != 2 {
		t.Fatalf("post-restart conn should reload vectors; got %d", len(c.embedVectors))
	}
	// Semantic ranking on the first call should return results
	// (vectors are present) without a fallback Note.
	res, payload, _ := tk.handleListEndpoints(ctx, nil, ListEndpointsInput{
		Connection: "c", Query: "alpha", Ranking: "semantic",
	})
	if res == nil || res.IsError {
		t.Fatalf("post-restart semantic should not error: %v", res)
	}
	out, _ := payload.(ListEndpointsOutput)
	if out.Note != "" {
		t.Errorf("post-restart should have no fallback Note; got %q", out.Note)
	}
	if len(out.Operations) == 0 {
		t.Error("post-restart semantic should produce ranked operations")
	}
}

// TestRanking_LexicalFallbackOnNoVectors: AC "spec without vectors
// falls back to lexical with the correct Note". Drives the
// embeddingless-spec path with semantic mode and asserts the
// fallback message names the cause.
func TestRanking_LexicalFallbackOnNoVectors(t *testing.T) {
	t.Parallel()
	tk := New("test")
	tk.SetEmbeddingProvider(newFakeEmbedder(32))
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	ctx := context.Background()
	_ = store.CreateCatalog(ctx, catalog.Catalog{ID: "c", Name: "c", DisplayName: "c"})
	_ = store.UpsertSpec(ctx, "c", catalog.SpecEntry{
		SpecName: "default", Content: persistedEmbedTestSpec, SourceKind: catalog.SourceInline,
	})
	// No UpsertOperationEmbeddings — store has no vectors.

	if err := tk.AddConnection("api", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "c",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	res, payload, _ := tk.handleListEndpoints(ctx, nil, ListEndpointsInput{
		Connection: "api", Query: "alpha", Ranking: "semantic",
	})
	if res == nil || res.IsError {
		t.Fatalf("semantic without vectors should not error: %v", res)
	}
	out, _ := payload.(ListEndpointsOutput)
	if out.Note == "" {
		t.Fatal("semantic without vectors should produce a fallback Note")
	}
	// Note must name the not-indexed cause, not the warmer states
	// that were deleted from this version.
	if !strings.Contains(out.Note, "not indexed") {
		t.Errorf("Note should mention 'not indexed'; got %q", out.Note)
	}
}

// failingEmbeddingsStore is a wrapper around catalog.MemoryStore
// that injects an error on ListOperationEmbeddings so the toolkit's
// addParsedConnection logs and continues without halting the
// registration. Used to cover the buildConnSpecs error branch.
type failingEmbeddingsStore struct {
	*catalog.MemoryStore
	listErr error
}

func (s *failingEmbeddingsStore) ListOperationEmbeddings(ctx context.Context, catalogID, specName string) ([]catalog.OperationEmbedding, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	rows, err := s.MemoryStore.ListOperationEmbeddings(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("failingEmbeddingsStore: %w", err)
	}
	return rows, nil
}

var _ catalog.Store = (*failingEmbeddingsStore)(nil)

// TestAddParsedConnection_EmbeddingListErrorLogsAndContinues
// covers the branch where ListOperationEmbeddings fails. The
// connection should still register (with empty vectors for that
// spec) — a flaky read should not prevent the model from seeing
// the connection's operations.
func TestAddParsedConnection_EmbeddingListErrorLogsAndContinues(t *testing.T) {
	t.Parallel()
	tk := New("test")
	tk.SetEmbeddingProvider(newFakeEmbedder(32))
	mem := catalog.NewMemoryStore()
	store := &failingEmbeddingsStore{MemoryStore: mem}
	tk.SetCatalogStore(store)
	ctx := context.Background()
	_ = mem.CreateCatalog(ctx, catalog.Catalog{ID: "c", Name: "c", DisplayName: "c"})
	_ = mem.UpsertSpec(ctx, "c", catalog.SpecEntry{
		SpecName: "default", Content: persistedEmbedTestSpec, SourceKind: catalog.SourceInline,
	})

	// Trip the error path.
	store.listErr = errLOEFailure

	if err := tk.AddConnection("api", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "c",
	}); err != nil {
		t.Fatalf("AddConnection should not error on embed-list failure; got %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["api"]
	tk.mu.RUnlock()
	if c == nil {
		t.Fatal("connection should have registered")
	}
	if len(c.embedVectors) != 0 {
		t.Errorf("expected empty embedVectors on list error; got %d", len(c.embedVectors))
	}
}

var errLOEFailure error = &simpleError{msg: "forced list-embeddings failure"}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// noOperationIDSpec exercises the buildOperationIndex path that
// synthesizes operationIds from method+path. Required to cover the
// previously-broken case where spec-write time used basePath="" and
// connection-registration time used effectiveBasePath, so the two
// synthesized ids did not match and semantic ranking silently fell
// back to score=0 for every op.
const noOperationIDSpec = `openapi: 3.0.0
info:
  title: t
  version: "1"
paths:
  /widgets:
    get:
      summary: List widgets
      responses:
        "200":
          description: ok
`

// TestAddParsedConnection_VectorLookupSurvivesBasePathOverride
// proves the embedding lookup works for operations without an
// explicit operationId even when the connection's spec has a
// non-empty base_path. Pre-fix, the synthesized operation_id at
// compute time was "GET /widgets" (basePath="") while at runtime
// it was "GET /v2/widgets" (effectiveBasePath="/v2"), so the
// lookup missed and the op fell out of semantic ranking. This
// test fails on the pre-fix code path because c.embedVectors
// keys on the runtime id.
func TestAddParsedConnection_VectorLookupSurvivesBasePathOverride(t *testing.T) {
	t.Parallel()
	tk := New("test")
	emb := newFakeEmbedder(32)
	tk.SetEmbeddingProvider(emb)
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	ctx := context.Background()
	_ = store.CreateCatalog(ctx, catalog.Catalog{ID: "c", Name: "c", DisplayName: "c"})
	_ = store.UpsertSpec(ctx, "c", catalog.SpecEntry{
		SpecName: "default", Content: noOperationIDSpec, SourceKind: catalog.SourceInline,
		BasePath: "/v2", // operator override, non-empty
	})
	rows, err := ComputeOperationEmbeddings(ctx, emb, noOperationIDSpec, "default", nil)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if err := store.UpsertOperationEmbeddings(ctx, "c", "default", rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := tk.AddConnection("api", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "c",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	tk.mu.RLock()
	c := tk.connections["api"]
	tk.mu.RUnlock()

	// Verify every operation's lookup key resolves to a stored
	// vector — the bug surfaces as a missing key in embedVectors.
	for _, op := range c.operations {
		if _, ok := c.embedVectors[embedKey{Spec: op.Spec, OperationID: op.OperationID}]; !ok {
			t.Errorf("op %s (spec=%s) has no embedding vector — basePath caused storage/lookup id mismatch",
				op.OperationID, op.Spec)
		}
	}
}
