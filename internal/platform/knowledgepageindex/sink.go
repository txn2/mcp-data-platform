package knowledgepageindex

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Sink implements indexjobs.Sink for the portal-knowledge-pages kind over the
// page's embedding-chunk table. currentModel is the provider model the gap query
// diffs stored pages against, so a model swap re-embeds pages stamped with the
// previous model.
type Sink struct {
	store        *Store
	currentModel string
}

// NewSink returns a Sink backed by the given store. currentModel is the
// embedding provider's model identifier (embedding.ModelName); pass "" on a
// deployment whose provider does not name its model, in which case a page
// converges once it has been through the worker at all.
func NewSink(store *Store, currentModel string) *Sink {
	return &Sink{store: store, currentModel: currentModel}
}

// Compile-time interface checks.
var (
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)

// Kind reports the portal-knowledge-pages source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the page's persisted chunk vectors keyed by item id for
// the worker's dedup pass, so an edit re-embeds only the chunks whose text moved.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx, key.SourceID)
}

// Upsert replaces the page's chunk set with the supplied rows, pruning any chunk
// the page's current text no longer produces.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.ReplaceVectors(ctx, key.SourceID, rows)
}

// UpsertBatch writes one chunk of the embed pass in place, leaving the page's
// other chunk rows untouched so partial progress survives a mid-pass failure.
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// StampExpected marks the page's chunk set as produced by the current model. The
// count is not stored: gap detection is condition-based (the page's marker versus
// the current model), not a count comparison, because the number of chunks a page
// produces is a function of its text and the provider's budget, not a target the
// reconciler could independently derive.
//
// The worker calls this only after a successful embed pass, which is exactly the
// convergence signal this marker records; a failure here is non-fatal (the next
// sweep re-enqueues the page and its unchanged chunks are reused by the dedup
// pass, so the retry costs no provider calls).
func (s *Sink) StampExpected(ctx context.Context, key indexjobs.Key, _ int) error {
	return s.store.StampModel(ctx, key.SourceID, s.currentModel)
}

// FindGaps returns the indexable page ids whose chunk set is missing or was
// produced by a model other than the current one.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.FindGaps(ctx, s.currentModel)
}

// Coverage reports the portal-knowledge-pages kind's indexed-vs-expected totals
// (pages converged on the current model vs all indexable pages). ExpectedKnown is
// true: every indexable page is expected to converge to a chunk set.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, expected, err := s.store.Coverage(ctx, s.currentModel)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}
