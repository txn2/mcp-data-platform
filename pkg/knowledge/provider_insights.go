package knowledge

import (
	"context"
	"fmt"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// SourceInsights is the provenance label for insight-provider hits.
const SourceInsights = "insights"

// insightSource is the slice of the insight store the provider needs: the
// relevance search (text path) plus the entity-keyed list (entity path). It
// matches knowledgekit.SearchableInsightStore; declared locally so the provider
// depends on the capability and tests can supply a fake.
type insightSource interface {
	Search(ctx context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error)
	List(ctx context.Context, filter knowledgekit.InsightFilter) ([]knowledgekit.Insight, int, error)
}

// InsightsProvider exposes captured domain knowledge (insights) to the router.
//
// Insights are knowledge-dimension memory rows owned by the caller
// (insight.captured_by == caller email). The underlying store scopes to that
// owner and to the knowledge dimension, so this provider covers exactly the
// records the MemoryProvider skips.
//
// Scope note (#632): the epic envisions reviewed insights becoming shared
// across callers. The current store has no review-state-aware sharing, and
// searching it without an owner would expose every user's personal insights, so
// PR1 keeps this provider per-user. Promoting reviewed insights to ScopeShared
// is deferred to the write-path/review work (#633).
type InsightsProvider struct {
	store insightSource
}

// NewInsightsProvider builds the insights provider over a searchable insight
// store.
func NewInsightsProvider(store insightSource) *InsightsProvider {
	return &InsightsProvider{store: store}
}

// Name returns the provenance label.
func (*InsightsProvider) Name() string { return SourceInsights }

// Scope marks this provider per-user; see the type doc for why reviewed-insight
// sharing is deferred.
func (*InsightsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's captured insights. It serves both query shapes:
// an exact entity-keyed lookup on EntityURNs (insights linked to the requested
// datasets, lineage-expanded by the Router) and a relevance search on Intent.
// Results from both paths are merged and de-duplicated by insight id. Each hit
// carries the insight's review status and linked entity URNs as provenance. It
// fails closed on a missing caller email rather than searching across all users.
func (p *InsightsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.Email == "" {
		return nil, nil
	}

	var hits []Hit
	seen := make(map[string]bool)

	entityHits, err := p.searchByEntity(ctx, q, seen)
	if err != nil {
		return nil, err
	}
	hits = append(hits, entityHits...)

	textHits, err := p.searchByText(ctx, q, seen)
	if err != nil {
		return nil, err
	}
	hits = append(hits, textHits...)

	return hits, nil
}

// searchByEntity returns the caller's insights linked to the query's entity URNs
// (already lineage-expanded by the Router). It reuses the entity-keyed List path
// that memory_manage(filter_entity_urn=...) relies on, scoped to the caller's
// email and the knowledge dimension by the store. Already-seen insights are
// skipped; when no explicit status was requested, rejected/superseded/rolled-back
// insights are dropped so a "what do we know" lookup never surfaces retracted
// knowledge.
func (p *InsightsProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if len(q.EntityURNs) == 0 {
		return nil, nil
	}

	var hits []Hit
	for _, urn := range q.EntityURNs {
		insights, _, err := p.store.List(ctx, knowledgekit.InsightFilter{
			EntityURN:  urn,
			CapturedBy: q.Caller.Email,
			Status:     q.Status,
			Limit:      q.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("insight entity lookup: %w", err)
		}
		for i := range insights {
			if seen[insights[i].ID] {
				continue
			}
			if q.Status == "" && !isLiveInsightStatus(insights[i].Status) {
				continue
			}
			seen[insights[i].ID] = true
			hits = append(hits, insightHit(insights[i], entityMatchScore))
		}
	}
	return hits, nil
}

// searchByText returns the caller's insights ranked by relevance to the intent,
// optionally filtered by review status. Already-seen insights (recalled on the
// entity path) are skipped. A query with no intent yields nothing here.
func (p *InsightsProvider) searchByText(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.store.Search(ctx, knowledgekit.InsightSearchQuery{
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
		if seen[scored[i].Insight.ID] {
			continue
		}
		// Same retraction as the entity path: with no explicit status requested, a
		// rejected/superseded/rolled-back insight is no longer in force and must not
		// surface in a "what do we know" lookup (#684).
		if q.Status == "" && !isLiveInsightStatus(scored[i].Insight.Status) {
			continue
		}
		seen[scored[i].Insight.ID] = true
		hits = append(hits, insightHit(scored[i].Insight, scored[i].Score))
	}
	return hits, nil
}

// insightHit maps an insight to a knowledge hit, carrying its review status and
// linked entity URNs as provenance.
func insightHit(in knowledgekit.Insight, score float64) Hit {
	return Hit{
		Text:       in.InsightText,
		Source:     SourceInsights,
		Ref:        in.ID,
		Score:      score,
		Status:     in.Status,
		EntityURNs: in.EntityURNs,
	}
}

// isLiveInsightStatus reports whether an insight status represents knowledge
// still in force. Rejected, superseded, and rolled-back insights are retracted
// and must not surface on either unfiltered discovery path (entity or text).
func isLiveInsightStatus(status string) bool {
	switch status {
	case knowledgekit.StatusRejected, knowledgekit.StatusSuperseded, knowledgekit.StatusRolledBack:
		return false
	default:
		return true
	}
}
