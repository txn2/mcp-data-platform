package embedding

import "context"

// noopProvider returns zero-value embeddings for testing or when no embedding
// service is available.
type noopProvider struct {
	dim int
}

// NewNoopProvider creates a no-op embedding provider.
func NewNoopProvider(dim int) Provider {
	if dim <= 0 {
		dim = DefaultDimension
	}
	return &noopProvider{dim: dim}
}

// Embed returns a zero vector.
func (n *noopProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, n.dim), nil
}

// EmbedBatch returns zero vectors for each input.
func (n *noopProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i := range texts {
		results[i] = make([]float32, n.dim)
	}
	return results, nil
}

// Dimension returns the configured dimensionality.
func (n *noopProvider) Dimension() int {
	return n.dim
}

// Verify interface compliance.
var _ Provider = (*noopProvider)(nil)
