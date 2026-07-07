package indexjobs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
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

// timeoutEmbedder times out on any EmbedBatch call whose chunk is
// larger than maxOK (maxOK <= 0 => every call times out, exercising
// the floor-of-1 path). Successful chunk sizes are recorded so a test
// can prove the batch converged to a completable size. When timeoutErr
// is nil the returned error wraps context.DeadlineExceeded, mirroring
// the Ollama provider's "calling ... API: <ctx deadline>" shape.
type timeoutEmbedder struct {
	dim        int
	maxOK      int
	timeoutErr error
	calls      atomic.Int32

	mu      sync.Mutex
	okSizes []int
}

func newTimeoutEmbedder(maxOK int) *timeoutEmbedder {
	return &timeoutEmbedder{dim: fakeDim, maxOK: maxOK}
}

func (e *timeoutEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, e.dim)
	v[0] = 1
	return v, nil
}

func (e *timeoutEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
	if len(texts) > e.maxOK {
		if e.timeoutErr != nil {
			return nil, e.timeoutErr
		}
		return nil, fmt.Errorf("calling Ollama batch embed API: %w", context.DeadlineExceeded)
	}
	e.mu.Lock()
	e.okSizes = append(e.okSizes, len(texts))
	e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, e.dim)
		out[i][0] = float32(i + 1)
	}
	return out, nil
}

func (e *timeoutEmbedder) Dimension() int { return e.dim }
func (*timeoutEmbedder) Kind() string     { return "fake" }

func nItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{ItemID: fmt.Sprintf("item-%02d", i), Text: fmt.Sprintf("text-%02d", i)}
	}
	return items
}

// TestEmbedItems_ShrinksBatchOnTimeout is acceptance criterion #1: a
// provider that times out at 32 but succeeds at <=8 completes the
// whole unit at the smaller size with no error surfaced to the worker.
func TestEmbedItems_ShrinksBatchOnTimeout(t *testing.T) {
	t.Parallel()
	emb := newTimeoutEmbedder(8)
	rows, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: nItems(32), batchSize: 32,
	})
	if err != nil {
		t.Fatalf("adaptive batch should complete without error; got %v", err)
	}
	if len(rows) != 32 {
		t.Fatalf("rows = %d; want 32", len(rows))
	}
	for _, r := range rows {
		if len(r.Embedding) != fakeDim {
			t.Fatalf("row %q not embedded (dim %d)", r.ItemID, len(r.Embedding))
		}
	}
	emb.mu.Lock()
	defer emb.mu.Unlock()
	if len(emb.okSizes) == 0 {
		t.Fatal("no successful chunks recorded")
	}
	total := 0
	for _, s := range emb.okSizes {
		if s > 8 {
			t.Errorf("succeeded on a chunk of %d; every completing chunk must be <= 8", s)
		}
		total += s
	}
	if total != 32 {
		t.Errorf("completed chunks cover %d texts; want 32", total)
	}
}

// TestEmbedItems_PartialProgressPersistsOnShrink is acceptance
// criterion #2: each completed sub-chunk reaches persistBatch, so a
// subsequent attempt's dedup pass sees forward progress.
func TestEmbedItems_PartialProgressPersistsOnShrink(t *testing.T) {
	t.Parallel()
	emb := newTimeoutEmbedder(8)
	var persisted int
	rows, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: nItems(32), batchSize: 32,
		persistBatch: func(b []Vector) error { persisted += len(b); return nil },
	})
	if err != nil {
		t.Fatalf("embedItems: %v", err)
	}
	if persisted != len(rows) || persisted != 32 {
		t.Errorf("persisted %d vectors across sub-chunks; want 32", persisted)
	}
}

// TestEmbedItems_NonTimeoutFailsFast is acceptance criterion #3: a
// genuine provider error is not subdivided. Exactly one EmbedBatch
// call is made before the error surfaces (no retry storm).
func TestEmbedItems_NonTimeoutFailsFast(t *testing.T) {
	t.Parallel()
	emb := newTimeoutEmbedder(0)
	emb.timeoutErr = errors.New("ollama batch API returned status 500: boom")
	_, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: nItems(32), batchSize: 32,
	})
	if err == nil {
		t.Fatal("expected a non-timeout provider error to surface")
	}
	if got := emb.calls.Load(); got != 1 {
		t.Errorf("non-timeout error subdivided: %d EmbedBatch calls; want 1", got)
	}
}

// TestEmbedItems_ShrinksOnNetTimeout drives the net.Error timeout
// branch of isEmbedTimeout end-to-end (not just the isolated
// TestIsEmbedTimeout): an http.Client.Timeout surfaces as a net.Error
// whose Timeout() is true rather than as context.DeadlineExceeded, and
// it must trigger the same subdivision so real Ollama timeouts (which
// arrive in either shape) converge instead of failing the unit.
func TestEmbedItems_ShrinksOnNetTimeout(t *testing.T) {
	t.Parallel()
	emb := newTimeoutEmbedder(8)
	emb.timeoutErr = fmt.Errorf("calling Ollama batch embed API: %w", netTimeoutError{timeout: true})
	rows, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: nItems(32), batchSize: 32,
	})
	if err != nil {
		t.Fatalf("net.Error timeout should subdivide and complete; got %v", err)
	}
	if len(rows) != 32 {
		t.Fatalf("rows = %d; want 32", len(rows))
	}
	emb.mu.Lock()
	defer emb.mu.Unlock()
	for _, s := range emb.okSizes {
		if s > 8 {
			t.Errorf("succeeded on a chunk of %d; every completing chunk must be <= 8", s)
		}
	}
}

// TestEmbedItems_FloorTimeoutSurfaces is acceptance criterion #4: when
// even a single text times out, the error surfaces cleanly instead of
// looping forever. Call count is bounded by the subdivision tree, not
// by attempts.
func TestEmbedItems_FloorTimeoutSurfaces(t *testing.T) {
	t.Parallel()
	emb := newTimeoutEmbedder(0) // every call, even size 1, times out
	_, err := embedItems(context.Background(), embedRequest{
		embedder: emb, items: nItems(4), batchSize: 4,
	})
	if err == nil {
		t.Fatal("expected the floor timeout to surface an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error should unwrap to context.DeadlineExceeded; got %q", err)
	}
	if !strings.Contains(err.Error(), "batch [0:1]") {
		t.Errorf("error should name the single-text chunk that failed; got %q", err)
	}
	// 4 items halving to a floor of 1: 4->[0:2],[2:4]->4x[i:i+1] = 7
	// calls worst case. The exact count is not the contract; a bound
	// well under an attempt storm is.
	if got := emb.calls.Load(); got > 8 {
		t.Errorf("floor timeout made %d calls; expected a bounded subdivision tree", got)
	}
}

// TestEmbedItems_ParentContextDoneFailsFast covers the guard against
// shrinking when the pass's own context is already done: the worker's
// processSafetyBound deadline (or a shutdown) expiring mid-embed
// surfaces as context.DeadlineExceeded, but subdividing against a dead
// context only fires doomed sub-calls. The error must surface on the
// first call, not after a shrink storm.
func TestEmbedItems_ParentContextDoneFailsFast(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // parent context is already done before the pass starts
	emb := newTimeoutEmbedder(0)
	_, err := embedItems(ctx, embedRequest{
		embedder: emb, items: nItems(32), batchSize: 32,
	})
	if err == nil {
		t.Fatal("expected an error when the parent context is done")
	}
	if got := emb.calls.Load(); got != 1 {
		t.Errorf("shrank against a dead context: %d EmbedBatch calls; want 1", got)
	}
}

// netTimeoutError is a net.Error whose Timeout() controls whether
// isEmbedTimeout classifies it as retryable.
type netTimeoutError struct{ timeout bool }

func (netTimeoutError) Error() string   { return "net error" }
func (e netTimeoutError) Timeout() bool { return e.timeout }
func (netTimeoutError) Temporary() bool { return false }

func TestIsEmbedTimeout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), true},
		{"canceled is not a timeout", context.Canceled, false},
		{"net timeout", net.Error(netTimeoutError{timeout: true}), true},
		{"net non-timeout", net.Error(netTimeoutError{timeout: false}), false},
		{"plain provider error", errors.New("status 500"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isEmbedTimeout(tc.err); got != tc.want {
				t.Errorf("isEmbedTimeout(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}
