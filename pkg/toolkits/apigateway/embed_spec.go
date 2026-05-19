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
// reused (no provider call) on a spec refresh. Returns nil when
// embedder is nil (embedding-less deployment) or when the spec
// parses to zero operations.
//
// The embed text is built with an empty base path so a base_path
// change does not invalidate every vector. Operators tweaking the
// per-spec prefix shouldn't trigger a full re-embed pass.
//
// progress is invoked at chunk boundaries during the fresh-embed
// pass with the cumulative number of operations whose vectors are
// ready (reused rows are counted up front; freshly-embedded rows
// are added per chunk). The embed-jobs worker uses this to publish
// embedded_so_far to the job row so the catalog status endpoint
// can render incremental progress while the final atomic upsert is
// still pending (#430). nil progress disables the callback for
// non-worker call sites (admin handler invocations that never see
// the long-running path).
//
// Called by the admin handler after every spec upsert / upload /
// refresh / clone. Failures are non-fatal at the call site: the
// spec write has already succeeded; an embedding compute failure
// just means semantic ranking falls back to lexical until the
// operator runs the re-embed admin endpoint.
//
//nolint:revive // argument-limit: orchestration entry point; each arg is atomic (ctx, embedder, two distinct strings, dedup map, progress callback) and bundling would only push the same surface into a struct.
func ComputeOperationEmbeddings(ctx context.Context, embedder embedding.Provider, content, specName string, existing map[string]catalog.OperationEmbedding, progress func(completed int)) ([]catalog.OperationEmbedding, error) {
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
	rows, toEmbedIdx, toEmbedTexts := planEmbeddingRows(ops, texts, existing, model, dim)
	reused := len(rows) - len(toEmbedIdx)
	// Publish the initial reused-count so a refresh on a fully
	// cached spec ticks straight to operation_count without waiting
	// for the embedding pass (which has nothing to do).
	if progress != nil {
		progress(reused)
	}
	if err := fillFreshEmbeddings(ctx, embedder, rows, toEmbedIdx, toEmbedTexts, reused, progress); err != nil {
		return nil, err
	}
	return rows, nil
}

// planEmbeddingRows builds one OperationEmbedding per operation,
// reusing existing[opID].Embedding when text hash, dimension, AND
// model identity all match. The reuse predicate keeps a model
// swap (e.g., nomic-embed-text → bge-base-en) from leaving old
// vectors stamped with the new model name, which would defeat
// the model column's row-level-breadcrumb role. Returns rows
// (some with Embedding set from existing, the rest empty) plus
// the indices and texts that still need a fresh embed call.
func planEmbeddingRows(ops []OperationSummary, texts []string, existing map[string]catalog.OperationEmbedding, model string, dim int) (rows []catalog.OperationEmbedding, toEmbedIdx []int, toEmbedTexts []string) {
	rows = make([]catalog.OperationEmbedding, len(ops))
	for i, op := range ops {
		sum := sha256.Sum256([]byte(texts[i]))
		row := catalog.OperationEmbedding{
			OperationID: op.OperationID,
			TextHash:    sum[:],
			Model:       model,
			Dim:         dim,
		}
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
	return rows, toEmbedIdx, toEmbedTexts
}

// fillFreshEmbeddings calls embedder for the deltas only and
// writes each vector back into its row. No-op when toEmbedTexts
// is empty (every operation's vector was reused from existing).
//
// reusedBase is the count of operations whose vectors were reused
// from the existing map (and therefore already published via the
// initial progress callback). The per-chunk progress publish adds
// the count of freshly-embedded operations to this base so the
// caller sees a cumulative "operations ready" counter across the
// whole spec, not just the fresh-embed portion.
//
//nolint:revive // argument-limit: internal helper called from one site; splitting would either duplicate the (rows, toEmbedIdx, toEmbedTexts) trio that ComputeOperationEmbeddings already produces together, or move the worker-progress wiring further from its consumer. Each arg is distinct.
func fillFreshEmbeddings(ctx context.Context, embedder embedding.Provider, rows []catalog.OperationEmbedding, toEmbedIdx []int, toEmbedTexts []string, reusedBase int, progress func(completed int)) error {
	if len(toEmbedTexts) == 0 {
		return nil
	}
	vectors, err := embedInBatches(ctx, embedder, toEmbedTexts, embedBatchSize, func(freshDone int) {
		if progress != nil {
			progress(reusedBase + freshDone)
		}
	})
	if err != nil {
		return fmt.Errorf("embed operation batch: %w", err)
	}
	if len(vectors) != len(toEmbedTexts) {
		return fmt.Errorf("embed operation batch: provider returned %d vectors for %d texts",
			len(vectors), len(toEmbedTexts))
	}
	for j, idx := range toEmbedIdx {
		rows[idx].Embedding = vectors[j]
	}
	return nil
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
