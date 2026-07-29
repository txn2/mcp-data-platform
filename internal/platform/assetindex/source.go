package assetindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Source implements indexjobs.Source for the portal-assets kind. A unit is one
// saved asset (SourceID = asset id) and yields exactly one item: the asset's
// composed embed text. The worker embeds it and the Sink writes the vector back
// onto the same row.
type Source struct {
	store *Store
}

// NewSource returns a Source backed by the given store.
func NewSource(store *Store) *Source { return &Source{store: store} }

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the portal-assets source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns the asset's single embeddable item. An asset soft-deleted
// between enqueue and claim yields an empty slice (a clean completion that
// writes no vector), per the Source contract.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	text, err := s.store.GetIndexText(ctx, sourceID)
	if errors.Is(err, errNotIndexable) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetSource: load items: %w", err)
	}
	return []indexjobs.Item{{ItemID: sourceID, Text: text}}, nil
}

// OnSucceeded is a no-op: the ranked search reads embeddings from the
// portal_assets table directly on every query, so there is no in-memory cache to
// refresh after a backfill writes a vector.
func (*Source) OnSucceeded(string) {}
