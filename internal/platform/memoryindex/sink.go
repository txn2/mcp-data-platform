package memoryindex

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Sink implements indexjobs.Sink for the memory kind over the embedding
// columns of memory_records. currentModel is the provider model the gap
// query diffs stored rows against, so a model swap re-embeds rows stamped
// with the previous model.
type Sink struct {
	store        *Store
	currentModel string
}

// NewSink returns a Sink backed by the given store. currentModel is the
// embedding provider's model identifier (embedding.ModelName); pass "" on
// a deployment whose provider does not name its model; every row then
// matches "" and only NULL-embedding rows are treated as gaps.
func NewSink(store *Store, currentModel string) *Sink {
	return &Sink{store: store, currentModel: currentModel}
}

// Compile-time interface checks.
var (
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)

// Kind reports the memory source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the record's persisted vector keyed by item id for
// the worker's dedup pass.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx, key.SourceID)
}

// Upsert writes the record's vector. The memory unit holds one item and
// has no sibling rows, so there is nothing to delete; it delegates to the
// shared store write.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// UpsertBatch is identical to Upsert for memory (single-item unit, no
// rows outside the batch to preserve).
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// StampExpected is a no-op for memory. Gap detection is condition-based
// (embedding IS NULL OR model mismatch), not count-based, so there is no
// expected count to record per unit.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps returns active record ids whose embedding is missing or was
// produced by a model other than the current one.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.FindGaps(ctx, s.currentModel)
}

// Coverage reports the memory kind's indexed-vs-expected record totals
// (records with an embedding vs all active records). ExpectedKnown is
// true: every active record is expected to converge to one vector.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, expected, err := s.store.Coverage(ctx)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}
