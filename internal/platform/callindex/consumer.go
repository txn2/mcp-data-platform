package callindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Source implements indexjobs.Source for the call kind. A unit is one recorded
// call (SourceID = record id) and yields exactly one item: the text the record
// is searched by. The worker embeds it and the Sink writes the vector back onto
// the same row.
type Source struct {
	store *Store
}

// NewSource returns a Source backed by the given store.
func NewSource(store *Store) *Source { return &Source{store: store} }

// Kind reports the call source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns the record's single embeddable item. A record that was
// deleted between enqueue and claim, or that holds nothing worth embedding,
// yields an empty slice: a clean completion that writes no vector.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	text, err := s.store.GetText(ctx, sourceID)
	if errors.Is(err, errNotIndexable) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("callSource: load items: %w", err)
	}
	return []indexjobs.Item{{ItemID: sourceID, Text: text}}, nil
}

// OnSucceeded is a no-op: search reads the vectors off call_records on every
// query, so there is no in-memory cache to refresh after a backfill.
func (*Source) OnSucceeded(string) {}

// Sink implements indexjobs.Sink for the call kind over the embedding columns
// of call_records. currentModel is the provider model the gap query diffs
// stored rows against, so a model swap re-embeds rows stamped with the previous
// model.
type Sink struct {
	store        *Store
	currentModel string
}

// NewSink returns a Sink backed by the given store. currentModel is the
// embedding provider's model identifier; "" on a deployment whose provider does
// not name its model, which leaves only NULL-embedding rows as gaps.
func NewSink(store *Store, currentModel string) *Sink {
	return &Sink{store: store, currentModel: currentModel}
}

// Kind reports the call source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the record's persisted vector keyed by item id for the
// worker's dedup pass.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx, key.SourceID)
}

// Upsert writes the record's vector.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// UpsertBatch is identical to Upsert here: the unit holds one item, so there
// are no rows outside the batch to preserve.
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, key.SourceID, rows)
}

// StampExpected is a no-op. Gap detection is condition-based (no vector, or a
// vector from another model), not count-based, so there is no expected count to
// record per unit.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps returns the ids of records whose embedding is missing or stale.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	return s.store.FindGaps(ctx, s.currentModel)
}

// Coverage reports the call kind's indexed-vs-expected totals. ExpectedKnown is
// true: every indexable record is expected to converge to one vector.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, expected, err := s.store.Coverage(ctx)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}

// Compile-time interface checks.
var (
	_ indexjobs.Source           = (*Source)(nil)
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)
