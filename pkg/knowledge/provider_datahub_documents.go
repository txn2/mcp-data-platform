package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// SourceContextDocuments is the provenance label for DataHub context-document hits.
const SourceContextDocuments = "context_documents"

// ContextDocumentsProvider surfaces DataHub context documents in unified search (#692).
// Context documents are the non-dataset knowledge home that predates knowledge
// pages; before this source they were undiscoverable from search (the catalog
// provider only searched datasets), so the knowledge in them was stranded. The
// catalog is global, so this is a shared source queried for every request.
type ContextDocumentsProvider struct {
	searcher semantic.DocumentSearcher
}

// NewContextDocumentsProvider builds the documents provider over a document searcher.
func NewContextDocumentsProvider(searcher semantic.DocumentSearcher) *ContextDocumentsProvider {
	return &ContextDocumentsProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*ContextDocumentsProvider) Name() string { return SourceContextDocuments }

// Scope marks the catalog shared (global, always queried).
func (*ContextDocumentsProvider) Scope() Scope { return ScopeShared }

// Search returns context documents for the query: those linked to the requested
// EntityURNs (entity path) plus those relevant to Intent (text path), merged and
// de-duplicated by document URN so a document found both ways appears once, the
// entity match ranked first at the exact-match score (mirroring CatalogProvider and
// PagesProvider). Both arms exclude drafts; the text arm additionally excludes
// documents hidden from global search, which the entity (linked-asset) arm surfaces.
func (p *ContextDocumentsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	return mergeArms(ctx, q, p.searchByEntity, p.searchByText)
}

// searchByEntity returns the context documents linked to each requested entity URN
// (already lineage-expanded by the Router), so an entity-keyed search surfaces the
// document that describes a dataset just as it surfaces a knowledge page that
// references it. A URN the catalog has no documents for, or that errors, is skipped
// rather than blanking the provider, since the entity set is probed across many
// lineage-expanded URNs that legitimately have none.
//
// The entity path gates only on publication, NOT on global visibility: DataHub
// defines ShowInGlobalContext=false as "only accessible through linked assets"
// (mcp-datahub types/document.go), and an entity-keyed lookup IS that linked-asset
// path, so suppressing those documents here would make them reachable nowhere. The
// text arm still hides them from global search.
func (p *ContextDocumentsProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool) []Hit {
	var hits []Hit
	for _, urn := range q.EntityURNs {
		docs, err := p.searcher.GetRelatedDocuments(ctx, urn)
		if err != nil {
			slog.Debug("related document lookup skipped", "urn", urn, "error", err)
			continue
		}
		for i := range docs {
			if seen[docs[i].URN] || !publishedDocument(docs[i]) {
				continue
			}
			seen[docs[i].URN] = true
			hits = append(hits, documentHit(docs[i], entityMatchScore))
		}
	}
	return hits
}

// searchByText returns context documents relevant to the intent. A query with no
// intent yields nothing, since a document is found by what it says.
func (p *ContextDocumentsProvider) searchByText(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if strings.TrimSpace(q.Intent) == "" {
		return nil, nil
	}
	// Fetch the same candidate budget as every other source. The upstream search
	// applies no visibility/status filter and returns full document bodies, so over
	// fetching to compensate for post-filter shrinkage would put a disproportionate
	// (limit x N full-body) DataHub query on every search; the efficient fix for a
	// hidden-heavy corpus is a server-side visibility push-down upstream, not blind
	// client over-fetch.
	docs, err := p.searcher.SearchDocuments(ctx, q.Intent, q.Limit)
	if err != nil {
		return nil, fmt.Errorf("document search: %w", err)
	}
	hits := make([]Hit, 0, len(docs))
	for i := range docs {
		// Global (text) search gates on publication AND visibility: a document a
		// steward hid (ShowInGlobalContext=false) must not appear in broad search,
		// only via its linked assets (the entity arm).
		if seen[docs[i].URN] || !publishedDocument(docs[i]) || !docs[i].ShowInGlobalContext {
			continue
		}
		seen[docs[i].URN] = true
		hits = append(hits, documentHit(docs[i], positionalScore(i, len(docs))))
	}
	return hits, nil
}

// publishedDocument reports whether a context document is published (not a draft),
// the gate both arms share. The upstream create path defaults an unset status to
// PUBLISHED (mcp-datahub pkg/tools/write_create.go: `if status == "" { status =
// "PUBLISHED" }`), so an empty status is treated as published (avoiding zero hits
// when a deployment leaves status unset); UNPUBLISHED and any other non-published
// state are excluded, so neither drafts nor an unknown future state leak. Visibility
// (ShowInGlobalContext) is NOT checked here: it gates only the global text arm, since
// a hidden document is by contract still reachable through its linked assets.
func publishedDocument(d semantic.DocumentResult) bool {
	return d.Status == "" || strings.EqualFold(d.Status, "PUBLISHED")
}

// documentHit maps a context document to a knowledge hit. The URN both drills in
// (Ref) and is the citation (Reference, a urn:li:document:<id> form); the title and
// snippet give an agent enough to decide whether to open and migrate the document,
// and the related-asset URNs say what the document is about.
func documentHit(d semantic.DocumentResult, score float64) Hit {
	return Hit{
		Text:       documentHitText(d),
		Source:     SourceContextDocuments,
		Ref:        d.URN,
		Score:      score,
		EntityURNs: d.RelatedAssetURNs,
		Reference:  d.URN,
	}
}

// documentHitText renders a document as a search snippet: its title (or sub-type
// when untitled) and a bounded body excerpt.
func documentHitText(d semantic.DocumentResult) string {
	title := strings.TrimSpace(d.Title)
	if title == "" {
		if d.SubType != "" {
			title = d.SubType
		} else {
			title = "context document"
		}
	}
	return catalogSnippet(title, d.Snippet)
}
