package indexjobs

import (
	"bytes"
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// providerModel returns the embedding provider's underlying model
// identifier when the implementation exposes it, else "". It delegates
// to embedding.ModelName so the worker's dedup model-identity and the
// memory write-path's stamped model are read through one definition, not
// two that can drift.
func providerModel(p embedding.Provider) string {
	return embedding.ModelName(p)
}

// embedRequest bundles the parameters embedItems needs. The struct
// shape keeps the function under the argument-limit lint ceiling.
type embedRequest struct {
	// Embedder is the upstream provider. nil short-circuits the
	// whole function (no error, empty result) so callers can pass
	// the platform embedder unconditionally.
	embedder embedding.Provider

	// items are the embeddable units the Source loaded for the
	// unit, in stable order. Empty yields an empty result.
	items []Item

	// existing is the dedup map (item id -> persisted vector). An
	// empty map forces every item through the embedder, which is
	// what the manual-retry path passes.
	existing map[string]Vector

	// batchSize caps texts per upstream EmbedBatch call. Zero
	// embeds the whole remainder in one call.
	batchSize int

	// progress is called at every chunk boundary with the
	// cumulative count of items whose vectors are ready (reused +
	// freshly embedded). nil disables.
	progress func(completed int)

	// persistBatch is called after every successful chunk with just
	// that chunk's rows so the worker can persist progress
	// incrementally. nil disables.
	persistBatch func(rows []Vector) error
}

// embedItems plans one Vector per item, reusing a persisted vector
// when its text hash, dimension, AND model identity all match, and
// embeds the rest in batches. It is the framework-owned generalization
// of the api-catalog ComputeOperationEmbeddings pass: every consumer
// gets text-hash dedup, batched provider calls, chunk-boundary
// progress, and incremental persistence for free; the consumer only
// supplies the (item id, text) pairs via Source.LoadItems.
//
// Returns nil when the provider is nil (embedding-less deployment)
// or when items is empty.
func embedItems(ctx context.Context, req embedRequest) ([]Vector, error) {
	if req.embedder == nil || len(req.items) == 0 {
		return nil, nil
	}
	model := providerModel(req.embedder)
	dim := req.embedder.Dimension()
	rows, toEmbedIdx, toEmbedTexts := planVectors(req.items, req.existing, model, dim)
	reused := len(rows) - len(toEmbedIdx)
	// Publish the reused count up front so a reindex of a fully
	// cached unit ticks straight to its item count without waiting
	// for an embedding pass that has nothing to do.
	if req.progress != nil {
		req.progress(reused)
	}
	if err := fillFresh(ctx, fillRequest{
		embedder:     req.embedder,
		rows:         rows,
		toEmbedIdx:   toEmbedIdx,
		toEmbedTexts: toEmbedTexts,
		reusedBase:   reused,
		batchSize:    req.batchSize,
		progress:     req.progress,
		persistBatch: req.persistBatch,
	}); err != nil {
		return nil, err
	}
	return rows, nil
}

// planVectors builds one Vector per item, reusing existing[itemID]
// when text hash, dimension, AND model identity all match. The
// reuse predicate keeps a model swap from leaving old vectors
// stamped with the new model name (which would defeat the model
// column's breadcrumb role). Returns rows (some with Embedding set
// from existing, the rest empty) plus the indices and texts that
// still need a fresh embed call.
func planVectors(items []Item, existing map[string]Vector, model string, dim int) (rows []Vector, toEmbedIdx []int, toEmbedTexts []string) {
	rows = make([]Vector, len(items))
	for i, item := range items {
		row := Vector{
			ItemID:   item.ItemID,
			TextHash: TextHash(item.Text),
			Model:    model,
			Dim:      dim,
		}
		if prev, ok := existing[item.ItemID]; ok &&
			bytes.Equal(prev.TextHash, row.TextHash) &&
			len(prev.Embedding) == dim &&
			prev.Model == model {
			row.Embedding = prev.Embedding
		} else {
			toEmbedIdx = append(toEmbedIdx, i)
			toEmbedTexts = append(toEmbedTexts, item.Text)
		}
		rows[i] = row
	}
	return rows, toEmbedIdx, toEmbedTexts
}

// fillRequest bundles the parameters fillFresh needs.
type fillRequest struct {
	embedder     embedding.Provider
	rows         []Vector
	toEmbedIdx   []int
	toEmbedTexts []string
	reusedBase   int
	batchSize    int
	progress     func(completed int)
	persistBatch func(rows []Vector) error
}

// fillFresh calls the embedder for the deltas only, writes each
// vector back into its row, and (when persistBatch is set) hands
// each chunk's rows to the caller for durable storage before moving
// on. No-op when there is nothing to embed.
func fillFresh(ctx context.Context, req fillRequest) error {
	if len(req.toEmbedTexts) == 0 {
		return nil
	}
	batchSize := req.batchSize
	if batchSize <= 0 {
		batchSize = len(req.toEmbedTexts)
	}
	err := embedInBatches(ctx, req.embedder, req.toEmbedTexts, batchSize,
		func(start, end int, vectors [][]float32) error {
			chunkIdx := req.toEmbedIdx[start:end]
			batch := make([]Vector, 0, len(chunkIdx))
			for j, idx := range chunkIdx {
				req.rows[idx].Embedding = vectors[j]
				batch = append(batch, req.rows[idx])
			}
			if req.persistBatch != nil {
				if err := req.persistBatch(batch); err != nil {
					return fmt.Errorf("persist: %w", err)
				}
			}
			if req.progress != nil {
				req.progress(req.reusedBase + end)
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("embed item batch: %w", err)
	}
	return nil
}

// embedInBatches splits texts into batchSize chunks, calls
// EmbedBatch per chunk, validates the vector count, and invokes
// onChunk with the chunk's [start,end) bounds and vectors.
func embedInBatches(ctx context.Context, embedder embedding.Provider, texts []string, batchSize int, onChunk func(start, end int, vectors [][]float32) error) error {
	if batchSize <= 0 {
		batchSize = len(texts)
	}
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		chunk := texts[start:end]
		vectors, err := embedder.EmbedBatch(ctx, chunk)
		if err != nil {
			return fmt.Errorf("batch [%d:%d]: %w", start, end, err)
		}
		if len(vectors) != len(chunk) {
			return fmt.Errorf("batch [%d:%d]: provider returned %d vectors for %d texts",
				start, end, len(vectors), len(chunk))
		}
		if err := onChunk(start, end, vectors); err != nil {
			return fmt.Errorf("batch [%d:%d] callback: %w", start, end, err)
		}
	}
	return nil
}
