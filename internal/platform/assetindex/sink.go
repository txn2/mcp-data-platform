package assetindex

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Sink implements indexjobs.Sink for the portal-assets kind over the embedding
// columns of the portal_assets table. currentModel is the provider model the
// gap query diffs stored rows against, so a model swap re-embeds rows stamped
// with the previous model.
type Sink struct {
	store        *Store
	currentModel string
}

// NewSink returns a Sink backed by the given store. currentModel is the
// embedding provider's model identifier (embedding.ModelName); pass "" on a
// deployment whose provider does not name its model, in which case every row
// matches "" and only NULL-embedding rows are treated as gaps.
func NewSink(store *Store, currentModel string) *Sink {
	return &Sink{store: store, currentModel: currentModel}
}

// Compile-time interface checks.
var (
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)

// Kind reports the portal-assets source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the asset's persisted vector keyed by item id for the
// worker's dedup pass.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx, key.SourceID)
}

// Upsert writes the asset's vector. The asset unit holds one item and has no
// sibling rows, so it delegates to the shared store write.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// UpsertBatch is identical to Upsert for assets (single-item unit, no rows
// outside the batch to preserve).
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// StampExpected is a no-op for assets. Gap detection is condition-based
// (embedding IS NULL OR model mismatch), not count-based, so there is no
// expected count to record per unit.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps returns non-deleted asset ids whose embedding is missing or was
// produced by a model other than the current one.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.FindGaps(ctx, s.currentModel)
}

// Coverage reports the portal-assets kind's indexed-vs-expected totals
// (non-deleted assets with an embedding vs all non-deleted assets).
// ExpectedKnown is true: every non-deleted asset is expected to converge to one
// vector.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, expected, err := s.store.Coverage(ctx)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}
