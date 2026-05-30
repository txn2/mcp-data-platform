package catalogindex

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// Sink implements indexjobs.Sink for the api-catalog kind against
// the existing api_catalog_operation_embeddings table (via
// catalog.Store) and api_catalog_specs.operation_count (the
// expected-count breadcrumb). Coexistence with the framework's
// generic vector tables is the point: the api-catalog vector table
// keeps its FK + ON DELETE CASCADE to api_catalog_specs untouched,
// so spec deletion still cascades to its vectors and no expensive
// embedding data is migrated.
type Sink struct {
	store catalog.Store
}

// NewSink returns a Sink backed by the given catalog store.
func NewSink(store catalog.Store) *Sink {
	return &Sink{store: store}
}

// Compile-time interface check.
var _ indexjobs.Sink = (*Sink)(nil)

// Kind reports the api-catalog source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the persisted vectors for the unit keyed by
// operation id, for the worker's dedup pass.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	catalogID, specName, ok := DecodeSourceID(key.SourceID)
	if !ok {
		return nil, fmt.Errorf("catalogindex: list existing: malformed source_id %q", key.SourceID)
	}
	rows, err := s.store.ListOperationEmbeddings(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogindex: list existing: %w", err)
	}
	out := make(map[string]indexjobs.Vector, len(rows))
	for _, r := range rows {
		out[r.OperationID] = toVector(r)
	}
	return out, nil
}

// Upsert atomically replaces every vector for the unit with the
// supplied set (delete-absent + insert), so a reindex that drops
// operations removes their stale vectors.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	catalogID, specName, ok := DecodeSourceID(key.SourceID)
	if !ok {
		return fmt.Errorf("catalogindex: upsert: malformed source_id %q", key.SourceID)
	}
	if err := s.store.UpsertOperationEmbeddings(ctx, catalogID, specName, toEmbeddings(rows)); err != nil {
		return fmt.Errorf("catalogindex: upsert: %w", err)
	}
	return nil
}

// UpsertBatch writes a single chunk's vectors in place without
// deleting rows outside the batch (incremental progress).
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	catalogID, specName, ok := DecodeSourceID(key.SourceID)
	if !ok {
		return fmt.Errorf("catalogindex: upsert batch: malformed source_id %q", key.SourceID)
	}
	if err := s.store.UpsertOperationEmbeddingsBatch(ctx, catalogID, specName, toEmbeddings(rows)); err != nil {
		return fmt.Errorf("catalogindex: upsert batch: %w", err)
	}
	return nil
}

// StampExpected records the unit's operation count on the spec row
// so the reconciler's gap predicate has a target.
func (s *Sink) StampExpected(ctx context.Context, key indexjobs.Key, count int) error {
	catalogID, specName, ok := DecodeSourceID(key.SourceID)
	if !ok {
		return fmt.Errorf("catalogindex: stamp expected: malformed source_id %q", key.SourceID)
	}
	if err := s.store.SetOperationCount(ctx, catalogID, specName, count); err != nil {
		return fmt.Errorf("catalogindex: stamp expected: %w", err)
	}
	return nil
}

// FindGaps returns the source ids whose operation_count disagrees
// with their persisted vector count, encoded for the reconciler to
// enqueue.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	gaps, err := s.store.ListEmbeddingGaps(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalogindex: find gaps: %w", err)
	}
	out := make([]string, len(gaps))
	for i, g := range gaps {
		out[i] = EncodeSourceID(g.CatalogID, g.SpecName)
	}
	return out, nil
}

// toVector maps a catalog embedding row to a framework vector.
func toVector(r catalog.OperationEmbedding) indexjobs.Vector {
	return indexjobs.Vector{
		ItemID:    r.OperationID,
		TextHash:  r.TextHash,
		Embedding: r.Embedding,
		Model:     r.Model,
		Dim:       r.Dim,
	}
}

// toEmbeddings maps framework vectors back to catalog embedding rows.
func toEmbeddings(rows []indexjobs.Vector) []catalog.OperationEmbedding {
	out := make([]catalog.OperationEmbedding, len(rows))
	for i, v := range rows {
		out[i] = catalog.OperationEmbedding{
			OperationID: v.ItemID,
			TextHash:    v.TextHash,
			Embedding:   v.Embedding,
			Model:       v.Model,
			Dim:         v.Dim,
		}
	}
	return out
}
