package knowledgepageindex

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// Source implements indexjobs.Source for the portal-knowledge-pages kind. A unit
// is one page (SourceID = page id) and yields one item per embeddable chunk of
// that page: the page's composed text split so no chunk exceeds the embedding
// provider's input budget (#1242). The worker embeds each chunk and the Sink
// writes the vectors into the page's chunk table.
type Source struct {
	store *Store
	// maxInputBytes is the provider's per-text input budget, the size the page's
	// text is chunked to. It is read from the provider (embedding.MaxInputBytes)
	// rather than the constant, so an operator running a larger-context model
	// gets larger chunks from the same code.
	maxInputBytes int
}

// NewSource returns a Source backed by the given store, chunking page text to
// maxInputBytes per item.
func NewSource(store *Store, maxInputBytes int) *Source {
	return &Source{store: store, maxInputBytes: maxInputBytes}
}

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the portal-knowledge-pages source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns one item per embeddable chunk of the page, in chunk order. A
// page soft-deleted between enqueue and claim, or one with no indexable text at
// all, yields an empty slice (a clean completion that writes no vectors), per the
// Source contract.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	content, err := s.store.GetContent(ctx, sourceID)
	if errors.Is(err, errNotIndexable) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("knowledgePageSource: load items: %w", err)
	}
	chunks := knowledgepage.IndexChunks(content.Title, content.Body, content.Tags, s.maxInputBytes)
	items := make([]indexjobs.Item, 0, len(chunks))
	for i, text := range chunks {
		items = append(items, indexjobs.Item{ItemID: itemID(sourceID, i), Text: text})
	}
	return items, nil
}

// OnSucceeded is a no-op: the ranked search reads embeddings from the chunk table
// directly on every query, so there is no in-memory cache to refresh after a
// backfill writes a vector.
func (*Source) OnSucceeded(string) {}
