package knowledge

import (
	"context"
	"fmt"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// SourceInsights is the provenance label for insight-provider hits.
const SourceInsights = "insights"

// insightSearcher is the relevance-search capability of the insight store. It
// matches knowledgekit.InsightSearcher; declared locally so the provider
// depends on the capability and tests can supply a fake.
type insightSearcher interface {
	Search(ctx context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error)
}

// InsightsProvider exposes captured domain knowledge (insights) to the router.
//
// Insights are knowledge-dimension memory rows owned by the caller
// (insight.captured_by == caller email). The underlying searcher scopes to that
// owner and to the knowledge dimension, so this provider covers exactly the
// records the MemoryProvider skips.
//
// Scope note (#632): the epic envisions reviewed insights becoming shared
// across callers. The current store has no review-state-aware sharing, and
// searching it without an owner would expose every user's personal insights, so
// PR1 keeps this provider per-user. Promoting reviewed insights to ScopeShared
// is deferred to the write-path/review work (#633).
type InsightsProvider struct {
	searcher insightSearcher
}

// NewInsightsProvider builds the insights provider over an insight searcher.
func NewInsightsProvider(searcher insightSearcher) *InsightsProvider {
	return &InsightsProvider{searcher: searcher}
}

// Name returns the provenance label.
func (*InsightsProvider) Name() string { return SourceInsights }

// Scope marks this provider per-user; see the type doc for why reviewed-insight
// sharing is deferred.
func (*InsightsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's captured insights ranked by relevance to the
// intent, optionally filtered by review status. Each hit carries the insight's
// review status and linked entity URNs as provenance. It responds to the text
// (Intent) path only; entity-keyed lookup is served by the memory provider, so
// a query with no intent yields nothing here. It fails closed on a missing
// caller email rather than searching across all users.
func (p *InsightsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.Email == "" || q.Intent == "" {
		return nil, nil
	}

	scored, err := p.searcher.Search(ctx, knowledgekit.InsightSearchQuery{
		QueryText:  q.Intent,
		Embedding:  q.Embedding,
		CapturedBy: q.Caller.Email,
		Status:     q.Status,
		Limit:      q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("insight search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		hits = append(hits, Hit{
			Text:       scored[i].Insight.InsightText,
			Source:     SourceInsights,
			Ref:        scored[i].Insight.ID,
			Score:      scored[i].Score,
			Status:     scored[i].Insight.Status,
			EntityURNs: scored[i].Insight.EntityURNs,
		})
	}
	return hits, nil
}
