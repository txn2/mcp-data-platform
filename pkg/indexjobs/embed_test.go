package indexjobs

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeEmbedder is a deterministic embedding.Provider for the embed-
// loop tests. Hooks let individual tests inject batch failures or
// count mismatches.
type fakeEmbedder struct {
	dim        int
	batchCalls atomic.Int32
	failBatch  atomic.Bool
	returnN    int // when > 0, EmbedBatch returns this many vectors regardless of input
}

// fakeDim is the fixed dimensionality the test embedder produces.
const fakeDim = 8

func newFakeEmbedder() *fakeEmbedder { return &fakeEmbedder{dim: fakeDim} }

func (e *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, e.dim)
	v[0] = 1
	return v, nil
}

func (e *fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.batchCalls.Add(1)
	if e.failBatch.Load() {
		return nil, errors.New("forced batch failure")
	}
	n := len(texts)
	if e.returnN > 0 {
		n = e.returnN
	}
	out := make([][]float32, n)
	for i := range out {
		out[i] = make([]float32, e.dim)
		out[i][0] = float32(i + 1)
	}
	return out, nil
}

func (e *fakeEmbedder) Dimension() int { return e.dim }
func (*fakeEmbedder) Kind() string     { return "fake" }

// modelEmbedder adds a Model() method so providerModel resolves a
// non-empty model name.
type modelEmbedder struct {
	*fakeEmbedder
	name string
}

func (m modelEmbedder) Model() string { return m.name }

func twoItems() []Item {
	return []Item{{ItemID: "a", Text: "alpha"}, {ItemID: "b", Text: "bravo"}}
}

func TestEmbedItems_NilEmbedderReturnsNil(t *testing.T) {
	t.Parallel()
	rows, err := embedItems(context.Background(), embedRequest{embedder: nil, items: twoItems()})
	if err != nil {
		t.Fatalf("nil embedder should not error; got %v", err)
	}
	if rows != nil {
		t.Errorf("nil embedder should return nil; got %d rows", len(rows))
	}
}

func TestEmbedItems_EmptyItemsReturnsNil(t *testing.T) {
	t.Parallel()
	rows, err := embedItems(context.Background(), embedRequest{embedder: newFakeEmbedder(), items: nil})
	if err != nil {
		t.Fatalf("empty items should not error; got %v", err)
	}
	if rows != nil {
		t.Errorf("empty items should return nil; got %d rows", len(rows))
	}
}

func TestEmbedItems_EmbedsAllFresh(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	rows, err := embedItems(context.Background(), embedRequest{embedder: emb, items: twoItems()})
	if err != nil {
		t.Fatalf("embedItems: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	for _, r := range rows {
		if len(r.Embedding) != 8 {
			t.Errorf("row %q embedding dim = %d; want 8", r.ItemID, len(r.Embedding))
		}
		if len(r.TextHash) == 0 {
			t.Errorf("row %q missing text hash", r.ItemID)
		}
		if r.Dim != 8 {
			t.Errorf("row %q dim = %d; want 8", r.ItemID, r.Dim)
		}
	}
}

func TestEmbedItems_BatchErrorPropagates(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	emb.failBatch.Store(true)
	_, err := embedItems(context.Background(), embedRequest{embedder: emb, items: twoItems()})
	if err == nil {
		t.Fatal("expected error from failing batch")
	}
	if !strings.Contains(err.Error(), "embed item batch") {
		t.Errorf("error should name embed item batch; got %q", err)
	}
}

func TestEmbedItems_CountMismatchPropagates(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	emb.returnN = 1 // returns 1 vector for 2 texts
	_, err := embedItems(context.Background(), embedRequest{embedder: emb, items: twoItems(), batchSize: 2})
	if err == nil {
		t.Fatal("expected count-mismatch error")
	}
	if !strings.Contains(err.Error(), "returned") || !strings.Contains(err.Error(), "vectors") {
		t.Errorf("error should name the mismatch; got %q", err)
	}
}

func TestEmbedItems_ProgressReportsReusedThenChunks(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	var calls []int
	rows, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: twoItems(), batchSize: 1,
		progress: func(n int) { calls = append(calls, n) },
	})
	if err != nil {
		t.Fatalf("embedItems: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	// initial reused publish (0) + one per chunk (1, 2).
	if len(calls) < 2 {
		t.Fatalf("progress called %d times; want >= 2: %v", len(calls), calls)
	}
	if calls[0] != 0 {
		t.Errorf("first progress = %d; want 0 (nothing reused)", calls[0])
	}
	if calls[len(calls)-1] != 2 {
		t.Errorf("final progress = %d; want 2", calls[len(calls)-1])
	}
}

func TestEmbedItems_PersistBatchInvokedPerChunk(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	var batches int
	_, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: twoItems(), batchSize: 1,
		persistBatch: func([]Vector) error { batches++; return nil },
	})
	if err != nil {
		t.Fatalf("embedItems: %v", err)
	}
	if batches != 2 {
		t.Errorf("persistBatch calls = %d; want 2 (one per chunk)", batches)
	}
}

func TestEmbedItems_PersistBatchErrorPropagates(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder()
	_, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: twoItems(), batchSize: 1,
		persistBatch: func([]Vector) error { return errors.New("disk full") },
	})
	if err == nil {
		t.Fatal("expected persistBatch error to propagate")
	}
	if !strings.Contains(err.Error(), "persist") {
		t.Errorf("error should name persist; got %q", err)
	}
}

func TestEmbedItems_AllReusedSkipsEmbedder(t *testing.T) {
	t.Parallel()
	emb := modelEmbedder{fakeEmbedder: newFakeEmbedder(), name: "m"}
	// First pass builds the existing set.
	rows, err := embedItems(context.Background(), embedRequest{embedder: emb, items: twoItems()})
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := emb.batchCalls.Load()
	if first == 0 {
		t.Fatal("precondition: first pass should call embedder")
	}
	existing := make(map[string]Vector, len(rows))
	for _, r := range rows {
		existing[r.ItemID] = r
	}
	// Second pass: identical text + model + dim -> no embedder call.
	if _, err := embedItems(context.Background(), embedRequest{embedder: emb, items: twoItems(), existing: existing}); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := emb.batchCalls.Load(); got != first {
		t.Errorf("all-reused path should not call embedder; batch calls %d -> %d", first, got)
	}
}

func TestEmbedItems_ModelMismatchForcesReembed(t *testing.T) {
	t.Parallel()
	emb := modelEmbedder{fakeEmbedder: newFakeEmbedder(), name: "new-model"}
	// Existing vector stamped with a different model -> must re-embed.
	existing := map[string]Vector{
		"a": {ItemID: "a", TextHash: sha("alpha"), Embedding: make([]float32, 8), Model: "old-model", Dim: 8},
	}
	before := emb.batchCalls.Load()
	_, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: []Item{{ItemID: "a", Text: "alpha"}}, existing: existing,
	})
	if err != nil {
		t.Fatalf("embedItems: %v", err)
	}
	if emb.batchCalls.Load() == before {
		t.Error("model mismatch should force a fresh embed call")
	}
}

func TestProviderModel(t *testing.T) {
	t.Parallel()
	if got := providerModel(newFakeEmbedder()); got != "" {
		t.Errorf("plain embedder model = %q; want empty", got)
	}
	if got := providerModel(modelEmbedder{fakeEmbedder: newFakeEmbedder(), name: "m"}); got != "m" {
		t.Errorf("model embedder model = %q; want m", got)
	}
}

// sha is a tiny helper for building an existing-vector hash that does
// not match the test text (so reuse is gated on the model field).
func sha(s string) []byte {
	b := make([]byte, 32)
	copy(b, s)
	return b
}
