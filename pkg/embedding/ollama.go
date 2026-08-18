package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

// MinInputBytes is the floor the adaptive input bound stops shrinking
// at. Below this, a refusal is no longer plausibly about the text's
// length: a model whose context cannot hold 256 bytes is misconfigured,
// and shrinking further would embed a fragment too small to carry
// meaning. The provider surfaces the error instead.
const MinInputBytes = 256

// ErrInputTooLarge reports that the provider refused a text because it
// does not fit the model's context window.
//
// It is separated from a generic provider failure because it is
// deterministic: the same bytes are refused on every attempt, so a caller
// that re-sends them unchanged fails identically forever. Callers either
// bound the text and try again (which this provider does itself, see
// Embed) or stop re-queueing the unit.
var ErrInputTooLarge = errors.New("embedding: input exceeds the model context length")

// contextLengthMarkers are the substrings an Ollama 400 body uses to say
// an input does not fit the model's context (observed: "the input length
// exceeds the context length"). Matched case-insensitively, and only on a
// 400, so a 5xx or a transport failure stays a generic retryable error.
//
// The wording is matched rather than compared because it is not part of
// Ollama's API contract and has varied across releases. The asymmetry is
// deliberate: a false positive costs a few extra calls at smaller bounds
// before the original error surfaces unchanged, while a false negative
// restores the endless identical failure this classification exists to
// end.
var contextLengthMarkers = []string{
	"context length",
	"context window",
	"input is too large",
}

// isContextLengthError reports whether an Ollama error response says the
// input overflowed the model's context.
func isContextLengthError(status int, body string) bool {
	if status != http.StatusBadRequest {
		return false
	}
	lower := strings.ToLower(body)
	for _, marker := range contextLengthMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

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
//
// The configured byte cap is a proxy for the model's token budget, and no
// fixed byte count can be an exact one: token density varies by close to
// an order of magnitude between prose and dense content (CSV, JSON,
// source code, base64), so a text well inside the byte cap can still
// overflow the context. When the server says so, Embed halves the bound
// and retries, down to MinInputBytes.
//
// This converges in a bounded number of calls (four from the default
// 6000) and is what keeps one dense document from failing identically on
// every attempt forever (#1350). The bound applies only to the bytes
// sent to the model; stored content is untouched.
//
// The converged bound is deliberately NOT remembered across calls. Doing
// so would look like an optimization -- a model that refuses the
// configured cap for every text makes each one pay the whole sequence --
// but density is a property of the text, not of the model: one dense CSV
// converging at 375 bytes would then truncate every prose document in the
// corpus to 375 bytes. Paying a few extra round trips on the outliers is
// the cheaper error. A model that cannot hold the configured cap at all
// is a misconfiguration, and the warning below names the model and the
// size that was refused so it can be corrected at max_input_bytes.
func (o *ollamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	budget := o.maxInputBytes
	for {
		emb, err := o.embedOnce(ctx, text, budget)
		if !errors.Is(err, ErrInputTooLarge) {
			return emb, err
		}
		sent := sentBytes(text, budget)
		next := sent / 2
		if next < MinInputBytes {
			return nil, err
		}
		// Reports what was actually put on the wire, not the budget: for a
		// text well inside an oversized budget those differ, and the sent
		// figure is the one that locates the model's real limit.
		slog.Warn("ollama: input refused as too long for the model context; retrying at a smaller bound",
			"sent_bytes", sent, "next_bytes", next, "model", o.model,
		)
		budget = next
	}
}

// sentBytes is how many bytes the last attempt actually put on the wire:
// the smaller of the budget and the text. Halving this rather than the
// budget keeps a text already well inside an oversized budget from paying
// retries that send byte-for-byte the same request.
func sentBytes(text string, budget int) int {
	if len(text) < budget {
		return len(text)
	}
	return budget
}

// embedOnce is one /api/embeddings round trip at the supplied byte bound.
func (o *ollamaProvider) embedOnce(ctx context.Context, text string, budget int) ([]float32, error) {
	text, truncated := capForEmbedding(text, budget)
	if truncated {
		slog.Warn("ollama: embedding input truncated to fit the input budget; embedded text is trimmed (stored content is unaffected)",
			"max_bytes", budget, "model", o.model,
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
		if isContextLengthError(resp.StatusCode, string(respBody)) {
			return nil, fmt.Errorf("ollama API returned status %d: %s: %w",
				resp.StatusCode, string(respBody), ErrInputTooLarge)
		}
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
	if errors.Is(err, ErrInputTooLarge) {
		// The batch endpoint reports one refusal for the whole array and
		// does not say which input overflowed, so there is nothing to
		// shrink here without shrinking every text in the batch. Re-run
		// the batch one input at a time instead: Embed applies the
		// adaptive bound per text, so only the offending one is trimmed
		// and the rest embed whole. batchUnsupported is deliberately NOT
		// set -- the endpoint is healthy, this batch's contents were not.
		slog.Warn("ollama: batch refused as too long for the model context; re-running it one input at a time",
			"batch_size", len(texts), "model", o.model,
		)
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
		if isContextLengthError(resp.StatusCode, string(respBody)) {
			return nil, false, fmt.Errorf("ollama batch API returned status %d: %s: %w",
				resp.StatusCode, string(respBody), ErrInputTooLarge)
		}
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

// MaxInputBytes returns the byte budget this provider trims each input
// to before calling Ollama. Callers reach for it via the
// `MaxInputBytes() int` type assertion (see the package-level
// MaxInputBytes helper) when they must split their own text so no piece
// is ever trimmed — the knowledge-page chunker sizes its chunks from
// this value. Kept off the Provider interface for the same reason as
// Model(): it is only meaningful for a provider that caps input.
func (o *ollamaProvider) MaxInputBytes() int { return o.maxInputBytes }

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
