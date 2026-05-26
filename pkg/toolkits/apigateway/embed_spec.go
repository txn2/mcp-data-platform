package apigateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// ComputeRequest bundles the parameters ComputeOperationEmbeddings
// needs. The struct shape keeps the function under the project's
// argument-limit lint ceiling once batch-size and per-batch
// persistence joined the parameter set for #479.
type ComputeRequest struct {
	// Embedder is the upstream provider. nil short-circuits the
	// whole function (no error, empty result) so callers can pass
	// the platform embedder unconditionally.
	Embedder embedding.Provider

	// Content is the raw OpenAPI document text.
	Content string

	// SpecName is the catalog key used for operation IDs and for
	// any per-spec routing inside the persist callback.
	SpecName string

	// Existing is the dedup map ListExisting returned. Empty map
	// (or nil) forces every operation through the embedder.
	Existing map[string]catalog.OperationEmbedding

	// BatchSize caps the number of texts per upstream EmbedBatch
	// call. Zero falls back to the whole remainder, which is
	// equivalent to "no batching" — only suitable for tiny specs
	// or test stubs.
	BatchSize int

	// Progress is called at every chunk boundary with the
	// cumulative count of operations whose vectors are ready
	// (reused + freshly embedded). nil disables.
	Progress func(completed int)

	// PersistBatch is called after every successful chunk with
	// just that chunk's rows so the embed-jobs worker can write
	// progress to durable storage incrementally. nil disables —
	// callers that do their own atomic-replace pass at the end
	// (e.g. tests) leave this nil.
	PersistBatch func(rows []catalog.OperationEmbedding) error
}

// ComputeOperationEmbeddings parses content as an OpenAPI document,
// walks its operations, and returns one catalog.OperationEmbedding
// per operation. For an operation whose text hashes to a value
// already present in req.Existing, the existing row's Embedding is
// reused (no provider call) on a spec refresh. Returns nil when
// req.Embedder is nil (embedding-less deployment) or when the spec
// parses to zero operations.
//
// The embed text is built with an empty base path so a base_path
// change does not invalidate every vector. Operators tweaking the
// per-spec prefix shouldn't trigger a full re-embed pass.
//
// req.Progress is invoked at chunk boundaries during the fresh-
// embed pass with the cumulative number of operations whose vectors
// are ready (reused rows are counted up front; freshly-embedded
// rows are added per chunk). The embed-jobs worker uses this to
// publish embedded_so_far to the job row so the catalog status
// endpoint can render incremental progress while the final atomic
// upsert is still pending (#430). nil disables the callback for
// non-worker call sites that never see the long-running path.
//
// req.PersistBatch is invoked after every successful chunk with
// just that chunk's rows. The embed-jobs worker writes these to
// api_catalog_operation_embeddings immediately so progress
// survives a mid-job failure: the next attempt's ListExisting
// pass picks the persisted rows up via the dedup map and skips
// the upstream call. Without this, a 5-batch job that fails on
// the 4th batch throws away batches 1-3 and the next attempt
// redoes the same work — the doom loop described in #479.
//
// Called by the admin handler after every spec upsert / upload /
// refresh / clone. Failures from the upstream embedder are
// non-fatal at the call site: the spec write has already
// succeeded; an embedding compute failure just means semantic
// ranking falls back to lexical until the operator runs the
// re-embed admin endpoint.
func ComputeOperationEmbeddings(ctx context.Context, req ComputeRequest) ([]catalog.OperationEmbedding, error) {
	if req.Embedder == nil {
		return nil, nil
	}
	doc, err := parseOpenAPISpec(req.Content)
	if err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	ops, texts := buildOperationIndex(doc, req.SpecName, "")
	if len(ops) == 0 {
		return nil, nil
	}
	model := providerModel(req.Embedder)
	dim := req.Embedder.Dimension()
	rows, toEmbedIdx, toEmbedTexts := planEmbeddingRows(ops, texts, req.Existing, model, dim)
	reused := len(rows) - len(toEmbedIdx)
	// Publish the initial reused-count so a refresh on a fully
	// cached spec ticks straight to operation_count without waiting
	// for the embedding pass (which has nothing to do).
	if req.Progress != nil {
		req.Progress(reused)
	}
	if err := fillFreshEmbeddings(ctx, fillRequest{
		embedder:     req.Embedder,
		rows:         rows,
		toEmbedIdx:   toEmbedIdx,
		toEmbedTexts: toEmbedTexts,
		reusedBase:   reused,
		batchSize:    req.BatchSize,
		progress:     req.Progress,
		persistBatch: req.PersistBatch,
	}); err != nil {
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

// fillRequest bundles the parameters fillFreshEmbeddings needs.
// Same shape rationale as ComputeRequest — the per-batch persist
// callback pushed the parameter count over the project's
// argument-limit lint ceiling.
type fillRequest struct {
	embedder     embedding.Provider
	rows         []catalog.OperationEmbedding
	toEmbedIdx   []int
	toEmbedTexts []string
	reusedBase   int
	batchSize    int
	progress     func(completed int)
	persistBatch func(rows []catalog.OperationEmbedding) error
}

// fillFreshEmbeddings calls embedder for the deltas only, writes
// each vector back into its row, and (when persistBatch is set)
// hands each batch's rows to the caller for durable storage before
// moving to the next chunk. No-op when there is nothing to embed.
//
// req.reusedBase is the count of operations whose vectors were
// reused from the existing map (and therefore already published
// via the initial progress callback in ComputeOperationEmbeddings).
// The per-chunk progress publish adds the count of freshly-
// embedded operations to this base so the caller sees a cumulative
// "operations ready" counter across the whole spec.
//
// req.persistBatch lets the embed-jobs worker UPSERT each batch's
// rows immediately, so a job that fails on batch N still has
// vectors for batches 0..N-1 persisted. The next attempt's
// ListExisting pass picks them up via the dedup map and skips
// the upstream call. nil persistBatch falls back to the prior
// behavior (write nothing until the caller's final Upsert) —
// suitable for tests and any call site that does its own atomic
// replace.
func fillFreshEmbeddings(ctx context.Context, req fillRequest) error {
	if len(req.toEmbedTexts) == 0 {
		return nil
	}
	batchSize := req.batchSize
	if batchSize <= 0 {
		batchSize = len(req.toEmbedTexts)
	}
	if err := embedInBatchesIter(ctx, req.embedder, req.toEmbedTexts, batchSize,
		func(start, end int, vectors [][]float32) error {
			chunkIdx := req.toEmbedIdx[start:end]
			batch := make([]catalog.OperationEmbedding, 0, len(chunkIdx))
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
		}); err != nil {
		return fmt.Errorf("embed operation batch: %w", err)
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
