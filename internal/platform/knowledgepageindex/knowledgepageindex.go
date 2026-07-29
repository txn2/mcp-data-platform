// Package knowledgepageindex is the knowledge-page consumer of the shared
// indexjobs framework (#633). It registers a Source/Sink pair under
// source_kind = "portal-knowledge-pages" so the reconciler embeds canonical
// knowledge pages off the request path: a newly created page (whose vector is
// still NULL) or one left stale by a content edit or a provider model swap
// self-heals on the next sweep.
//
// Like the asset, prompt, and memory consumers, pages store their vectors inline
// on the portal_knowledge_pages table (one embedding per row), not in a
// dedicated vector table. So this package's Store reads and writes the embedding
// / embedding_model / embedding_text_hash columns directly: a page IS its own
// indexing unit. SourceID is the page id; each unit yields exactly one Item
// whose text is portal.KnowledgePageIndexText (title + body + tags). The body is
// indexed (unlike assets, whose body lives unindexed in S3), so page CONTENT is
// semantically searchable.
//
// Only non-deleted pages are indexed. Gap detection and coverage both filter on
// deleted_at IS NULL, so a soft-deleted page is never embedded and never counted
// as missing coverage.
package knowledgepageindex

import (
	"database/sql"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "portal-knowledge-pages"

// RegisterConsumer registers the knowledge-pages Source/Sink pair on the shared
// indexjobs registry. Keeping the wiring here (rather than inline in the
// platform) keeps the platform package thin. currentModel is the embedding
// provider's model identifier.
func RegisterConsumer(reg interface {
	Register(indexjobs.Source, indexjobs.Sink) error
}, db *sql.DB, currentModel string,
) error {
	store := NewStore(db)
	if err := reg.Register(NewSource(store), NewSink(store, currentModel)); err != nil {
		return fmt.Errorf("registering knowledge-pages index consumer: %w", err)
	}
	return nil
}
