package scriptindex

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Sink implements indexjobs.Sink for the scripts kind over the script's chunk
// table. currentModel is the provider model the gap query diffs stored chunk
// sets against, so a model swap re-embeds scripts built by the previous one.
type Sink struct {
	store        *Store
	currentModel string
}

// NewSink returns a Sink backed by the given store. currentModel is the
// embedding provider's model identifier (embedding.ModelName); "" on a
// provider that does not name its model.
func NewSink(store *Store, currentModel string) *Sink {
	return &Sink{store: store, currentModel: currentModel}
}

// Compile-time interface checks.
var (
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)

// Kind reports the scripts source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the script's chunk vectors keyed by item id for the
// worker's dedup pass, so an edit re-embeds only the chunks whose text moved.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx, key.SourceID)
}

// Upsert replaces the script's chunk set with the supplied rows, removing any
// chunk its current text no longer produces.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.ReplaceVectors(ctx, key.SourceID, rows)
}

// UpsertBatch writes one batch of the embed pass in place, leaving the
// script's other chunks alone so partial progress survives a failure.
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// StampExpected records that the script's chunk set was built by the current
// model from the text LoadItems read. The worker calls it only after a
// successful pass; a failure here is non-fatal, since the next sweep finds the
// script again and the dedup pass reuses its unchanged chunks.
func (s *Sink) StampExpected(ctx context.Context, key indexjobs.Key, _ int) error {
	return s.store.Stamp(ctx, key.SourceID, s.currentModel)
}

// FindGaps returns enabled script ids whose chunk set is missing, was built
// from text the script no longer has, or was built by another model.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.FindGaps(ctx, s.currentModel)
}

// Coverage reports enabled scripts whose chunk set is current against all
// enabled scripts. ExpectedKnown is true: every enabled script converges.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, expected, err := s.store.Coverage(ctx, s.currentModel)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}
