package apigateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// ComputeOperationEmbeddings parses content as an OpenAPI document,
// walks its operations, and returns one catalog.OperationEmbedding
// per operation. For an operation whose text hashes to a value
// already present in existing, the existing row's Embedding is
// reused — skipping the provider call for unchanged operations on
// a spec refresh. Returns nil when embedder is nil (embedding-less
// deployment) or when the spec parses to zero operations.
//
// The embed text is built with an empty base path so a base_path
// change does not invalidate every vector. Operators tweaking the
// per-spec prefix shouldn't trigger a full re-embed pass.
//
// Called by the admin handler after every spec upsert / upload /
// refresh / clone. Failures are non-fatal at the call site: the
// spec write has already succeeded; an embedding compute failure
// just means semantic ranking falls back to lexical until the
// operator runs the re-embed admin endpoint.
func ComputeOperationEmbeddings(ctx context.Context, embedder embedding.Provider, content, specName string, existing map[string]catalog.OperationEmbedding) ([]catalog.OperationEmbedding, error) {
	if embedder == nil {
		return nil, nil
	}
	doc, err := parseOpenAPISpec(content)
	if err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	ops, texts := buildOperationIndex(doc, specName, "")
	if len(ops) == 0 {
		return nil, nil
	}
	model := providerModel(embedder)
	dim := embedder.Dimension()
	rows := make([]catalog.OperationEmbedding, len(ops))
	var toEmbedIdx []int
	var toEmbedTexts []string
	for i, op := range ops {
		sum := sha256.Sum256([]byte(texts[i]))
		row := catalog.OperationEmbedding{
			OperationID: op.OperationID,
			TextHash:    sum[:],
			Model:       model,
			Dim:         dim,
		}
		// Reuse the existing vector only when text hash, dimension,
		// AND model identity all match. A model swap to a different
		// 768-dim provider (e.g., nomic-embed-text → bge-base-en)
		// would otherwise keep the old vector while stamping the
		// new model name, defeating the model-column's role as a
		// row-level breadcrumb that the cached vectors match the
		// current provider's output.
		if prev, ok := existing[op.OperationID]; ok &&
			bytes.Equal(prev.TextHash, row.TextHash) &&
			len(prev.Embedding) == dim &&
			prev.Model == model {
			row.Embedding = prev.Embedding
		} else {
			toEmbedIdx = append(toEmbedIdx, i)
			toEmbedTexts = append(toEmbedTexts, texts[i])
		}
		rows[i] = row
	}
	if len(toEmbedTexts) > 0 {
		vectors, err := embedInBatches(ctx, embedder, toEmbedTexts, embedBatchSize)
		if err != nil {
			return nil, fmt.Errorf("embed operation batch: %w", err)
		}
		if len(vectors) != len(toEmbedTexts) {
			return nil, fmt.Errorf("embed operation batch: provider returned %d vectors for %d texts",
				len(vectors), len(toEmbedTexts))
		}
		for j, idx := range toEmbedIdx {
			rows[idx].Embedding = vectors[j]
		}
	}
	return rows, nil
}

// providerModel returns the embedding provider's underlying model
// identifier when the implementation exposes it. Provider is a
// minimal interface that does not require Model(); concrete
// implementations like ollamaProvider satisfy the optional
// modelNamed interface, while noopProvider does not. Empty string
// when the provider does not expose a model name.
func providerModel(p embedding.Provider) string {
	if m, ok := p.(modelNamed); ok {
		return m.Model()
	}
	return ""
}

// modelNamed is the optional interface concrete embedding providers
// implement to expose their underlying model identifier. Kept off
// embedding.Provider so adding a new provider does not require a
// Model() method that may not be meaningful (e.g., the noop
// provider has no model to name).
type modelNamed interface {
	Model() string
}
