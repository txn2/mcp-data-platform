// Package embedding provides text embedding generation for memory vector search.
package embedding

import "context"

// DefaultDimension is the default embedding dimensionality (nomic-embed-text).
const DefaultDimension = 768

// DefaultTimeout is the default HTTP timeout in seconds for embedding
// API calls. Tuned for the singular /api/embeddings path (one text per
// call), where a CPU-only Ollama with nomic-embed-text typically returns
// in 1-3 seconds; 30s is a generous ceiling for transient slowness on
// the request path. Synchronous request-path callers (memory_recall,
// memory_manage, knowledge capture_insight, apigateway query-vector)
// share this default so a wedged Ollama fails the tool call at 30s
// instead of holding an MCP request handler open for minutes.
//
// The batched /api/embed path used by the api-gateway embed-jobs worker
// needs a much higher ceiling (CPU-only Ollama on a 32-text batch can
// take 60+ seconds). The worker constructs its own Provider with a
// longer timeout from apigateway.embed_jobs.embed_timeout — see
// pkg/platform/apigateway_embed_jobs.go. The default here intentionally
// does NOT cover the batch case so request-path consumers are not
// caught up in the worker's longer budget (#445).
const DefaultTimeout = 30

// Kind values for Provider.Kind. Used by callers (platform wiring,
// toolkit write paths) to distinguish a real, network-backed embedder
// from the placeholder noop. A noop returns zero vectors with no
// error, which is indistinguishable from a "real" embedding at the
// Embed/EmbedBatch contract level; without an explicit kind, downstream
// consumers cannot tell whether the vectors they hold are meaningful.
const (
	// KindNoop identifies the placeholder provider returned when no
	// embedder is configured. Callers MUST treat KindNoop as
	// "embedding unavailable" and refuse to persist vectors from it.
	KindNoop = "noop"

	// KindOllama identifies the Ollama-backed provider.
	KindOllama = "ollama"
)

// IsConfigured reports whether p is a real, configured embedding
// provider whose vectors are safe to persist. Returns false for nil
// and for the noop placeholder. Used by the platform wiring layer
// and toolkit write paths as a single-line guard.
func IsConfigured(p Provider) bool {
	if p == nil {
		return false
	}
	return p.Kind() != KindNoop
}

// Provider generates vector embeddings from text.
type Provider interface {
	// Embed generates an embedding vector for a single text input.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embedding vectors for multiple text inputs.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the dimensionality of the generated embeddings.
	Dimension() int

	// Kind returns a short identifier for the provider implementation
	// (KindOllama, KindNoop, ...). Callers use this to refuse to
	// persist vectors from the noop placeholder without depending on
	// concrete type assertions.
	Kind() string
}
