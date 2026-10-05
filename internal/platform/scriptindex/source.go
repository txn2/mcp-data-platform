package scriptindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// Source implements indexjobs.Source for the scripts kind. A unit is one
// enabled script (SourceID = script id) and yields one item per chunk of it:
// the card, then the source (script.IndexChunks).
type Source struct {
	store *Store
	// maxInputBytes is the provider's per-text input, the size a script is
	// chunked to.
	maxInputBytes int
}

// NewSource returns a Source backed by the given store, chunking each script
// to maxInputBytes per item.
func NewSource(store *Store, maxInputBytes int) *Source {
	return &Source{store: store, maxInputBytes: maxInputBytes}
}

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the scripts source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns one item per chunk of the script, in chunk order, and
// records the hash of the text they were cut from for StampExpected. A script
// disabled or deleted between enqueue and claim yields an empty slice (a clean
// completion that clears its vectors), per the Source contract.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	sc, err := s.store.GetIndexed(ctx, sourceID)
	if errors.Is(err, errNotIndexable) {
		// Nothing is built, so nothing an earlier pass read may be stamped.
		s.store.loaded.Delete(sourceID)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scriptSource: load items: %w", err)
	}
	s.store.loaded.Store(sourceID, indexjobs.TextHash(Corpus(sc)))
	chunks := Chunks(sc, s.maxInputBytes)
	items := make([]indexjobs.Item, 0, len(chunks))
	for i, text := range chunks {
		items = append(items, indexjobs.Item{ItemID: itemID(sourceID, i), Text: text})
	}
	return items, nil
}

// OnSucceeded is a no-op: the ranked search reads the chunk table on every
// query, so there is no in-memory cache to refresh after a backfill.
func (*Source) OnSucceeded(string) {}
