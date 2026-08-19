package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contextLengthBody is the 400 body Ollama 0.18.0 + nomic-embed-text
// returns when an input does not fit the model's context. It is the
// error that failed one resource 835 times on a live deployment (#1350).
const contextLengthBody = `{"error":"the input length exceeds the context length"}`

func TestIsContextLengthError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{"canonical ollama body", contextLengthBody, true},
		{"reworded exceeded", `{"error":"context length exceeded"}`, true},
		{"context window wording", `{"error":"exceeds the context window"}`, true},
		{"input is too large wording", `{"error":"input is too large"}`, true},
		{"mixed case", `{"error":"Input Length Exceeds The Context Length"}`, true},
		{"unrelated refusal", `{"error":"model not found"}`, false},
		{"outage body", "upstream unavailable", false},
		{"empty body", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isContextLengthError(tt.body))
		})
	}
}

// TestIsContextLengthError_StatusDoesNotGateTheMatch pins #1385 with the
// response the deployment produced: the single-input endpoint refused the
// same oversized text with a 500 where the batch endpoint had said 400,
// body identical. Both must classify as ErrInputTooLarge, or the halving
// loop never engages and the unit fails identically forever.
func TestIsContextLengthError_StatusDoesNotGateTheMatch(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(contextLengthBody))
		}))
		p := NewOllamaProvider(OllamaConfig{URL: srv.URL, Model: "m", MaxInputBytes: MinInputBytes})
		_, err := p.Embed(context.Background(), "x")
		srv.Close()
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInputTooLarge), "status %d with the context-length body must classify as ErrInputTooLarge, got %v", status, err)
	}
}

func TestSentBytes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 10, sentBytes("aaaaaaaaaaaaaaa", 10), "budget binds when the text is longer")
	assert.Equal(t, 4, sentBytes("abcd", 10), "the text binds when it is shorter than the budget")
	assert.Equal(t, 4, sentBytes("abcd", 4), "equal lengths report the budget")
	assert.Equal(t, 0, sentBytes("", 10), "an empty text sends nothing")
}

// oversizeServer answers /api/embeddings with a context-length 400 until
// the prompt it receives is at most acceptAt bytes, then succeeds. It
// records every prompt length it was asked to embed, which is what the
// convergence assertions read.
type oversizeServer struct {
	mu        sync.Mutex
	acceptAt  int
	promptLen []int
	// singleStatus is the status the single-input endpoint refuses with.
	// Zero means 400. The Ollama behind #1385 answers 500 there while the
	// batch endpoint answers 400 for the same input and the same body.
	singleStatus int
}

func (s *oversizeServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/api/embed" {
			var req ollamaBatchRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(contextLengthBody))
			return
		}
		var req ollamaRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		s.promptLen = append(s.promptLen, len(req.Prompt))
		if len(req.Prompt) > s.acceptAt {
			status := s.singleStatus
			if status == 0 {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(contextLengthBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(ollamaResponse{Embedding: []float64{0.5}}))
	}
}

func (s *oversizeServer) prompts() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.promptLen...)
}

// TestEmbed_ShrinksUntilTheModelAcceptsIt is the core of #1350: a text
// inside the configured byte cap that the model still refuses must
// converge to a bound it accepts, rather than returning the same error on
// every call forever.
func TestEmbed_ShrinksUntilTheModelAcceptsIt(t *testing.T) {
	t.Parallel()

	srv := &oversizeServer{acceptAt: 1000}
	ts := httptest.NewServer(srv.handler(t))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	vec, err := p.Embed(context.Background(), strings.Repeat("a", 6000))
	require.NoError(t, err, "an oversized input must converge, not fail")
	assert.Equal(t, []float32{0.5}, vec)

	// 6000 refused, 3000 refused, 1500 refused, 750 accepted.
	assert.Equal(t, []int{6000, 3000, 1500, 750}, srv.prompts(),
		"each retry must halve the bytes actually sent")
}

// TestEmbed_ShrinkHalvesTheBytesSentNotTheBudget proves a text far inside
// an oversized budget converges from its own length, so it does not pay
// retries that re-send byte-for-byte the same request.
func TestEmbed_ShrinkHalvesTheBytesSentNotTheBudget(t *testing.T) {
	t.Parallel()

	srv := &oversizeServer{acceptAt: 600}
	ts := httptest.NewServer(srv.handler(t))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	_, err := p.Embed(context.Background(), strings.Repeat("a", 1000))
	require.NoError(t, err)

	assert.Equal(t, []int{1000, 500}, srv.prompts(),
		"the first retry must halve 1000 (what was sent), not 6000 (the budget)")
}

// TestEmbed_StopsAtTheFloor proves the loop terminates and surfaces the
// provider's own error when shrinking cannot help, rather than spinning
// down to an empty string.
func TestEmbed_StopsAtTheFloor(t *testing.T) {
	t.Parallel()

	srv := &oversizeServer{acceptAt: 0}
	ts := httptest.NewServer(srv.handler(t))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	_, err := p.Embed(context.Background(), strings.Repeat("a", 6000))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInputTooLarge)
	for _, n := range srv.prompts() {
		assert.GreaterOrEqual(t, n, MinInputBytes,
			"the loop must never send fewer than MinInputBytes")
	}
	assert.Less(t, len(srv.prompts()), 10, "the loop must terminate quickly")
}

// TestEmbed_NonContextErrorIsNotRetried proves the shrink loop is scoped
// to the deterministic failure. A 500 must surface immediately so the
// caller's own retry-with-backoff still governs a provider outage.
func TestEmbed_NonContextErrorIsNotRetried(t *testing.T) {
	t.Parallel()

	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	_, err := p.Embed(context.Background(), strings.Repeat("a", 6000))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInputTooLarge)
	assert.Equal(t, 1, calls, "a non-context failure must not be retried at a smaller bound")
}

// TestEmbedBatch_RefusedBatchRerunsPerInput proves a batch the model
// refuses is re-run one input at a time. The batch endpoint reports one
// refusal for the whole array, so only a per-input pass can tell which
// text overflowed and bound just that one.
func TestEmbedBatch_RefusedBatchRerunsPerInput(t *testing.T) {
	t.Parallel()

	srv := &oversizeServer{acceptAt: 1000}
	ts := httptest.NewServer(srv.handler(t))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	vecs, err := p.EmbedBatch(context.Background(), []string{"short", strings.Repeat("a", 6000)})
	require.NoError(t, err)
	require.Len(t, vecs, 2)

	assert.Equal(t, []int{5, 6000, 3000, 1500, 750}, srv.prompts(),
		"the within-budget input must embed whole; only the oversized one shrinks")
}

// TestEmbedBatch_ConvergesWhenTheSingleEndpointRefusesWith500 is the
// #1385 reproduction end to end: the batch endpoint refuses with a 400,
// the per-input rerun is refused with a 500 carrying the same body, and
// the oversized input must still converge instead of surfacing the 500 as
// a generic failure on every attempt.
func TestEmbedBatch_ConvergesWhenTheSingleEndpointRefusesWith500(t *testing.T) {
	t.Parallel()

	srv := &oversizeServer{acceptAt: 1000, singleStatus: http.StatusInternalServerError}
	ts := httptest.NewServer(srv.handler(t))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	vecs, err := p.EmbedBatch(context.Background(), []string{"short", strings.Repeat("a", 6000)})
	require.NoError(t, err, "a 500 with the context-length body must be bounded like the 400")
	require.Len(t, vecs, 2)

	assert.Equal(t, []int{5, 6000, 3000, 1500, 750}, srv.prompts(),
		"the oversized input must halve until accepted whatever status refused it")
}

// TestEmbedBatch_RefusedBatchDoesNotMarkTheEndpointUnsupported proves the
// per-input rerun is scoped to the batch whose contents were refused. A
// context-length 400 says nothing about the endpoint, so a later healthy
// batch must still take the single-call path.
func TestEmbedBatch_RefusedBatchDoesNotMarkTheEndpointUnsupported(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var batchCalls, singleCalls int
	refuse := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/embed" {
			batchCalls++
			var req ollamaBatchRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			if refuse {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(contextLengthBody))
				return
			}
			out := make([][]float64, len(req.Input))
			for i := range out {
				out[i] = []float64{0.5}
			}
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(ollamaBatchResponse{Embeddings: out}))
			return
		}
		singleCalls++
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(ollamaResponse{Embedding: []float64{0.5}}))
	}))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: 6000})
	_, err := p.EmbedBatch(context.Background(), []string{"a", "b"})
	require.NoError(t, err)

	mu.Lock()
	refuse = false
	mu.Unlock()

	_, err = p.EmbedBatch(context.Background(), []string{"c", "d"})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, batchCalls, "the second batch must still try the batch endpoint")
	assert.Equal(t, 2, singleCalls, "only the refused batch reruns per input")
}

// TestEmbed_ContextLengthErrorWrapsTheProviderBody proves the sentinel is
// added to the provider's error rather than replacing it, so an operator
// reading the failure still sees what the server said.
func TestEmbed_ContextLengthErrorWrapsTheProviderBody(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(contextLengthBody))
	}))
	defer ts.Close()

	p := NewOllamaProvider(OllamaConfig{URL: ts.URL, Model: "m", MaxInputBytes: MinInputBytes})
	_, err := p.Embed(context.Background(), "tiny")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInputTooLarge)
	assert.Contains(t, err.Error(), "the input length exceeds the context length")
	assert.Contains(t, err.Error(), fmt.Sprintf("status %d", http.StatusBadRequest))
}

// TestErrInputTooLarge_IsDistinctFromGenericFailure guards the property
// callers rely on: the sentinel must not match an unrelated error.
func TestErrInputTooLarge_IsDistinctFromGenericFailure(t *testing.T) {
	t.Parallel()

	assert.False(t, errors.Is(errors.New("ollama API returned status 500"), ErrInputTooLarge))
	assert.True(t, errors.Is(fmt.Errorf("wrapped: %w", ErrInputTooLarge), ErrInputTooLarge))
}
