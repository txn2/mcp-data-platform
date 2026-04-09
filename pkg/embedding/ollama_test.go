package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestOllamaProvider_EmbedBatch_Success(t *testing.T) {
	t.Parallel()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		resp := ollamaResponse{Embedding: []float64{float64(callCount), 0.0}}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	results, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, results, 3)
	// Each call increments, so first embedding starts with 1.0
	assert.InDelta(t, float32(1.0), results[0][0], 0.0001)
	assert.InDelta(t, float32(2.0), results[1][0], 0.0001)
	assert.InDelta(t, float32(3.0), results[2][0], 0.0001)
}

func TestOllamaProvider_EmbedBatch_ErrorOnSecond(t *testing.T) {
	t.Parallel()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
			return
		}
		resp := ollamaResponse{Embedding: []float64{1.0}}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{URL: srv.URL})

	results, err := p.EmbedBatch(context.Background(), []string{"ok", "fail", "ok"})
	assert.Nil(t, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding text[1]")
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
