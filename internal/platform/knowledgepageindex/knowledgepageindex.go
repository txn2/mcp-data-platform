// Package knowledgepageindex is the knowledge-page consumer of the shared
// indexjobs framework (#633). It registers a Source/Sink pair under
// source_kind = "portal-knowledge-pages" so canonical knowledge pages are
// embedded off the request path. A newly created page and a content edit each
// enqueue their own job at write time (#1256), so a page a session just wrote is
// findable through ranked search in that same session; the reconciler is the
// backstop for those a write could not produce and for the corpus a provider
// model swap invalidates.
//
// SourceID is the page id, and a unit yields one Item per CHUNK of the page's
// composed text (title + body + tags, split by knowledgepage.IndexChunks to the
// provider's input budget), because a page's body routinely exceeds what an
// embedding provider accepts in one call — before #1242 everything past that
// budget was trimmed off and never reached the model. The vectors live in
// portal_knowledge_page_embedding_chunks, one row per chunk, and search keeps the
// best-scoring chunk per page so results stay page-granular. The body is indexed
// (unlike assets, whose body lives unindexed in S3), so page CONTENT is
// semantically searchable.
//
// Only live pages with indexable text are indexed. Gap detection and coverage
// share one predicate, so a soft-deleted or text-less page is never embedded and
// never counted as missing coverage.
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
// provider's model identifier; maxInputBytes is that provider's per-text input
// budget (embedding.MaxInputBytes), the size pages are chunked to.
func RegisterConsumer(reg interface {
	Register(indexjobs.Source, indexjobs.Sink) error
}, db *sql.DB, currentModel string, maxInputBytes int,
) error {
	store := NewStore(db)
	if err := reg.Register(NewSource(store, maxInputBytes), NewSink(store, currentModel)); err != nil {
		return fmt.Errorf("registering knowledge-pages index consumer: %w", err)
	}
	return nil
}
