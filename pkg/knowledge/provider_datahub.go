package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// SourceDatahub is the provenance label for technical-catalog hits.
const SourceDatahub = "datahub"

// tableSearcher is the catalog relevance-search capability the datahub provider
// needs. It matches semantic.Provider's SearchTables; declared locally so the
// provider depends on the capability and tests can supply a fake.
type tableSearcher interface {
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
}

// DatahubProvider exposes the technical catalog (DataHub) to the router as a
// relevance search. It is shared: the catalog is global, so it is queried for
// every request and needs no caller identity. This folds datahub_search's
// relevance role into search; structured catalog navigation
// (platform/domain/tag/entity-type filters) stays in datahub_browse.
//
// DataHub ranks results but does not return a numeric score, so the provider
// derives a descending positional score from the result order; the router's
// per-provider normalization then places these on the common scale.
type DatahubProvider struct {
	searcher tableSearcher
}

// NewDatahubProvider builds the datahub provider over a catalog searcher.
func NewDatahubProvider(searcher tableSearcher) *DatahubProvider {
	return &DatahubProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*DatahubProvider) Name() string { return SourceDatahub }

// Scope marks the catalog shared (global, always queried).
func (*DatahubProvider) Scope() Scope { return ScopeShared }

// Search returns catalog entities relevant to the intent. It responds to the
// text path only; a query with no intent yields nothing.
func (p *DatahubProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
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
		hits = append(hits, Hit{
			Text:       catalogHitText(results[i]),
			Source:     SourceDatahub,
			Ref:        results[i].URN,
			Score:      positionalScore(i, n),
			EntityURNs: []string{results[i].URN},
		})
	}
	return hits, nil
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

// catalogHitText renders a catalog entity as a knowledge snippet: its name and
// its description when present.
func catalogHitText(r semantic.TableSearchResult) string {
	if r.Description == "" {
		return r.Name
	}
	return strings.TrimSpace(r.Name + "\n" + r.Description)
}
