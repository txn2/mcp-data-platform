package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaConfig configures the Ollama embedding provider.
type OllamaConfig struct {
	URL     string
	Model   string
	Timeout time.Duration
}

// maxErrorBodyBytes is the maximum number of bytes read from an error response body.
const maxErrorBodyBytes = 4096

// ollamaProvider generates embeddings via the Ollama API.
type ollamaProvider struct {
	client *http.Client
	url    string
	model  string
	dim    int
}

// NewOllamaProvider creates an embedding provider that calls Ollama.
func NewOllamaProvider(cfg OllamaConfig) Provider {
	if cfg.URL == "" {
		cfg.URL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		cfg.Model = "nomic-embed-text"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout * time.Second
	}

	return &ollamaProvider{
		client: &http.Client{Timeout: cfg.Timeout},
		url:    cfg.URL,
		model:  cfg.Model,
		dim:    DefaultDimension,
	}
}

// ollamaRequest is the JSON body sent to Ollama's /api/embeddings endpoint.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaResponse is the JSON body returned from Ollama's /api/embeddings endpoint.
type ollamaResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Embed generates an embedding for a single text input.
func (o *ollamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaRequest{
		Model:  o.model,
		Prompt: text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling Ollama embeddings API: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding Ollama response: %w", err)
	}

	return toFloat32(result.Embedding), nil
}

// EmbedBatch generates embeddings for multiple text inputs.
func (o *ollamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := o.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embedding text[%d]: %w", i, err)
		}
		results[i] = emb
	}
	return results, nil
}

// Dimension returns the embedding dimensionality.
func (o *ollamaProvider) Dimension() int {
	return o.dim
}

// toFloat32 converts a float64 slice to float32.
func toFloat32(f64 []float64) []float32 {
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}

// Verify interface compliance.
var _ Provider = (*ollamaProvider)(nil)
