package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceKnowledgePages is the provenance label for knowledge-page hits.
const SourceKnowledgePages = "knowledge_pages"

// knowledgePageSearcher is the relevance-search capability of the knowledge-page
// store. It matches knowledgepage.Searcher; declared locally so the
// provider depends on the capability and tests can supply a fake.
type knowledgePageSearcher interface {
	Search(ctx context.Context, q knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error)
}

// PagesProvider exposes the platform's canonical knowledge pages (the
// internal-knowledge home for business/domain ontology) to the router. Pages are
// org-shared, so this provider is shared: it is queried for every request and
// needs no caller identity, and it never holds per-user records.
type PagesProvider struct {
	searcher knowledgePageSearcher
}

// NewKnowledgePagesProvider builds the knowledge-pages provider over a searcher.
func NewKnowledgePagesProvider(searcher knowledgePageSearcher) *PagesProvider {
	return &PagesProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*PagesProvider) Name() string { return SourceKnowledgePages }

// Scope marks knowledge pages shared (global canonical knowledge, always queried).
func (*PagesProvider) Scope() Scope { return ScopeShared }

// Search returns knowledge pages relevant to the intent, ranked over page
// CONTENT (title + body + tags are indexed). It responds to the text path only;
// a query with no intent yields nothing.
func (p *PagesProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.searcher.Search(ctx, knowledgepage.SearchQuery{
		QueryText: q.Intent,
		Embedding: q.Embedding,
		Limit:     q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("knowledge page search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		hits = append(hits, Hit{
			Text:   knowledgePageHitText(scored[i].Page),
			Source: SourceKnowledgePages,
			Ref:    scored[i].Page.ID,
			Score:  scored[i].Score,
		})
	}
	return hits, nil
}

// knowledgePageHitText renders a page as a knowledge snippet: its title and its
// summary when present, so a hit conveys what the page covers without a fetch.
func knowledgePageHitText(page knowledgepage.Page) string {
	if page.Summary == "" {
		return page.Title
	}
	return strings.TrimSpace(page.Title + "\n" + page.Summary)
}
