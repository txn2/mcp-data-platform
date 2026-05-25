package apigateway

import (
	"context"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// TestComputeOperationEmbeddings_NilEmbedderReturnsNil documents
// the nil-embedder contract: admin write paths can call this
// unconditionally without guarding on the embedder.
func TestComputeOperationEmbeddings_NilEmbedderReturnsNil(t *testing.T) {
	t.Parallel()
	rows, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: nil, Content: persistedEmbedTestSpec, SpecName: "default",
	})
	if err != nil {
		t.Fatalf("nil embedder should not error; got %v", err)
	}
	if rows != nil {
		t.Errorf("nil embedder should return nil rows; got %d", len(rows))
	}
}

// TestComputeOperationEmbeddings_UnparseableSpecErrors covers the
// parse-failure path: malformed YAML bubbles up as a wrapped
// "parse spec" error so the admin handler logs the right cause.
func TestComputeOperationEmbeddings_UnparseableSpecErrors(t *testing.T) {
	t.Parallel()
	_, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: newFakeEmbedder(8), Content: "::not yaml::", SpecName: "default",
	})
	if err == nil {
		t.Fatal("expected error on malformed spec")
	}
	if !strings.Contains(err.Error(), "parse spec") {
		t.Errorf("error should name parse spec; got %q", err)
	}
}

// TestComputeOperationEmbeddings_ZeroOperationsReturnsNil covers
// the no-operations early-return: a spec that parses but has zero
// methods on any path produces no rows.
func TestComputeOperationEmbeddings_ZeroOperationsReturnsNil(t *testing.T) {
	t.Parallel()
	emptySpec := `openapi: 3.0.0
info: {title: t, version: "1"}
paths: {}`
	rows, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: newFakeEmbedder(8), Content: emptySpec, SpecName: "default",
	})
	if err != nil {
		t.Fatalf("zero-op spec should not error; got %v", err)
	}
	if rows != nil {
		t.Errorf("zero-op spec should return nil; got %d rows", len(rows))
	}
}

// TestComputeOperationEmbeddings_BatchErrorPropagates drives the
// embedInBatches failure path on the compute helper so the wrapped
// error surfaces to the admin handler. Exercises fillFreshEmbeddings
// directly through its parent.
func TestComputeOperationEmbeddings_BatchErrorPropagates(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder(8)
	emb.failBatch.Store(true)
	_, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: emb, Content: persistedEmbedTestSpec, SpecName: "default",
	})
	if err == nil {
		t.Fatal("expected error from failing batch")
	}
	if !strings.Contains(err.Error(), "embed operation batch") {
		t.Errorf("error should name embed operation batch; got %q", err)
	}
}

// TestComputeOperationEmbeddings_CountMismatchPropagates drives
// the count-mismatch guard via embedInBatches (which is the
// primary enforcer of "vectors-returned == texts-passed"), and
// verifies the wrapped error surfaces through
// ComputeOperationEmbeddings. fillFreshEmbeddings has its own
// belt-and-braces count check that is defensive against a future
// refactor bypassing embedInBatches.
func TestComputeOperationEmbeddings_CountMismatchPropagates(t *testing.T) {
	t.Parallel()
	_, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: countMismatchEmbedder{returnCount: 1}, Content: persistedEmbedTestSpec, SpecName: "default",
	})
	if err == nil {
		t.Fatal("expected error from vector count mismatch")
	}
	if !strings.Contains(err.Error(), "returned") || !strings.Contains(err.Error(), "vectors") {
		t.Errorf("error should name the count mismatch; got %q", err)
	}
}

// countMismatchEmbedder returns fewer vectors than texts, simulating
// a provider bug that drops items silently.
type countMismatchEmbedder struct{ returnCount int }

func (countMismatchEmbedder) Dimension() int { return 8 }
func (countMismatchEmbedder) Kind() string   { return "fake" }
func (countMismatchEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{1, 0, 0, 0, 0, 0, 0, 0}, nil
}

func (e countMismatchEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	out := make([][]float32, e.returnCount)
	for i := range out {
		out[i] = []float32{1, 0, 0, 0, 0, 0, 0, 0}
	}
	return out, nil
}

// TestComputeOperationEmbeddings_ProgressCallback proves the
// progress callback is invoked with the initial reused count up
// front (so a fully-cached spec ticks straight to operation_count)
// AND at every chunk boundary during the fresh-embed pass. The
// callback is the path the embed-jobs worker uses to publish
// embedded_so_far on the job row (#430).
func TestComputeOperationEmbeddings_ProgressCallback(t *testing.T) {
	t.Parallel()
	emb := newFakeEmbedder(8)
	var calls []int
	progress := func(n int) { calls = append(calls, n) }
	rows, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: emb, Content: persistedEmbedTestSpec, SpecName: "default", Progress: progress,
	})
	if err != nil {
		t.Fatalf("ComputeOperationEmbeddings: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	// At minimum: one initial publish (reused=0) and one chunk-done
	// publish (fresh=2). The final cumulative count must equal the
	// number of operations.
	if len(calls) < 2 {
		t.Fatalf("progress called %d times; want >= 2 (initial + chunk-done): %v", len(calls), calls)
	}
	if calls[len(calls)-1] != 2 {
		t.Errorf("final progress = %d; want 2 (all operations ready)", calls[len(calls)-1])
	}
}

// TestComputeOperationEmbeddings_AllReusedSkipsFreshEmbed covers
// fillFreshEmbeddings's early-return: when every operation's text
// hash + dim + model already match the existing set, the embedder
// must not be invoked. Drives a counting embedder and asserts zero
// calls on the second pass.
func TestComputeOperationEmbeddings_AllReusedSkipsFreshEmbed(t *testing.T) {
	t.Parallel()
	emb := newTrackingEmbedder()
	// First pass writes the existing set.
	rows, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: emb, Content: persistedEmbedTestSpec, SpecName: "default",
	})
	if err != nil {
		t.Fatalf("first compute: %v", err)
	}
	firstBatch := emb.batchCalls.Load()
	if firstBatch == 0 {
		t.Fatal("precondition: first pass should call EmbedBatch")
	}
	existing := make(map[string]catalog.OperationEmbedding, len(rows))
	for _, r := range rows {
		existing[r.OperationID] = r
	}
	// Second pass with identical content + identical model. Nothing
	// to re-embed, so fillFreshEmbeddings returns without calling
	// the provider.
	if _, err := ComputeOperationEmbeddings(context.Background(), ComputeRequest{
		Embedder: emb, Content: persistedEmbedTestSpec, SpecName: "default", Existing: existing,
	}); err != nil {
		t.Fatalf("second compute: %v", err)
	}
	if got := emb.batchCalls.Load(); got != firstBatch {
		t.Errorf("all-reused path should not call embedder; batch calls went from %d to %d", firstBatch, got)
	}
}
