package toolsindex

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Sink implements indexjobs.Sink for the tools kind over the
// tool_embeddings table (vectors) and index_sources (expected count).
// The key's SourceID is used verbatim as the tool_embeddings source_id;
// there is no composite encoding because, unlike api-catalog, the tool
// corpus is a single flat set per source.
type Sink struct {
	store *Store
}

// NewSink returns a Sink backed by the given store.
func NewSink(store *Store) *Sink { return &Sink{store: store} }

// Compile-time interface checks.
var (
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)

// Kind reports the tools source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the persisted vectors keyed by tool name for the
// worker's dedup pass.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx, key.SourceID)
}

// Upsert atomically replaces the full vector set for the source, so a
// tool dropped from the registry has its stale vector removed.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.Replace(ctx, key.SourceID, rows)
}

// UpsertBatch writes one chunk in place without disturbing rows outside
// it (the worker's incremental progress persistence).
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertBatch(ctx, key.SourceID, rows)
}

// Coverage reports the number of indexed tool vectors. ExpectedKnown
// is false: the tools kind stamps no expected count (it re-syncs the
// live registry every sweep), so the dashboard shows a sync indicator
// from the latest job status rather than an indexed/expected ratio.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, err := s.store.Coverage(ctx)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, ExpectedKnown: false}, nil
}

// StampExpected is a no-op for the tools kind. The framework calls it
// after a successful embed to record an expected item count for
// count-based gap detection, but tools detects gaps by always
// re-syncing (see Store.FindGaps), so there is no count to record.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps returns the source ids whose expected count and persisted
// vector count disagree.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.FindGaps(ctx)
}
