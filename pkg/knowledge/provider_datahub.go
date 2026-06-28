package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// SourceCatalog is the provenance label for technical-catalog hits.
const SourceCatalog = "catalog"

// tableSearcher is the catalog capability the datahub provider needs: relevance
// search (text path) and exact entity lookup by table identifier (entity path).
// It matches semantic.Provider; declared locally so the provider depends on the
// capability and tests can supply a fake.
type tableSearcher interface {
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
	GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
}

// CatalogProvider exposes the technical catalog (DataHub) to the router. It
// serves two query shapes: a relevance search on Intent (folding datahub_search
// into search) and an exact entity-keyed lookup on EntityURNs that returns the
// catalog entity itself, so handing search a dataset URN surfaces its catalog
// entry alongside the URN-linked memory and insights. Structured catalog
// navigation (platform/domain/tag/entity-type filters) stays in datahub_browse.
//
// It is shared: the catalog is global, so it is queried for every request and
// needs no caller identity.
//
// DataHub ranks search results but does not return a numeric score, so the
// provider derives a descending positional score from the result order;
// entity-keyed hits take the max score. The router's per-provider normalization
// then places these on the common scale.
type CatalogProvider struct {
	searcher tableSearcher
}

// NewCatalogProvider builds the catalog provider over a catalog searcher.
func NewCatalogProvider(searcher tableSearcher) *CatalogProvider {
	return &CatalogProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*CatalogProvider) Name() string { return SourceCatalog }

// Scope marks the catalog shared (global, always queried).
func (*CatalogProvider) Scope() Scope { return ScopeShared }

// Search returns catalog entities for the query: the entities named by
// EntityURNs (entity path) plus those relevant to Intent (text path), merged and
// de-duplicated by URN.
func (p *CatalogProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	return mergeArms(ctx, q, p.searchByEntity, p.searchByText)
}

// searchByEntity fetches the catalog entity for each requested URN (already
// lineage-expanded by the Router). A URN that does not parse as a dataset, or
// that the catalog cannot resolve, is skipped rather than failing the search:
// the entity set is probed across many (lineage-expanded) URNs, most of which
// legitimately have no catalog entry, so a miss must not blank the provider.
// Only entities the catalog actually returned (non-empty URN) yield a hit, so a
// non-existent URN produces nothing.
func (p *CatalogProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool) []Hit {
	var hits []Hit
	for _, urn := range q.EntityURNs {
		if seen[urn] {
			continue
		}
		table, err := memory.ParseURNToTable(urn)
		if err != nil {
			continue
		}
		tc, err := p.searcher.GetTableContext(ctx, table)
		if err != nil {
			slog.Debug("catalog entity lookup skipped", "urn", urn, "error", err)
			continue
		}
		if tc == nil || tc.URN == "" {
			continue
		}
		seen[urn] = true
		hits = append(hits, Hit{
			Text:       catalogContextText(table, tc),
			Source:     SourceCatalog,
			Ref:        urn,
			Score:      entityMatchScore,
			EntityURNs: []string{urn},
			// A DataHub reference is its URN verbatim (the canonical citable form).
			Reference: urn,
		})
	}
	return hits
}

// searchByText returns catalog entities relevant to the intent. A query with no
// intent yields nothing.
func (p *CatalogProvider) searchByText(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	results, err := p.searcher.SearchTables(ctx, semantic.SearchFilter{
		Query: q.Intent,
		Limit: q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("catalog search: %w", err)
	}

	n := len(results)
	hits := make([]Hit, 0, n)
	for i := range results {
		if seen[results[i].URN] {
			continue
		}
		seen[results[i].URN] = true
		hits = append(hits, Hit{
			Text:       catalogHitText(results[i]),
			Source:     SourceCatalog,
			Ref:        results[i].URN,
			Score:      positionalScore(i, n),
			EntityURNs: []string{results[i].URN},
			// A DataHub reference is its URN verbatim (the canonical citable form).
			Reference: results[i].URN,
		})
	}
	return hits, nil
}

// datasetPrefix is the URN form of a catalog dataset reference. The catalog owns
// exactly this prefix for fetch; the context-documents source owns
// urn:li:document:, so the two urn:li: sources never contend for a reference.
const datasetPrefix = "urn:li:dataset:"

// Fetch dereferences a urn:li:dataset:<id> reference to the dataset's full catalog
// context (#694), folding what datahub_get_entity returns into the one fetch verb.
// It owns only the dataset URN form; any other reference is declined (owned=false).
// A URN that does not parse as a dataset, that the catalog has no entry for, or that
// the catalog errors on is ErrNotFound: DataHub reports a missing/deleted entity as
// an error rather than an empty result (mcp-datahub GetEntity), and the search
// entity path treats that same lookup error as a skip (searchByEntity), so a stale
// dataset citation must be a clean not-found here too, not a hard tool failure. The
// catalog is global, so no per-caller scope applies.
func (p *CatalogProvider) Fetch(ctx context.Context, ref string, _ Caller) (*Document, bool, error) {
	if !strings.HasPrefix(ref, datasetPrefix) {
		return nil, false, nil
	}
	table, err := memory.ParseURNToTable(ref)
	if err != nil {
		return nil, true, ErrNotFound
	}
	tc, err := p.searcher.GetTableContext(ctx, table)
	if err != nil {
		// DataHub conflates "no such entity" with an error, the same condition
		// searchByEntity skips; surface it as not-found so a stale citation is a clean
		// answer rather than a failure.
		slog.Debug("catalog entity fetch miss", "urn", ref, "error", err)
		return nil, true, ErrNotFound
	}
	if tc == nil || tc.URN == "" {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference:  ref,
		Source:     SourceCatalog,
		Title:      table.String(),
		Content:    tc,
		EntityURNs: []string{ref},
	}, true, nil
}

// positionalScore turns a 0-based rank into a descending score in (0,1],
// highest for the first result. DataHub returns an ordered list without
// numeric scores, so order is the only relevance signal available.
func positionalScore(i, n int) float64 {
	if n <= 1 {
		return entityMatchScore
	}
	return float64(n-i) / float64(n)
}

// catalogHitText renders a search-ranked catalog entity as a knowledge snippet:
// its name and its description when present.
func catalogHitText(r semantic.TableSearchResult) string {
	return catalogSnippet(r.Name, r.Description)
}

// catalogContextText renders an entity-keyed catalog hit: the table's dotted
// name and its description when present.
func catalogContextText(table semantic.TableIdentifier, tc *semantic.TableContext) string {
	return catalogSnippet(table.String(), tc.Description)
}

// catalogSnippet joins a catalog entity's name and optional description into one
// knowledge snippet.
func catalogSnippet(name, description string) string {
	if description == "" {
		return name
	}
	return strings.TrimSpace(name + "\n" + description)
}
