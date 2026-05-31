package toolsindex

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// CurrentItemsFunc returns the live tool corpus to index: the same
// items the worker's Source.LoadItems produces. The Sink calls it from
// FindGaps to diff the running registry against the persisted vectors,
// so gap detection sees the in-process tool set (descriptions, deny
// flips) a DB count never could.
type CurrentItemsFunc func(ctx context.Context) ([]indexjobs.Item, error)

// Sink implements indexjobs.Sink for the tools kind over the
// tool_embeddings table. The key's SourceID is used verbatim as the
// tool_embeddings source_id; there is no composite encoding because,
// unlike api-catalog, the tool corpus is a single flat set per source.
type Sink struct {
	store        *Store
	currentItems CurrentItemsFunc
}

// NewSink returns a Sink backed by the given store. currentItems
// supplies the live tool corpus for content-drift gap detection; when
// nil, FindGaps falls back to re-syncing every sweep so a wiring
// mistake never silently stops indexing.
func NewSink(store *Store, currentItems CurrentItemsFunc) *Sink {
	return &Sink{store: store, currentItems: currentItems}
}

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

// Coverage reports the number of indexed tool vectors. ExpectedKnown is
// false: tools has no fixed expected total, and deriving one from the
// live registry size would mean enumerating the whole tool set on every
// dashboard poll and would still read wrong whenever a stale vector
// briefly outlives a removed tool (indexed > registry). The dashboard
// instead renders the in-sync state as a full bar from this indexed
// count, and the verdict's drift signal comes from the content-hash gap
// check (FindGaps), not a count ratio.
func (s *Sink) Coverage(ctx context.Context) (indexjobs.Coverage, error) {
	indexed, err := s.store.Coverage(ctx)
	if err != nil {
		return indexjobs.Coverage{}, err
	}
	return indexjobs.Coverage{Indexed: indexed, ExpectedKnown: false}, nil
}

// StampExpected is a no-op for the tools kind. The framework calls it
// after a successful embed to record an expected item count for
// count-based gap detection, but tools derives both its gap check and
// its coverage from the live registry (see FindGaps and Coverage), so
// there is no count to record.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps reports whether the live tool corpus has drifted from the
// persisted vectors, returning the single tools source when it has and
// an empty slice when the index is in sync.
//
// The tool set lives in the running process (compiled-in toolkits plus
// admin visibility and description config), and it drifts in ways a
// count comparison cannot see: a description edit or a deny flip changes
// the live descriptor without changing the stored vector count. So
// rather than the api-catalog count diff, this enumerates the live tools
// and compares each one's embed-text hash against the persisted vector,
// returning the source on any add, removal, or edit. A steady-state
// registry produces no gap, so the reconciler stops enqueuing the
// every-sweep no-op job the unconditional predecessor produced (issue
// #511); an actual change still converges within one reconcile interval.
//
// currentItems nil is a wiring fault, not a steady state, so FindGaps
// fails safe by re-syncing rather than silently reporting no gap.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	if s.currentItems == nil {
		return []string{SourceID}, nil
	}
	items, err := s.currentItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("toolsindex: find gaps load items: %w", err)
	}
	existing, err := s.store.ListVectors(ctx, SourceID)
	if err != nil {
		return nil, fmt.Errorf("toolsindex: find gaps list vectors: %w", err)
	}
	if indexjobs.ContentGap(items, existing) {
		return []string{SourceID}, nil
	}
	return nil, nil
}
