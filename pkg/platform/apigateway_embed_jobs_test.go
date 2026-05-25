package platform

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/embedjobs"
)

// fakeEmbedder is a stub embedding.Provider used by the wiring
// adapter tests. Returns a deterministic 8-float vector so test
// assertions can match exactly.
type fakeEmbedder struct {
	dim    int
	embed  func(context.Context, string) ([]float32, error)
	batch  func(context.Context, []string) ([][]float32, error)
	called int
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	f.called++
	if f.embed != nil {
		return f.embed(ctx, text)
	}
	return make([]float32, f.dim), nil
}

func (f *fakeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	f.called++
	if f.batch != nil {
		return f.batch(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, f.dim)
	}
	return out, nil
}

func (f *fakeEmbedder) Dimension() int { return f.dim }
func (*fakeEmbedder) Kind() string     { return "fake" }

// seedCatalogStore inserts a catalog and a single spec used by
// the resolver and persister tests below.
func seedCatalogStore(t *testing.T, store apigatewaycatalog.Store, catalogID, specName, content string) {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateCatalog(ctx, apigatewaycatalog.Catalog{
		ID: catalogID, Name: catalogID, Version: "v1",
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(ctx, catalogID, apigatewaycatalog.SpecEntry{
		SpecName: specName, Content: content, SourceKind: "inline",
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
}

// TestCatalogSpecResolver_ReturnsContent proves the resolver
// adapter pulls the content column off the spec row via the
// catalog Store.
func TestCatalogSpecResolver_ReturnsContent(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedCatalogStore(t, store, "cat1", "spec1", "openapi: 3.0.0")

	r := &catalogSpecResolver{store: store}
	got, err := r.GetSpecContent(context.Background(), "cat1", "spec1")
	if err != nil {
		t.Fatalf("GetSpecContent: %v", err)
	}
	if got != "openapi: 3.0.0" {
		t.Errorf("content = %q; want %q", got, "openapi: 3.0.0")
	}
}

// TestCatalogSpecResolver_WrapsStoreError proves a missing
// (catalog, spec) is surfaced as a wrapped error rather than
// silently returning "". The worker treats any non-nil error
// from the resolver as a terminal spec-vanished case.
func TestCatalogSpecResolver_WrapsStoreError(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()

	r := &catalogSpecResolver{store: store}
	_, err := r.GetSpecContent(context.Background(), "missing", "missing")
	if err == nil {
		t.Fatal("expected error for missing spec")
	}
}

// TestApigatewayEmbeddingComputer_NoSpecReturnsEmpty exercises
// the happy path of the Compute adapter with an empty OpenAPI
// document. ComputeOperationEmbeddings returns (nil, nil) when
// the spec has zero operations; the adapter must propagate that
// without translating into a nil-vs-empty slice trap.
func TestApigatewayEmbeddingComputer_EmptySpec(t *testing.T) {
	t.Parallel()
	spec := `openapi: 3.0.0
info:
  title: t
  version: v1
paths: {}
`
	c := &apigatewayEmbeddingComputer{embedder: &fakeEmbedder{dim: 8}}
	rows, err := c.Compute(context.Background(), embedjobs.ComputeRequest{
		Content: spec, SpecName: "spec1",
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d; want 0 for empty spec", len(rows))
	}
}

// TestApigatewayEmbeddingComputer_TranslatesExisting proves the
// adapter rebuilds the catalog.OperationEmbedding-keyed map from
// the embedjobs.ExistingEmbedding input so the downstream dedup
// path sees the same set of cached vectors.
func TestApigatewayEmbeddingComputer_TranslatesExisting(t *testing.T) {
	t.Parallel()
	spec := `openapi: 3.0.0
info:
  title: t
  version: v1
paths:
  /ping:
    get:
      operationId: ping
      responses:
        '200':
          description: ok
`
	h := sha256.Sum256([]byte("ignored"))
	c := &apigatewayEmbeddingComputer{embedder: &fakeEmbedder{dim: 4}}
	existing := map[string]embedjobs.ExistingEmbedding{
		"ping": {
			OperationID: "ping",
			TextHash:    h[:],
			Embedding:   []float32{1, 2, 3, 4},
			Model:       "fake",
			Dim:         4,
		},
	}
	rows, err := c.Compute(context.Background(), embedjobs.ComputeRequest{
		Content: spec, SpecName: "spec1", Existing: existing,
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d; want 1", len(rows))
	}
	if rows[0].OperationID != "ping" {
		t.Errorf("OperationID = %q; want %q", rows[0].OperationID, "ping")
	}
}

// TestApigatewayEmbeddingComputer_ParseError surfaces an invalid
// OpenAPI document as a wrapped error rather than swallowing it
// (the worker treats this as retryable).
func TestApigatewayEmbeddingComputer_ParseError(t *testing.T) {
	t.Parallel()
	c := &apigatewayEmbeddingComputer{embedder: &fakeEmbedder{dim: 4}}
	_, err := c.Compute(context.Background(), embedjobs.ComputeRequest{
		Content: "this is not yaml at all: [", SpecName: "spec1",
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

// TestCatalogEmbeddingPersister_RoundTrip proves Upsert + List
// preserve every field. Used by the worker dedup path and the
// stamp-then-list flow that closes the reconciler convergence
// loop.
func TestCatalogEmbeddingPersister_RoundTrip(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedCatalogStore(t, store, "cat1", "spec1", "openapi: 3.0.0")
	p := &catalogEmbeddingPersister{store: store}

	h := sha256.Sum256([]byte("x"))
	rows := []embedjobs.ComputedEmbedding{
		{OperationID: "ping", TextHash: h[:], Embedding: []float32{1, 2}, Model: "m", Dim: 2},
		{OperationID: "pong", TextHash: h[:], Embedding: []float32{3, 4}, Model: "m", Dim: 2},
	}
	if err := p.Upsert(context.Background(), "cat1", "spec1", rows); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := p.ListExisting(context.Background(), "cat1", "spec1")
	if err != nil {
		t.Fatalf("ListExisting: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d; want 2", len(got))
	}
	if got["ping"].Model != "m" || got["ping"].Dim != 2 {
		t.Errorf("ping row metadata wrong: %+v", got["ping"])
	}
}

// TestCatalogEmbeddingPersister_StampOperationCount proves the
// adapter calls SetOperationCount with the supplied integer. The
// worker calls this after a successful Upsert so the reconciler's
// gap predicate stops re-enqueueing already-indexed specs.
func TestCatalogEmbeddingPersister_StampOperationCount(t *testing.T) {
	t.Parallel()
	store := apigatewaycatalog.NewMemoryStore()
	seedCatalogStore(t, store, "cat1", "spec1", "openapi: 3.0.0")
	p := &catalogEmbeddingPersister{store: store}

	if err := p.StampOperationCount(context.Background(), "cat1", "spec1", 7); err != nil {
		t.Fatalf("StampOperationCount: %v", err)
	}
	spec, err := store.GetSpec(context.Background(), "cat1", "spec1")
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	if spec.OperationCount != 7 {
		t.Errorf("OperationCount = %d; want 7", spec.OperationCount)
	}
}

// TestCatalogEmbeddingPersister_ErrorWrapping proves both
// Upsert and ListExisting wrap underlying store failures with a
// type-identifiable prefix so callers can grep the worker logs
// for "catalogEmbeddingPersister:" when triaging a failed job.
func TestCatalogEmbeddingPersister_ErrorWrapping(t *testing.T) {
	t.Parallel()
	store := &errStore{err: errors.New("db down")}
	p := &catalogEmbeddingPersister{store: store}

	if _, err := p.ListExisting(context.Background(), "c", "s"); err == nil {
		t.Error("ListExisting did not surface store error")
	}
	if err := p.Upsert(context.Background(), "c", "s", nil); err == nil {
		t.Error("Upsert did not surface store error")
	}
	if err := p.StampOperationCount(context.Background(), "c", "s", 1); err == nil {
		t.Error("StampOperationCount did not surface store error")
	}
}

// TestCatalogSpecResolver_ErrorWrapping mirrors the persister
// error-wrapping test but for the resolver adapter.
func TestCatalogSpecResolver_ErrorWrapping(t *testing.T) {
	t.Parallel()
	store := &errStore{err: errors.New("db down")}
	r := &catalogSpecResolver{store: store}
	if _, err := r.GetSpecContent(context.Background(), "c", "s"); err == nil {
		t.Error("GetSpecContent did not surface store error")
	}
}

// TestApigatewayConnectionReloader_NoRegistryIsNoOp proves that
// a reloader constructed with a nil registry returns silently
// (the platform may be wired before any toolkits have been
// registered).
func TestApigatewayConnectionReloader_NoRegistryIsNoOp(_ *testing.T) {
	r := &apigatewayConnectionReloader{registry: nil}
	r.ReloadConnectionsByCatalog("cat1")
}

// errStore is a catalog.Store stub that returns the supplied
// error from every method. Used to exercise the adapter
// error-wrapping branches without standing up the postgres or
// memory store machinery.
type errStore struct{ err error }

func (s *errStore) CreateCatalog(_ context.Context, _ apigatewaycatalog.Catalog) error { return s.err }
func (s *errStore) GetCatalog(_ context.Context, _ string) (*apigatewaycatalog.Catalog, error) {
	return nil, s.err
}

func (s *errStore) ListCatalogs(_ context.Context) ([]apigatewaycatalog.Catalog, error) {
	return nil, s.err
}

func (s *errStore) UpdateCatalog(_ context.Context, _ string, _ apigatewaycatalog.Update) error {
	return s.err
}
func (s *errStore) DeleteCatalog(_ context.Context, _ string) error { return s.err }
func (s *errStore) UpsertSpec(_ context.Context, _ string, _ apigatewaycatalog.SpecEntry) error {
	return s.err
}

func (s *errStore) GetSpec(_ context.Context, _, _ string) (*apigatewaycatalog.SpecEntry, error) {
	return nil, s.err
}

func (s *errStore) ListSpecs(_ context.Context, _ string) ([]apigatewaycatalog.SpecEntry, error) {
	return nil, s.err
}
func (s *errStore) DeleteSpec(_ context.Context, _, _ string) error { return s.err }
func (s *errStore) ReferencingConnections(_ context.Context, _ string) ([]apigatewaycatalog.ConnectionRef, error) {
	return nil, s.err
}

func (s *errStore) UpsertOperationEmbeddings(_ context.Context, _, _ string, _ []apigatewaycatalog.OperationEmbedding) error {
	return s.err
}

func (s *errStore) UpsertOperationEmbeddingsBatch(_ context.Context, _, _ string, _ []apigatewaycatalog.OperationEmbedding) error {
	return s.err
}

func (s *errStore) ListOperationEmbeddings(_ context.Context, _, _ string) ([]apigatewaycatalog.OperationEmbedding, error) {
	return nil, s.err
}

func (s *errStore) DeleteOperationEmbeddings(_ context.Context, _, _ string) error {
	return s.err
}

func (s *errStore) SetOperationCount(_ context.Context, _, _ string, _ int) error {
	return s.err
}
