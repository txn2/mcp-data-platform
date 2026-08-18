package indexjobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// contextLengthRefusal is the body Ollama returns for an input that does
// not fit the model's context.
const contextLengthRefusal = `{"error":"the input length exceeds the context length"}`

// oversizeOllama stands in for an Ollama server whose model refuses any
// input longer than acceptAt bytes, which is the shape that failed one
// resource 835 times on a live deployment (#1350). It answers both the
// batch and the single-input endpoints so the provider's own fallback is
// exercised rather than stubbed.
func oversizeOllama(t *testing.T, acceptAt int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls.Add(1)
		refuse := func() {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(contextLengthRefusal))
		}
		if r.URL.Path == "/api/embed" {
			var req struct {
				Input []string `json:"input"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			for _, in := range req.Input {
				if len(in) > acceptAt {
					refuse()
					return
				}
			}
			out := make([][]float64, len(req.Input))
			for i := range out {
				out[i] = []float64{0.25}
			}
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"embeddings": out}))
			return
		}
		var req struct {
			Prompt string `json:"prompt"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		if len(req.Prompt) > acceptAt {
			refuse()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"embedding": []float64{0.25}}))
	}))
}

// TestWorkerProcess_OversizedItemIndexesInsteadOfFailing wires the real
// worker, the real embed pass, and the real Ollama provider against a
// server that refuses over-context input. It is the end-to-end form of
// #1350: before the adaptive bound, this job failed, was re-queued by the
// next sweep, and failed again identically forever.
//
// A unit test of the provider alone would not prove this. The bound lives
// in the provider and the failure was observed in the worker, so what has
// to be shown is that the provider's convergence actually reaches the
// worker's embed pass and turns a terminal failure into a written vector.
func TestWorkerProcess_OversizedItemIndexesInsteadOfFailing(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := oversizeOllama(t, 1000, &calls)
	defer srv.Close()

	prov := embedding.NewOllamaProvider(embedding.OllamaConfig{
		URL: srv.URL, Model: "m", MaxInputBytes: 6000,
	})
	src := &stubSource{kind: "resources", items: []Item{
		{ItemID: "r1", Text: strings.Repeat("a,b,c,d\n", 2000)},
	}}
	snk := &stubSink{kind: "resources"}
	store := &recordingStore{}
	w := NewWorker(WorkerConfig{
		Store: store, Registry: registryWith(src, snk), Embedder: prov, WorkerID: "w1",
	})

	w.process(context.Background(), &Job{
		ID: 1, SourceKind: "resources", SourceID: "r1",
		Trigger: TriggerWrite, Status: StatusRunning, Attempts: 1,
	})

	assert.True(t, store.completed, "the job must complete, not fail")
	assert.False(t, store.failed, "an over-context input must not be a terminal failure")
	assert.False(t, store.retried, "nor a retry: the provider converged within the call")
	require.Len(t, snk.upserted, 1, "a vector must be written for the oversized item")
	assert.NotEmpty(t, snk.upserted[0].Embedding, "the written vector must be real")
	assert.Equal(t, 1, snk.stamped, "the unit's expected count must be stamped")
	// 16000 bytes: the batch call is refused, the per-input rerun is refused
	// at 6000, then halves to 3000, 1500 and 750. Bounded, not a loop.
	assert.Equal(t, int32(5), calls.Load(), "convergence must cost a bounded number of calls")
}

// TestWorkerProcess_UnembeddableItemStillFails proves the bound is not a
// blanket suppression. When shrinking cannot make an input acceptable the
// failure must still surface, because that is what puts the unit on the
// triage surface where the park deferral then applies to it.
func TestWorkerProcess_UnembeddableItemStillFails(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := oversizeOllama(t, 0, &calls)
	defer srv.Close()

	prov := embedding.NewOllamaProvider(embedding.OllamaConfig{
		URL: srv.URL, Model: "m", MaxInputBytes: 6000,
	})
	src := &stubSource{kind: "resources", items: []Item{{ItemID: "r1", Text: "anything"}}}
	store := &recordingStore{}
	w := NewWorker(WorkerConfig{
		Store: store, Registry: registryWith(src, &stubSink{kind: "resources"}),
		Embedder: prov, WorkerID: "w1",
	})

	w.process(context.Background(), &Job{
		ID: 1, SourceKind: "resources", SourceID: "r1",
		Trigger: TriggerWrite, Status: StatusRunning, Attempts: MaxAttempts,
	})

	assert.True(t, store.failed, "an input no bound can fix must reach the failure surface")
	assert.False(t, store.completed)
	assert.Less(t, calls.Load(), int32(10), "the shrink loop must stop at the floor, not spin")
}
