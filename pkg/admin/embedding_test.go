package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// stubProvider is a minimal embedding.Provider that lets each test set
// Kind, Dimension, and (optionally via the modelNamed assertion) Model
// without depending on the ollama package.
type stubProvider struct {
	kind  string
	model string
	dim   int
}

func (s *stubProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, s.dim), nil
}

func (s *stubProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, s.dim)
	}
	return out, nil
}

func (s *stubProvider) Dimension() int { return s.dim }
func (s *stubProvider) Kind() string   { return s.kind }
func (s *stubProvider) Model() string  { return s.model }

func TestGetEmbeddingStatus(t *testing.T) {
	tests := []struct {
		name      string
		provider  embedding.Provider
		wantKind  string
		wantModel string
		wantDim   int
		wantStat  string
	}{
		{
			name:     "nil provider",
			provider: nil,
			wantKind: "",
			wantDim:  0,
			wantStat: embeddingStatusUnconfigured,
		},
		{
			name:     "noop provider",
			provider: embedding.NewNoopProvider(768),
			wantKind: embedding.KindNoop,
			wantDim:  768,
			wantStat: embeddingStatusUnconfigured,
		},
		{
			name:      "real provider with model name",
			provider:  &stubProvider{kind: embedding.KindOllama, model: "nomic-embed-text", dim: 768},
			wantKind:  embedding.KindOllama,
			wantModel: "nomic-embed-text",
			wantDim:   768,
			wantStat:  embeddingStatusOK,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(Deps{Embedder: tc.provider}, nil)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/embedding/status", http.NoBody)
			w := httptest.NewRecorder()
			h.getEmbeddingStatus(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", w.Code)
			}
			var got embeddingProviderStatusResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q; want %q", got.Kind, tc.wantKind)
			}
			if got.Model != tc.wantModel {
				t.Errorf("Model = %q; want %q", got.Model, tc.wantModel)
			}
			if got.Dimension != tc.wantDim {
				t.Errorf("Dimension = %d; want %d", got.Dimension, tc.wantDim)
			}
			if got.Status != tc.wantStat {
				t.Errorf("Status = %q; want %q", got.Status, tc.wantStat)
			}
		})
	}
}
