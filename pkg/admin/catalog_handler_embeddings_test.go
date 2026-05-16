package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// catalogEmbeddingSpec is a minimal OpenAPI 3.0 document the
// embedding tests upsert at PUT /specs/{spec}. The exact content
// doesn't matter — only that buildOperationIndex parses ≥1
// operation so the admin handler's compute path has something to
// embed and persist.
const catalogEmbeddingSpec = `openapi: 3.0.0
info:
  title: t
  version: "1"
paths:
  /users:
    get:
      operationId: list-users
      summary: List users
      responses:
        "200":
          description: ok
  /orders:
    post:
      operationId: create-order
      summary: Create an order
      responses:
        "200":
          description: ok
`

// countingEmbedder tracks how many EmbedBatch / Embed calls it
// served, so tests can assert "the provider was called N times
// and never again" across operations like clone or re-register.
type countingEmbedder struct {
	dim         int
	batchCalls  atomic.Int32
	singleCalls atomic.Int32
}

func newCountingEmbedder() *countingEmbedder { return &countingEmbedder{dim: 4} }

func (e *countingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	e.singleCalls.Add(1)
	out := make([]float32, e.dim)
	out[0] = 1
	return out, nil
}

func (e *countingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.batchCalls.Add(1)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, e.dim)
		out[i][0] = float32(i + 1)
	}
	return out, nil
}

func (e *countingEmbedder) Dimension() int { return e.dim }

// newCatalogEmbedTestHandler wires a memory store, a counting
// embedder, and turns the admin handler into mutable mode so the
// PUT /specs routes are reachable. The handler is what the
// acceptance tests below drive.
func newCatalogEmbedTestHandler(t *testing.T) (*Handler, *apicatalog.MemoryStore, *countingEmbedder) {
	t.Helper()
	store := apicatalog.NewMemoryStore()
	emb := newCountingEmbedder()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          emb,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	if err := store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "petstore", Name: "petstore", DisplayName: "Petstore",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	return h, store, emb
}

// TestCatalogSpecUpsert_PersistsEmbeddings: AC "spec upsert with
// embedding provider configured produces len(operations) rows in
// the embedding table". Drives the admin PUT and asserts the
// resulting embedding rows match the operations parsed from the
// spec.
func TestCatalogSpecUpsert_PersistsEmbeddings(t *testing.T) {
	t.Parallel()
	h, store, emb := newCatalogEmbedTestHandler(t)

	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert spec: %d %s", res.Code, res.Body.String())
	}

	rows, err := store.ListOperationEmbeddings(context.Background(), "petstore", "default")
	if err != nil {
		t.Fatalf("ListOperationEmbeddings: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 embedding rows (one per op); got %d", len(rows))
	}
	if emb.batchCalls.Load() == 0 {
		t.Error("provider EmbedBatch should have been called for fresh rows")
	}
	// EmbeddingCount on the response must report the persisted count.
	var sr specResponse
	_ = json.Unmarshal(res.Body.Bytes(), &sr)
	if sr.EmbeddingCount != 2 {
		t.Errorf("response embedding_count=%d, want 2", sr.EmbeddingCount)
	}
}

// TestCatalogSpecUpsert_NoEmbedderSucceedsWithoutVectors: AC
// "spec upsert without embedding provider succeeds; embedding
// table has no rows". Drives the admin PUT against a handler
// configured with no embedder.
func TestCatalogSpecUpsert_NoEmbedderSucceedsWithoutVectors(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert spec: %d %s", res.Code, res.Body.String())
	}
	rows, _ := store.ListOperationEmbeddings(context.Background(), "p", "default")
	if len(rows) != 0 {
		t.Errorf("no embedder configured: expected 0 rows; got %d", len(rows))
	}
}

// TestCatalogSpecUpsert_DedupsOnUnchangedTextHash: refresh that
// reuses the same operation text must reuse the existing
// embedding row's vector without calling the provider again.
func TestCatalogSpecUpsert_DedupsOnUnchangedTextHash(t *testing.T) {
	t.Parallel()
	h, store, emb := newCatalogEmbedTestHandler(t)
	body := map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	}
	if res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", body); res.Code != http.StatusOK {
		t.Fatalf("first upsert: %d %s", res.Code, res.Body.String())
	}
	firstBatchCalls := emb.batchCalls.Load()
	// Second upsert with identical content — every operation
	// hashes to the same value as the existing row, so no fresh
	// embed pass should run.
	if res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", body); res.Code != http.StatusOK {
		t.Fatalf("second upsert: %d %s", res.Code, res.Body.String())
	}
	if got := emb.batchCalls.Load(); got != firstBatchCalls {
		t.Errorf("dedup failed: batch calls went from %d to %d on identical content", firstBatchCalls, got)
	}
	// Rows should still be present after the second write.
	rows, _ := store.ListOperationEmbeddings(context.Background(), "petstore", "default")
	if len(rows) != 2 {
		t.Errorf("rows after second write: got %d, want 2", len(rows))
	}
}

// TestCatalogSpecDelete_CascadesEmbeddings: deleting a spec must
// also drop its embedding rows. Mirrors the FK ON DELETE CASCADE
// in migration 000044 at the in-memory level.
func TestCatalogSpecDelete_CascadesEmbeddings(t *testing.T) {
	t.Parallel()
	h, store, _ := newCatalogEmbedTestHandler(t)
	if res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	}); res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
	if res := doJSON(t, h, http.MethodDelete, "/api/v1/admin/api-catalogs/petstore/specs/default", nil); res.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", res.Code, res.Body.String())
	}
	rows, _ := store.ListOperationEmbeddings(context.Background(), "petstore", "default")
	if len(rows) != 0 {
		t.Errorf("spec deletion must cascade to embeddings; got %d rows", len(rows))
	}
}

// TestCatalogReembedEndpoint_RecomputesVectors: the admin reembed
// endpoint wipes and recomputes the named spec's vectors. Used
// when a spec was written before an embedder was configured.
func TestCatalogReembedEndpoint_RecomputesVectors(t *testing.T) {
	t.Parallel()
	// First scenario: spec was written WITHOUT an embedder; reembed
	// fills in vectors.
	store := apicatalog.NewMemoryStore()
	emb := newCountingEmbedder()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          emb,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	// Seed a spec directly to simulate the "no-embedder when first
	// written" history.
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	if rows, _ := store.ListOperationEmbeddings(context.Background(), "p", "default"); len(rows) != 0 {
		t.Fatalf("precondition: spec should have 0 vectors before reembed; got %d", len(rows))
	}
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/p/specs/default/reembed", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("reembed: %d %s", res.Code, res.Body.String())
	}
	rows, _ := store.ListOperationEmbeddings(context.Background(), "p", "default")
	if len(rows) != 2 {
		t.Errorf("reembed must produce one row per operation; got %d", len(rows))
	}
}

// TestCatalogReembedEndpoint_NoEmbedderReturnsServiceUnavailable
// proves the reembed endpoint refuses with 503 when no embedder
// is wired, instead of silently writing zero rows. Operators need
// a clear "wire an embedder first" signal.
func TestCatalogReembedEndpoint_NoEmbedderReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/p/specs/default/reembed", nil)
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("reembed without embedder: got %d, want 503", res.Code)
	}
}

// TestCatalogReembedEndpoint_NotFoundForMissingSpec ensures the
// reembed admin path returns a clean 404 rather than 500 when the
// operator names a spec that does not exist.
func TestCatalogReembedEndpoint_NotFoundForMissingSpec(t *testing.T) {
	t.Parallel()
	h, _, _ := newCatalogEmbedTestHandler(t)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/petstore/specs/missing/reembed", nil)
	if res.Code != http.StatusNotFound {
		t.Errorf("reembed missing spec: got %d, want 404", res.Code)
	}
}

// TestComputeOperationEmbeddings_NilEmbedderReturnsNil documents
// the nil-embedder contract: the function quietly returns nil so
// admin write paths don't have to guard on the embedder before
// calling it.
func TestComputeOperationEmbeddings_NilEmbedderReturnsNil(t *testing.T) {
	t.Parallel()
	// computeAndStoreEmbeddings should be a no-op when embedder is
	// nil. Verified through the handler: write a spec and confirm
	// no rows appear (covered above), and also call the helper
	// directly to assert no error path.
	store := apicatalog.NewMemoryStore()
	h := &Handler{deps: Deps{APICatalogStore: store, Embedder: nil}}
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "d", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	h.computeAndStoreEmbeddings(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "d", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	rows, _ := store.ListOperationEmbeddings(context.Background(), "p", "d")
	if len(rows) != 0 {
		t.Errorf("nil embedder must not produce rows; got %d", len(rows))
	}
}

// verify embedding.Provider import is consumed (lints reject
// unused-but-imported when this file is the only consumer).
var _ embedding.Provider = (*countingEmbedder)(nil)

// errFailingEmbedder is a deterministic-failure embedder used to
// prove that an embed compute failure does NOT poison the spec
// write itself: the spec row still saves; only the vector rows
// are missing.
var errFailingEmbedder = errors.New("forced failure")

type failingEmbedder struct{}

func (failingEmbedder) Dimension() int { return 4 }
func (failingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, errFailingEmbedder
}

func (failingEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, errFailingEmbedder
}

// TestCatalogSpecUpsert_EmbedderFailureDoesNotPoisonWrite: a
// failing provider must not block the spec write. Operators can
// still save spec content and retry vectors via the reembed
// endpoint.
func TestCatalogSpecUpsert_EmbedderFailureDoesNotPoisonWrite(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          failingEmbedder{},
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
	spec, err := store.GetSpec(context.Background(), "p", "default")
	if err != nil || spec == nil {
		t.Fatalf("spec should persist despite embed failure; err=%v spec=%v", err, spec)
	}
	rows, _ := store.ListOperationEmbeddings(context.Background(), "p", "default")
	if len(rows) != 0 {
		t.Errorf("embed failure must leave vectors empty; got %d rows", len(rows))
	}
}

// TestCatalogReembedEndpoint_DeleteFailureReturnsServerError covers
// the path where the underlying DeleteOperationEmbeddings call
// rejects. Drives an errorCatalogStore through the reembed handler
// to surface the 500 cleanly.
func TestCatalogReembedEndpoint_DeleteFailureReturnsServerError(t *testing.T) {
	t.Parallel()
	store := &reembedErrorStore{
		MemoryStore: apicatalog.NewMemoryStore(),
		delErr:      errors.New("delete failed"),
	}
	_ = store.MemoryStore.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.MemoryStore.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          newCountingEmbedder(),
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/p/specs/default/reembed", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestCatalogReembedEndpoint_UpsertFailureReturnsServerError covers
// the persist-step failure path on the reembed handler.
func TestCatalogReembedEndpoint_UpsertFailureReturnsServerError(t *testing.T) {
	t.Parallel()
	store := &reembedErrorStore{
		MemoryStore: apicatalog.NewMemoryStore(),
		upsertErr:   errors.New("upsert failed"),
	}
	_ = store.MemoryStore.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.MemoryStore.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          newCountingEmbedder(),
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/p/specs/default/reembed", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestCatalogReembedEndpoint_ComputeFailureReturnsBadGateway covers
// the path where the embedding provider itself errors during
// reembed — surfaced as a 502 so the operator can distinguish
// "your provider is down" from "the platform is broken".
func TestCatalogReembedEndpoint_ComputeFailureReturnsBadGateway(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	_ = store.UpsertSpec(context.Background(), "p", apicatalog.SpecEntry{
		SpecName: "default", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          failingEmbedder{},
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/p/specs/default/reembed", nil)
	if res.Code != http.StatusBadGateway {
		t.Errorf("got %d, want 502", res.Code)
	}
}

// TestCatalogReembedEndpoint_GetSpecStoreErrorReturns500 covers the
// path where GetSpec fails with a non-NotFound error.
func TestCatalogReembedEndpoint_GetSpecStoreErrorReturns500(t *testing.T) {
	t.Parallel()
	store := &reembedErrorStore{
		MemoryStore: apicatalog.NewMemoryStore(),
		getErr:      errors.New("db boom"),
	}
	_ = store.MemoryStore.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	h := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          newCountingEmbedder(),
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/p/specs/default/reembed", nil)
	if res.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", res.Code)
	}
}

// TestCatalogClone_CopiesEmbeddings: cloning a catalog should copy
// the embedding rows alongside the spec rows so the cloned catalog
// answers semantic ranking on first call.
func TestCatalogClone_CopiesEmbeddings(t *testing.T) {
	t.Parallel()
	h, store, _ := newCatalogEmbedTestHandler(t)
	res := doJSON(t, h, http.MethodPut, "/api/v1/admin/api-catalogs/petstore/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", res.Code, res.Body.String())
	}
	if rows, _ := store.ListOperationEmbeddings(context.Background(), "petstore", "default"); len(rows) != 2 {
		t.Fatalf("precondition: petstore should have 2 embedded rows; got %d", len(rows))
	}
	res = doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs/petstore/clone", map[string]any{
		"id": "petstore-clone", "name": "petstore-clone",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("clone: %d %s", res.Code, res.Body.String())
	}
	rows, _ := store.ListOperationEmbeddings(context.Background(), "petstore-clone", "default")
	if len(rows) != 2 {
		t.Errorf("clone should carry over 2 embedding rows; got %d", len(rows))
	}
}

// TestComputeAndStoreEmbeddings_ZeroOpsCleansStaleRows: a spec
// edit that removes every operation must drop the corresponding
// stale embedding rows so ranking does not surface orphan vectors.
func TestComputeAndStoreEmbeddings_ZeroOpsCleansStaleRows(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	emb := newCountingEmbedder()
	h := &Handler{deps: Deps{APICatalogStore: store, Embedder: emb}}
	ctx := context.Background()
	_ = store.CreateCatalog(ctx, apicatalog.Catalog{ID: "p", Name: "p", DisplayName: "P"})
	_ = store.UpsertSpec(ctx, "p", apicatalog.SpecEntry{
		SpecName: "d", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	h.computeAndStoreEmbeddings(ctx, "p", apicatalog.SpecEntry{
		SpecName: "d", Content: catalogEmbeddingSpec, SourceKind: apicatalog.SourceInline,
	})
	if rows, _ := store.ListOperationEmbeddings(ctx, "p", "d"); len(rows) != 2 {
		t.Fatalf("precondition: expected 2 rows; got %d", len(rows))
	}
	// Now compute with an empty spec (no operations) — the rows
	// should be cleaned up.
	emptySpec := `openapi: 3.0.0
info: {title: t, version: "1"}
paths: {}`
	h.computeAndStoreEmbeddings(ctx, "p", apicatalog.SpecEntry{
		SpecName: "d", Content: emptySpec, SourceKind: apicatalog.SourceInline,
	})
	if rows, _ := store.ListOperationEmbeddings(ctx, "p", "d"); len(rows) != 0 {
		t.Errorf("zero-ops spec should drop stale embedding rows; got %d", len(rows))
	}
}

// reembedErrorStore is an APICatalogStore that selectively fails
// specific methods so the reembed handler's error branches can be
// exercised against a real-enough backend.
type reembedErrorStore struct {
	*apicatalog.MemoryStore
	getErr    error
	delErr    error
	upsertErr error
}

func (s *reembedErrorStore) GetSpec(ctx context.Context, catalogID, specName string) (*apicatalog.SpecEntry, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.MemoryStore.GetSpec(ctx, catalogID, specName)
}

func (s *reembedErrorStore) DeleteOperationEmbeddings(ctx context.Context, catalogID, specName string) error {
	if s.delErr != nil {
		return s.delErr
	}
	return s.MemoryStore.DeleteOperationEmbeddings(ctx, catalogID, specName)
}

func (s *reembedErrorStore) UpsertOperationEmbeddings(ctx context.Context, catalogID, specName string, rows []apicatalog.OperationEmbedding) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	return s.MemoryStore.UpsertOperationEmbeddings(ctx, catalogID, specName, rows)
}

var _ APICatalogStore = (*reembedErrorStore)(nil)

// modelNamedEmbedder is a countingEmbedder that also reports a
// configured model name via the Model() optional interface.
// Drives the model-swap dedup test.
type modelNamedEmbedder struct {
	countingEmbedder
	modelName string
}

func (e *modelNamedEmbedder) Model() string { return e.modelName }

// TestCatalogSpecUpsert_ModelSwapForcesReembed proves the dedup
// predicate honors model identity. Persisting a spec under model A,
// then re-upserting the same content under model B (same vector
// dimension) must re-embed every op — the cached vectors don't
// represent model B's output, even though the text hash is
// unchanged.
func TestCatalogSpecUpsert_ModelSwapForcesReembed(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	_ = store.CreateCatalog(context.Background(), apicatalog.Catalog{
		ID: "p", Name: "p", DisplayName: "P",
	})
	// First write under "model-a".
	embA := &modelNamedEmbedder{modelName: "model-a"}
	embA.dim = 4
	hA := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          embA,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	res := doJSON(t, hA, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("first upsert: %d %s", res.Code, res.Body.String())
	}
	if embA.batchCalls.Load() == 0 {
		t.Fatal("precondition: first upsert should call embedder")
	}

	// Second write under "model-b". Same content, same dimension —
	// pre-fix this would reuse vectors and stamp model-b on rows
	// that hold model-a's output. The fix forces a re-embed.
	embB := &modelNamedEmbedder{modelName: "model-b"}
	embB.dim = 4
	hB := NewHandler(Deps{
		APICatalogStore:   store,
		Embedder:          embB,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)
	res = doJSON(t, hB, http.MethodPut, "/api/v1/admin/api-catalogs/p/specs/default", map[string]any{
		"source_kind": "inline",
		"content":     catalogEmbeddingSpec,
	})
	if res.Code != http.StatusOK {
		t.Fatalf("second upsert: %d %s", res.Code, res.Body.String())
	}
	if embB.batchCalls.Load() == 0 {
		t.Error("model swap must trigger re-embed; batch call count was 0")
	}
	rows, _ := store.ListOperationEmbeddings(context.Background(), "p", "default")
	for _, r := range rows {
		if r.Model != "model-b" {
			t.Errorf("row %s stamped model=%s; want model-b after swap", r.OperationID, r.Model)
		}
	}
}
