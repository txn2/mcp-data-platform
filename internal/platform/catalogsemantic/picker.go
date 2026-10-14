package catalogsemantic

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// DefinitionLister lists the catalog's definitions; the catalog store
// implements it. A store without it picks nothing.
type DefinitionLister interface {
	ListDefinitions(ctx context.Context, q catalog.DefinitionQuery) ([]catalog.DefinitionSummary, error)
}

// domainListLimit bounds the domains ListDomains returns.
const domainListLimit = 1000

var (
	_ semantic.CatalogPicker        = (*Provider)(nil)
	_ semantic.TableMatchCounter    = (*Provider)(nil)
	_ semantic.GlossaryMatchCounter = (*Provider)(nil)
)

// ListDomains returns every domain.
func (p *Provider) ListDomains(ctx context.Context) ([]semantic.EntityRef, error) {
	return p.definitions(ctx, catalog.KindDomain, "", domainListLimit)
}

// SearchGlossaryTerms finds glossary terms whose name, slug or description
// holds the query; an empty query lists them.
func (p *Provider) SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error) {
	return p.definitions(ctx, catalog.KindGlossaryTerm, query, limit)
}

// SearchDataProducts finds data products the same way.
func (p *Provider) SearchDataProducts(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error) {
	return p.definitions(ctx, catalog.KindDataProduct, query, limit)
}

// definitions lists the definitions of one kind matching query.
func (p *Provider) definitions(ctx context.Context, kind catalog.Kind, query string, limit int) ([]semantic.EntityRef, error) {
	lister, ok := p.store.(DefinitionLister)
	if !ok {
		return []semantic.EntityRef{}, nil
	}
	if query == "*" {
		query = ""
	}
	defs, err := lister.ListDefinitions(ctx, catalog.DefinitionQuery{Kinds: []catalog.Kind{kind}, Text: query, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("listing %s definitions: %w", kind, err)
	}
	out := make([]semantic.EntityRef, 0, len(defs))
	for _, d := range defs {
		out = append(out, semantic.EntityRef{URN: d.Reference, Name: d.Name, Description: p.sanitizer.SanitizeDescription(d.Description)})
	}
	return out, nil
}

// SearchTablesCounted runs SearchTables and reports the total: a page shorter
// than the limit asked for is every match, because the catalog returns exactly
// the page it is asked for; a full page leaves the total unknown.
func (p *Provider) SearchTablesCounted(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, int, error) {
	results, err := p.SearchTables(ctx, filter)
	if err != nil {
		return nil, semantic.TotalUnknown, err
	}
	return results, pageTotal(len(results), filter.Limit), nil
}

// SearchGlossaryTermsCounted runs SearchGlossaryTerms and reports the total
// the same way.
func (p *Provider) SearchGlossaryTermsCounted(ctx context.Context, query string, limit int) ([]semantic.EntityRef, int, error) {
	refs, err := p.SearchGlossaryTerms(ctx, query, limit)
	if err != nil {
		return nil, semantic.TotalUnknown, err
	}
	return refs, pageTotal(len(refs), limit), nil
}

// pageTotal is the match count a page proves: its length when it is shorter
// than the limit, otherwise unknown.
func pageTotal(n, limit int) int {
	if limit > 0 && n < limit {
		return n
	}
	return semantic.TotalUnknown
}
