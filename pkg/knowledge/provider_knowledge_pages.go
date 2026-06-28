package knowledge

import (
	"context"
	"errors"
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
	// Get reads one page by id (the other half of search: a hit's reference
	// dereferenced to the full body). Returns knowledgepage.ErrNotFound for a
	// missing id.
	Get(ctx context.Context, id string) (*knowledgepage.Page, error)
	// List returns the offset/limit page of live pages plus the total live-page
	// count, ordered deterministically, for exhaustive enumeration (#695).
	List(ctx context.Context, filter knowledgepage.Filter) ([]knowledgepage.Page, int, error)
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
	return mergeArms(ctx, q, p.searchByEntity, p.searchByText)
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
				Reference:  knowledgepage.PageReference(pg.ID),
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
			Text:      knowledgePageHitText(scored[i].Page),
			Source:    SourceKnowledgePages,
			Ref:       scored[i].Page.ID,
			Score:     scored[i].Score,
			Reference: knowledgepage.PageReference(scored[i].Page.ID),
		})
	}
	return hits, nil
}

// Fetch dereferences an mcp:knowledge_page:<id> reference to the page's full body
// (#694), the consumer the search snippet was built to anticipate ("a hit conveys
// what the page covers without a fetch"). It owns only the knowledge-page reference
// form; any other reference is declined (owned=false) so the Router tries the next
// provider. A missing id, or a soft-deleted page, is ErrNotFound: a page-handler
// Get returns soft-deleted rows (it is the editor's undelete path), so the live
// read must filter them exactly as the portal HTTP handler does.
func (p *PagesProvider) Fetch(ctx context.Context, ref string, _ Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetKnowledgePage {
		// Not a knowledge-page reference: decline so the Router tries the next provider.
		return nil, false, nil //nolint:nilerr // a non-page reference is a decline, not a failure
	}
	page, err := p.searcher.Get(ctx, parsed.RefPageID)
	if err != nil {
		if errors.Is(err, knowledgepage.ErrNotFound) {
			return nil, true, ErrNotFound
		}
		return nil, true, fmt.Errorf("getting knowledge page %s: %w", parsed.RefPageID, err)
	}
	if page == nil || page.DeletedAt != nil {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference: ref,
		Source:    SourceKnowledgePages,
		Title:     page.Title,
		Body:      page.Body,
	}, true, nil
}

// Browse enumerates knowledge pages in full (#695): the offset/limit page of live
// pages plus the total live-page count, with no relevance threshold, so an agent can
// page the whole corpus to audit or migrate it. Pages are org-shared, so no
// per-caller scope applies; the store's List excludes soft-deleted pages and orders
// by (updated_at DESC, id) - a deterministic total order whose unique id tiebreaker
// keeps pagination stable across pages even when timestamps collide, so a sweep
// neither skips nor double-returns a page of a fixed corpus. Each member carries the
// same Reference search emits, so a browse page feeds directly into fetch.
func (p *PagesProvider) Browse(ctx context.Context, q BrowseQuery) (BrowsePage, error) {
	pages, total, err := p.searcher.List(ctx, knowledgepage.Filter{Offset: q.Offset, Limit: q.Limit})
	if err != nil {
		return BrowsePage{}, fmt.Errorf("listing knowledge pages: %w", err)
	}
	hits := make([]Hit, 0, len(pages))
	for i := range pages {
		hits = append(hits, Hit{
			Text:      knowledgePageHitText(pages[i]),
			Source:    SourceKnowledgePages,
			Ref:       pages[i].ID,
			Reference: knowledgepage.PageReference(pages[i].ID),
		})
	}
	return BrowsePage{Hits: hits, Total: total}, nil
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
