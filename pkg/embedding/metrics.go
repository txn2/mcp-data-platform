package embedding

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// WithMetrics wraps a provider so every call it answers is counted and timed
// under its model (embedding_calls_total{model, status},
// embedding_call_duration_seconds{model}, #1895), on the query path and the
// indexing path alike, and so a search that fell back to lexical ranking
// because the call failed is counted (embedding_fallbacks_total{model}).
// A nil recorder or a provider that is not configured is returned as it is.
func WithMetrics(p Provider, m *observability.Metrics) Provider {
	if m == nil || !IsConfigured(p) {
		return p
	}
	return &measured{Provider: p, metrics: m, model: ModelName(p)}
}

// measured records what the provider it wraps did. It keeps the optional
// surfaces (Model, MaxInputBytes) a caller reads through the Provider.
type measured struct {
	Provider
	metrics *observability.Metrics
	model   string
}

// Embed records the wrapped provider's single-text call.
func (m *measured) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	out, err := m.Provider.Embed(ctx, text)
	m.metrics.RecordEmbeddingCall(ctx, m.model, callStatus(err), time.Since(start))
	return out, err //nolint:wrapcheck // the provider's error, as it is
}

// EmbedBatch records the wrapped provider's batch call.
func (m *measured) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	start := time.Now()
	out, err := m.Provider.EmbedBatch(ctx, texts)
	m.metrics.RecordEmbeddingCall(ctx, m.model, callStatus(err), time.Since(start))
	return out, err //nolint:wrapcheck // the provider's error, as it is
}

// Model is the wrapped provider's model, so ModelName reads through.
func (m *measured) Model() string { return m.model }

// MaxInputBytes is the wrapped provider's input cap, so a chunker reads through.
func (m *measured) MaxInputBytes() int {
	if c, ok := m.Provider.(inputCapped); ok {
		return c.MaxInputBytes()
	}
	return 0
}

// recordFallback counts one search ranked lexically.
func (m *measured) recordFallback(ctx context.Context) {
	m.metrics.RecordEmbeddingFallback(ctx, m.model)
}

// fallbackCounted is what a measured provider adds: the fallback count the
// search helpers record through.
type fallbackCounted interface {
	recordFallback(ctx context.Context)
}

func callStatus(err error) string {
	if err != nil {
		return observability.StatusUpstreamErr
	}
	return observability.StatusOK
}

// countFallback counts a search's fall back to lexical ranking when the
// provider is measured.
func countFallback(ctx context.Context, p Provider) {
	if f, ok := p.(fallbackCounted); ok {
		f.recordFallback(ctx)
	}
}
