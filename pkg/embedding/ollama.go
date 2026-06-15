package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// OllamaConfig configures the Ollama embedding provider.
type OllamaConfig struct {
	URL     string
	Model   string
	Timeout time.Duration
	// MaxInputBytes caps the byte length of each text sent to Ollama.
	// Zero or negative selects DefaultMaxInputBytes. See that constant
	// for why the platform bounds input itself rather than trusting
	// Ollama's truncate flag.
	MaxInputBytes int
}

// maxErrorBodyBytes is the maximum number of bytes read from an error response body.
const maxErrorBodyBytes = 4096

// DefaultMaxInputBytes bounds the byte length of each text the provider
// sends to Ollama. The platform must cap input itself rather than rely
// on Ollama's truncate flag, which is unreliable: against Ollama 0.18.0
// + nomic-embed-text at a 2048-token context, real content that exceeds
// the context returns HTTP 400 "the input length exceeds the context
// length" EVEN with truncate:true, because Ollama's Go-layer truncation
// and the runner's tokenizer disagree on the token count for some
// content. Plain prose embeds at ~3.4 chars/token, so the ~2048-token
// boundary sits near 7000 bytes; 6000 leaves margin for tokenizer drift
// and denser content (code, JSON specs). Operators running a larger-
// context model can raise this via config. The cap only trims the text
// that is embedded; the full content is still stored. See #623.
const DefaultMaxInputBytes = 6000

// ollamaProvider generates embeddings via the Ollama API.
//
// batchUnsupported records whether the connected Ollama server returns
// 404 for the batch endpoint (/api/embed, added in modern Ollama
// releases). Once set, EmbedBatch skips the batch attempt and falls
// straight back to N sequential /api/embeddings calls. Stored as an
// atomic so the worker can call EmbedBatch concurrently without a
// mutex; the worst case on first concurrent call against an old server
// is a small number of redundant 404 hits before the flag settles.
type ollamaProvider struct {
	client           *http.Client
	url              string
	model            string
	dim              int
	maxInputBytes    int
	batchUnsupported atomic.Bool
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
	if cfg.MaxInputBytes <= 0 {
		cfg.MaxInputBytes = DefaultMaxInputBytes
	}

	return &ollamaProvider{
		client:        &http.Client{Timeout: cfg.Timeout},
		url:           cfg.URL,
		model:         cfg.Model,
		dim:           DefaultDimension,
		maxInputBytes: cfg.MaxInputBytes,
	}
}

// ollamaRequest is the JSON body sent to Ollama's /api/embeddings endpoint.
// Truncate is always true so Ollama trims any residual overflow; the
// provider also caps input itself because that flag is not sufficient
// on its own (see DefaultMaxInputBytes).
type ollamaRequest struct {
	Model    string `json:"model"`
	Prompt   string `json:"prompt"`
	Truncate bool   `json:"truncate"`
}

// ollamaResponse is the JSON body returned from Ollama's /api/embeddings endpoint.
type ollamaResponse struct {
	Embedding []float64 `json:"embedding"`
}

// ollamaBatchRequest is the JSON body sent to Ollama's batch /api/embed
// endpoint. Note the field name shift: the singular endpoint uses
// "prompt" with a string value, the batch endpoint uses "input" with
// either a string OR an array of strings. We always send the array
// form so a one-element batch and an N-element batch take the same
// code path.
type ollamaBatchRequest struct {
	Model    string   `json:"model"`
	Input    []string `json:"input"`
	Truncate bool     `json:"truncate"`
}

// ollamaBatchResponse is the JSON body returned from Ollama's batch
// /api/embed endpoint. The vectors come back in the same order as the
// input array.
type ollamaBatchResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// capForEmbedding truncates s to at most maxBytes bytes, backing off to
// the nearest UTF-8 rune boundary so a multi-byte rune is never split.
// It reports whether truncation occurred. A non-positive maxBytes (or an
// input already within budget) returns s unchanged.
func capForEmbedding(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// Embed generates an embedding for a single text input.
func (o *ollamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	text, truncated := capForEmbedding(text, o.maxInputBytes)
	if truncated {
		slog.Warn("ollama: embedding input truncated to fit the input budget; embedded text is trimmed (stored content is unaffected)",
			"max_bytes", o.maxInputBytes, "model", o.model,
		)
	}
	body, err := json.Marshal(ollamaRequest{
		Model:    o.model,
		Prompt:   text,
		Truncate: true,
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

// EmbedBatch generates embeddings for multiple text inputs in a single
// HTTP call against Ollama's batch /api/embed endpoint. On servers
// that predate the batch endpoint (HTTP 404), it transparently falls
// back to N sequential /api/embeddings calls and records the
// fallback so subsequent batches skip the batch attempt.
//
// The fallback path keeps the same N-sequential-call shape as before
// the batch endpoint existed, so older Ollama deployments keep working;
// the win is that modern deployments stop paying N round-trips per batch.
// Both paths apply the same input cap and truncate flag (see Embed and
// embedBatchOnce), so the fallback is not a bypass for the #623 fix.
func (o *ollamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if o.batchUnsupported.Load() {
		return o.embedBatchSequential(ctx, texts)
	}
	results, fallback, err := o.embedBatchOnce(ctx, texts)
	if fallback {
		return o.embedBatchSequential(ctx, texts)
	}
	if err != nil {
		return nil, err
	}
	return results, nil
}

// embedBatchOnce makes a single POST to /api/embed. Returns
// (results, false, nil) on success, (nil, true, nil) when the batch
// endpoint is unavailable on this server (caller should fall back),
// and (nil, false, err) for any other failure.
func (o *ollamaProvider) embedBatchOnce(ctx context.Context, texts []string) (results [][]float32, fallback bool, err error) {
	capped := make([]string, len(texts))
	truncatedCount := 0
	for i, t := range texts {
		c, truncated := capForEmbedding(t, o.maxInputBytes)
		capped[i] = c
		if truncated {
			truncatedCount++
		}
	}
	body, err := json.Marshal(ollamaBatchRequest{Model: o.model, Input: capped, Truncate: true})
	if err != nil {
		return nil, false, fmt.Errorf("marshaling batch request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("creating batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("calling Ollama batch embed API: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	if resp.StatusCode == http.StatusNotFound {
		o.batchUnsupported.Store(true)
		slog.Warn("ollama: /api/embed not available, falling back to sequential /api/embeddings calls (recommend upgrading the ollama server for substantially faster batch embedding)",
			"url", o.url, "model", o.model,
		)
		return nil, true, nil
	}
	// Warn only once we know we are not falling back: the sequential
	// path warns per item itself, so warning here too would double-log.
	if truncatedCount > 0 {
		slog.Warn("ollama: embedding inputs truncated to fit the input budget; embedded text is trimmed (stored content is unaffected)",
			"truncated", truncatedCount, "batch_size", len(texts), "max_bytes", o.maxInputBytes, "model", o.model,
		)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, false, fmt.Errorf("ollama batch API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, false, fmt.Errorf("decoding Ollama batch response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, false, fmt.Errorf("ollama batch returned %d embeddings for %d inputs", len(result.Embeddings), len(texts))
	}

	results = make([][]float32, len(result.Embeddings))
	for i, emb := range result.Embeddings {
		results[i] = toFloat32(emb)
	}
	return results, false, nil
}

// embedBatchSequential is the pre-fix code path: N round-trips to
// /api/embeddings. Used as the 404 fallback for older Ollama servers
// that lack the batch endpoint.
func (o *ollamaProvider) embedBatchSequential(ctx context.Context, texts []string) ([][]float32, error) {
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

// Model returns the configured Ollama model name. Callers reach
// for this via a `Model() string` type assertion when they need a
// row-level breadcrumb of which model produced a stored vector
// (today: the api_catalog_operation_embeddings row metadata).
// Kept off the Provider interface so adding a new provider doesn't
// drag a method that's only meaningful for back-end-named providers.
func (o *ollamaProvider) Model() string {
	return o.model
}

// Kind returns the Ollama kind identifier so callers can distinguish
// this real, network-backed provider from the noop placeholder.
func (*ollamaProvider) Kind() string { return KindOllama }

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
