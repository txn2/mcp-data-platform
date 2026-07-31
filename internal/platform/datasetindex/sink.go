package datasetindex

import (
	"context"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Sink implements indexjobs.Sink for the catalog-datasets kind over
// catalog_datasets. currentModel is the provider model the gap query diffs
// stored rows against, so a model swap re-embeds rows stamped with the previous
// model; syncInterval is how long an enumeration stays fresh before the gap
// sweep asks for another one.
type Sink struct {
	store        *Store
	currentModel string
	syncInterval time.Duration
}

// NewSink returns a Sink backed by the given store. currentModel is the
// embedding provider's model identifier (embedding.ModelName); pass "" on a
// deployment whose provider does not name its model, in which case only
// missing-embedding rows count as gaps.
func NewSink(store *Store, currentModel string, syncInterval time.Duration) *Sink {
	return &Sink{store: store, currentModel: currentModel, syncInterval: syncInterval}
}

// Compile-time interface checks.
var (
	_ indexjobs.Sink             = (*Sink)(nil)
	_ indexjobs.CoverageReporter = (*Sink)(nil)
)

// Kind reports the catalog-datasets source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns every mirrored dataset's vector for the worker's dedup
// pass. The unit is the whole corpus, so the key carries no narrowing.
func (s *Sink) ListExisting(ctx context.Context, _ indexjobs.Key) (map[string]indexjobs.Vector, error) {
	return s.store.ListVectors(ctx)
}

// Upsert replaces the corpus: it drops mirrored datasets outside the supplied
// set and writes the set's vectors. This is what prunes a dataset the catalog
// no longer returns.
func (s *Sink) Upsert(ctx context.Context, _ indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.ReplaceVectors(ctx, rows)
}

// UpsertBatch writes one chunk's vectors in place, without pruning: the rows
// outside the batch are the rest of the corpus, which the final Upsert settles.
func (s *Sink) UpsertBatch(ctx context.Context, _ indexjobs.Key, rows []indexjobs.Vector) error {
	return s.store.UpsertVectors(ctx, rows)
}

// StampExpected is a no-op. Gap detection here is condition-based (sweep age,
// missing or stale-model vectors), not count-based: the expected count lives in
// DataHub, so a stamped number could only ever restate the last sweep's own
// result. The sweep timestamp — the part that does drive scheduling — is
// stamped by the Source when the enumeration it describes actually happened.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps returns the corpus unit when it owes work: the enumeration has aged
// out (which is how the periodic sweep is scheduled without a goroutine of its
// own) or some mirrored dataset has no vector, or one from a superseded model.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	needs, err := s.store.NeedsSweep(ctx, s.currentModel, s.syncInterval)
	if err != nil {
		return nil, err
	}
	if !needs {
		return nil, nil
	}
	return []string{SourceID}, nil
}

// Coverage reports the catalog-datasets kind's indexed-vs-expected totals
// (mirrored datasets carrying a vector vs all mirrored datasets). ExpectedKnown
// is true: every mirrored dataset is expected to converge to one vector. It
// describes the mirror, not the catalog — a corpus truncated by the entry cap
// reports full coverage of what it mirrors, which is why the Source logs the
// truncation.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, expected, err := s.store.Coverage(ctx)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, Expected: expected, ExpectedKnown: true}, nil
}
