// Package embedding provides text embedding generation for memory vector search.
package embedding

import "context"

// DefaultDimension is the default embedding dimensionality (nomic-embed-text).
const DefaultDimension = 768

// DefaultTimeout is the default HTTP timeout in seconds for embedding API calls.
const DefaultTimeout = 30

// Provider generates vector embeddings from text.
type Provider interface {
	// Embed generates an embedding vector for a single text input.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embedding vectors for multiple text inputs.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the dimensionality of the generated embeddings.
	Dimension() int
}
