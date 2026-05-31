package memoryindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Source implements indexjobs.Source for the memory kind. A unit is one
// memory record (SourceID = record id) and yields exactly one item: the
// record's content. The worker embeds it and the Sink writes the vector
// back onto the same row.
type Source struct {
	store *Store
}

// NewSource returns a Source backed by the given store.
func NewSource(store *Store) *Source { return &Source{store: store} }

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the memory source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns the record's single embeddable item. A record that
// was archived or deleted between enqueue and claim yields an empty slice
// (a clean completion that writes no vector), per the Source contract.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	content, err := s.store.GetContent(ctx, sourceID)
	if errors.Is(err, errArchivedOrMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memorySource: load items: %w", err)
	}
	return []indexjobs.Item{{ItemID: sourceID, Text: content}}, nil
}

// OnSucceeded is a no-op: recall reads embeddings from memory_records
// directly on every query, so there is no in-memory cache to refresh
// after a backfill writes a vector.
func (*Source) OnSucceeded(string) {}
