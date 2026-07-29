package promptindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Source implements indexjobs.Source for the prompts kind. A unit is one
// approved prompt (SourceID = prompt id) and yields exactly one item: the
// prompt's composed embed text. The worker embeds it and the Sink writes the
// vector back onto the same row.
type Source struct {
	store *Store
}

// NewSource returns a Source backed by the given store.
func NewSource(store *Store) *Source { return &Source{store: store} }

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the prompts source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns the prompt's single embeddable item. A prompt that was
// deprecated, disabled, or deleted between enqueue and claim yields an empty
// slice (a clean completion that writes no vector), per the Source contract.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	text, err := s.store.GetIndexText(ctx, sourceID)
	if errors.Is(err, errNotIndexable) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("promptSource: load items: %w", err)
	}
	return []indexjobs.Item{{ItemID: sourceID, Text: text}}, nil
}

// OnSucceeded is a no-op: the ranked search reads embeddings from the prompts
// table directly on every query, so there is no in-memory cache to refresh
// after a backfill writes a vector.
func (*Source) OnSucceeded(string) {}
