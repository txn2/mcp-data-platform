package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOllamaProvider_Defaults(t *testing.T) {
	t.Parallel()

	prov := NewOllamaProvider(OllamaConfig{})
	p, ok := prov.(*ollamaProvider)
	require.True(t, ok, "expected *ollamaProvider")

	assert.Equal(t, "http://localhost:11434", p.url)
	assert.Equal(t, "nomic-embed-text", p.model)
	assert.Equal(t, DefaultDimension, p.dim)
	assert.NotNil(t, p.client)
	assert.Equal(t, DefaultTimeout*time.Second, p.client.Timeout)
}

func TestNewOllamaProvider_CustomValues(t *testing.T) {
	t.Parallel()

	prov := NewOllamaProvider(OllamaConfig{
		URL:     "http://custom:1234",
		Model:   "custom-model",
		Timeout: 5 * time.Second,
	})
	p, ok := prov.(*ollamaProvider)
	require.True(t, ok, "expected *ollamaProvider")

	assert.Equal(t, "http://custom:1234", p.url)
	assert.Equal(t, "custom-model", p.model)
	assert.Equal(t, 5*time.Second, p.client.Timeout)
}

func TestOllamaProvider_Dimension(t *testing.T) {
	t.Parallel()

	p := NewOllamaProvider(OllamaConfig{})
	assert.Equal(t, DefaultDimension, p.Dimension())
}

func TestOllamaProvider_Embed_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/embeddings", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req ollamaRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-model", req.Model)
		assert.Equal(t, "hello world", req.Prompt)

		resp := ollamaResponse{
			Embedding: []float64{0.1, 0.2, 0.3},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{
		URL:   srv.URL,
		Model: "test-model",
	})

	emb, err := p.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	require.Len(t, emb, 3)
	assert.InDelta(t, float32(0.1), emb[0], 0.0001)
	assert.InDelta(t, float32(0.2), emb[1], 0.0001)
	assert.InDelta(t, float32(0.3), emb[2], 0.0001)
}

func TestOllamaProvider_Embed_Non200Status(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("model not found"))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	emb, err := p.Embed(context.Background(), "test")
	assert.Nil(t, emb)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
	assert.Contains(t, err.Error(), "model not found")
}

func TestOllamaProvider_Embed_ConnectionError(t *testing.T) {
	t.Parallel()

	p := NewOllamaProvider(OllamaConfig{
		URL:     "http://127.0.0.1:1", // unreachable port
		Timeout: 1 * time.Second,
	})

	emb, err := p.Embed(context.Background(), "test")
	assert.Nil(t, emb)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "calling Ollama embeddings API")
}

func TestOllamaProvider_Embed_InvalidJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	emb, err := p.Embed(context.Background(), "test")
	assert.Nil(t, emb)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding Ollama response")
}

func TestOllamaProvider_Embed_CancelledContext(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	emb, err := p.Embed(ctx, "test")
	assert.Nil(t, emb)
	require.Error(t, err)
}

func TestOllamaProvider_EmbedBatch_BatchEndpoint_SingleHTTPCall(t *testing.T) {
	t.Parallel()

	var (
		batchCallCount    int
		singularCallCount int
		gotInput          []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			batchCallCount++
			var req ollamaBatchRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			gotInput = req.Input
			out := ollamaBatchResponse{
				Embeddings: make([][]float64, len(req.Input)),
			}
			for i := range req.Input {
				out.Embeddings[i] = []float64{float64(i + 1), 0.0}
			}
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(out))
		case "/api/embeddings":
			singularCallCount++
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL, Model: "test-model"})

	results, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, 1, batchCallCount, "EmbedBatch must make exactly one HTTP call against /api/embed for N texts")
	assert.Zero(t, singularCallCount, "must not fall back to /api/embeddings on the happy path")
	assert.Equal(t, []string{"a", "b", "c"}, gotInput)
	assert.InDelta(t, float32(1.0), results[0][0], 0.0001)
	assert.InDelta(t, float32(2.0), results[1][0], 0.0001)
	assert.InDelta(t, float32(3.0), results[2][0], 0.0001)
}

func TestOllamaProvider_EmbedBatch_FallbackOn404(t *testing.T) {
	t.Parallel()

	var (
		batchCallCount    int
		singularCallCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			batchCallCount++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
		case "/api/embeddings":
			singularCallCount++
			resp := ollamaResponse{Embedding: []float64{float64(singularCallCount)}}
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(resp))
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	// First EmbedBatch: try /api/embed (404), fall back to /api/embeddings sequential.
	results, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, 1, batchCallCount, "first call probes /api/embed once")
	assert.Equal(t, 3, singularCallCount, "fallback issues one /api/embeddings per text")

	// Second EmbedBatch: must NOT re-probe /api/embed; goes straight to sequential.
	results2, err := p.EmbedBatch(context.Background(), []string{"d", "e"})
	require.NoError(t, err)
	require.Len(t, results2, 2)
	assert.Equal(t, 1, batchCallCount, "second call must skip /api/embed entirely")
	assert.Equal(t, 5, singularCallCount, "second call adds two more /api/embeddings calls")
}

func TestOllamaProvider_EmbedBatch_FallbackPropagatesSequentialError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			w.WriteHeader(http.StatusNotFound)
		case "/api/embeddings":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		}
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	results, err := p.EmbedBatch(context.Background(), []string{"x", "y"})
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding text[0]")
}

func TestOllamaProvider_EmbedBatch_LengthMismatch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/embed", r.URL.Path)
		// Protocol violation: server returns only 1 embedding for 3 inputs.
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(ollamaBatchResponse{
			Embeddings: [][]float64{{1.0, 2.0}},
		}))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	results, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned 1 embeddings for 3 inputs")
}

func TestOllamaProvider_EmbedBatch_BatchEndpoint_Non200NotFallback(t *testing.T) {
	t.Parallel()

	var (
		batchCallCount    int
		singularCallCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			batchCallCount++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("model not loaded"))
		case "/api/embeddings":
			singularCallCount++
		}
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	results, err := p.EmbedBatch(context.Background(), []string{"a", "b"})
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
	assert.Equal(t, 1, batchCallCount)
	assert.Zero(t, singularCallCount, "non-404 batch errors must not trigger sequential fallback")
}

func TestOllamaProvider_EmbedBatch_Empty(t *testing.T) {
	t.Parallel()

	p := NewOllamaProvider(OllamaConfig{URL: "http://unused"})
	results, err := p.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestToFloat32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []float64
		want []float32
	}{
		{name: "empty", in: nil, want: []float32{}},
		{name: "single", in: []float64{1.5}, want: []float32{1.5}},
		{name: "multiple", in: []float64{0.1, 0.2, -0.3}, want: []float32{0.1, 0.2, -0.3}},
		{name: "large", in: []float64{1e10}, want: []float32{1e10}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toFloat32(tt.in)
			require.Len(t, got, len(tt.want))
			for i := range got {
				assert.InDelta(t, tt.want[i], got[i], 0.001, "index %d", i)
			}
		})
	}
}

// TestOllamaProvider_Model returns the configured model name so
// downstream code (api-catalog embedding storage) can stamp the
// model into the row metadata for debugging.
func TestOllamaProvider_Model(t *testing.T) {
	t.Parallel()
	prov := NewOllamaProvider(OllamaConfig{Model: "my-model"})
	p, ok := prov.(*ollamaProvider)
	require.True(t, ok)
	assert.Equal(t, "my-model", p.Model())
}

// TestNewOllamaProvider_MaxInputBytes verifies the default cap is applied
// when unset (or non-positive) and that an explicit value is honored.
func TestNewOllamaProvider_MaxInputBytes(t *testing.T) {
	t.Parallel()

	def, ok := NewOllamaProvider(OllamaConfig{}).(*ollamaProvider)
	require.True(t, ok)
	assert.Equal(t, DefaultMaxInputBytes, def.maxInputBytes)

	zeroed, ok := NewOllamaProvider(OllamaConfig{MaxInputBytes: -10}).(*ollamaProvider)
	require.True(t, ok)
	assert.Equal(t, DefaultMaxInputBytes, zeroed.maxInputBytes, "non-positive selects default")

	custom, ok := NewOllamaProvider(OllamaConfig{MaxInputBytes: 24000}).(*ollamaProvider)
	require.True(t, ok)
	assert.Equal(t, 24000, custom.maxInputBytes)
}

func TestCapForEmbedding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        string
		maxBytes  int
		wantBytes int
		wantTrunc bool
	}{
		{name: "within budget", in: "hello", maxBytes: 100, wantBytes: 5, wantTrunc: false},
		{name: "exactly at budget", in: "hello", maxBytes: 5, wantBytes: 5, wantTrunc: false},
		{name: "over budget", in: "hello world", maxBytes: 5, wantBytes: 5, wantTrunc: true},
		{name: "disabled with zero", in: "hello world", maxBytes: 0, wantBytes: 11, wantTrunc: false},
		{name: "disabled with negative", in: "hello world", maxBytes: -1, wantBytes: 11, wantTrunc: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, trunc := capForEmbedding(tt.in, tt.maxBytes)
			assert.Equal(t, tt.wantTrunc, trunc)
			assert.Len(t, got, tt.wantBytes)
		})
	}
}

// TestCapForEmbedding_UTF8Boundary ensures truncation never splits a
// multi-byte rune: the result stays valid UTF-8 and within the budget.
func TestCapForEmbedding_UTF8Boundary(t *testing.T) {
	t.Parallel()

	// "世" is 3 bytes each; a 7-byte budget lands mid-rune (2 full runes
	// = 6 bytes, the 3rd straddles 7..9).
	in := "世世世"
	got, trunc := capForEmbedding(in, 7)
	assert.True(t, trunc)
	assert.True(t, utf8.ValidString(got), "truncation must not split a rune")
	assert.LessOrEqual(t, len(got), 7)
	assert.Equal(t, "世世", got)
}

// TestOllamaProvider_Embed_CapsInputAndSetsTruncate proves the single
// /api/embeddings path caps oversized input before the request and sets
// truncate:true. This is the #623 fix: the platform bounds input itself
// rather than trusting Ollama's (unreliable) truncate flag.
func TestOllamaProvider_Embed_CapsInputAndSetsTruncate(t *testing.T) {
	t.Parallel()

	var got ollamaRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(ollamaResponse{Embedding: []float64{0.1}}))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL, Model: "m", MaxInputBytes: 10})
	oversized := strings.Repeat("a", 100)
	_, err := p.Embed(context.Background(), oversized)
	require.NoError(t, err)

	assert.Len(t, got.Prompt, 10, "input must be capped to MaxInputBytes before sending")
	assert.True(t, got.Truncate, "truncate must be set true")
}

// TestOllamaProvider_EmbedBatch_CapsInputsAndSetsTruncate proves the
// batch /api/embed path caps every oversized input and sets truncate:true.
func TestOllamaProvider_EmbedBatch_CapsInputsAndSetsTruncate(t *testing.T) {
	t.Parallel()

	var got ollamaBatchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(ollamaBatchResponse{
			Embeddings: [][]float64{{0.1}, {0.2}},
		}))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL, Model: "m", MaxInputBytes: 10})
	_, err := p.EmbedBatch(context.Background(), []string{strings.Repeat("a", 100), "short"})
	require.NoError(t, err)

	require.Len(t, got.Input, 2)
	assert.Len(t, got.Input[0], 10, "oversized input must be capped")
	assert.Equal(t, "short", got.Input[1], "within-budget input must be untouched")
	assert.True(t, got.Truncate, "truncate must be set true")
}
