package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceKnowledgePages is the provenance label for knowledge-page hits.
const SourceKnowledgePages = "knowledge_pages"

// PageSearcher is what the provider needs from the knowledge-page store:
// relevance search over page content (the text path) and the reverse lookup of the
// pages that reference an entity (the entity path, #634). Declared locally so the
// provider depends on the capability and tests can supply a fake.
type PageSearcher interface {
	Search(ctx context.Context, q knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error)
	ListPagesReferencing(ctx context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error)
}

// PagesProvider exposes the platform's canonical knowledge pages (the
// internal-knowledge home for business/domain ontology) to the router. Pages are
// org-shared, so this provider is shared: it is queried for every request and
// needs no caller identity, and it never holds per-user records.
type PagesProvider struct {
	searcher PageSearcher
}

// NewKnowledgePagesProvider builds the knowledge-pages provider over a searcher.
func NewKnowledgePagesProvider(searcher PageSearcher) *PagesProvider {
	return &PagesProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*PagesProvider) Name() string { return SourceKnowledgePages }

// Scope marks knowledge pages shared (global canonical knowledge, always queried).
func (*PagesProvider) Scope() Scope { return ScopeShared }

// Search returns knowledge pages matching the query: the pages that REFERENCE each
// requested entity (the entity path, via the reverse lookup) plus the pages relevant
// to the intent (the text path, ranked over title/body/tags), merged and de-duplicated
// by page id. So an entity-keyed search surfaces "the knowledge about this entity",
// not just text matches (#634).
func (p *PagesProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	seen := make(map[string]bool)
	entityHits := p.searchByEntity(ctx, q, seen)
	textHits, err := p.searchByText(ctx, q, seen)
	if err != nil {
		return nil, err
	}
	if len(entityHits) == 0 && len(textHits) == 0 {
		return nil, nil
	}
	return append(entityHits, textHits...), nil
}

// searchByEntity returns the knowledge pages that reference each requested entity
// URN (the reverse lookup). A URN that does not parse as a reference, or that no
// page references, is skipped rather than failing the search.
func (p *PagesProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool) []Hit {
	var hits []Hit
	for _, urn := range q.EntityURNs {
		ref, err := knowledgepage.ParseEntityRef(urn)
		if err != nil {
			continue
		}
		pages, err := p.searcher.ListPagesReferencing(ctx, ref)
		if err != nil {
			slog.Debug("knowledge-page reverse lookup skipped", "urn", urn, "error", err)
			continue
		}
		for _, pg := range pages {
			if seen[pg.ID] {
				continue
			}
			seen[pg.ID] = true
			hits = append(hits, Hit{
				Text:       knowledgePageRefHitText(pg),
				Source:     SourceKnowledgePages,
				Ref:        pg.ID,
				Score:      entityMatchScore,
				EntityURNs: []string{urn},
			})
		}
	}
	return hits
}

// searchByText returns knowledge pages relevant to the intent, ranked over page
// content. A query with no intent yields nothing.
func (p *PagesProvider) searchByText(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
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
		if seen[scored[i].Page.ID] {
			continue
		}
		seen[scored[i].Page.ID] = true
		hits = append(hits, Hit{
			Text:   knowledgePageHitText(scored[i].Page),
			Source: SourceKnowledgePages,
			Ref:    scored[i].Page.ID,
			Score:  scored[i].Score,
		})
	}
	return hits, nil
}

// knowledgePageRefHitText renders a reverse-lookup page hit: its title, noting it
// is knowledge about the queried entity.
func knowledgePageRefHitText(pg knowledgepage.PageRef) string {
	return pg.Title
}

// knowledgePageHitText renders a page as a knowledge snippet: its title and its
// summary when present, so a hit conveys what the page covers without a fetch.
func knowledgePageHitText(page knowledgepage.Page) string {
	if page.Summary == "" {
		return page.Title
	}
	return strings.TrimSpace(page.Title + "\n" + page.Summary)
}
