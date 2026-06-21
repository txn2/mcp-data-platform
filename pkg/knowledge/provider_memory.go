package knowledge

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// SourceMemory is the provenance label for memory-provider hits.
const SourceMemory = "memory"

// memorySearcher is the slice of memory.Store the memory provider needs: the
// two relevance-search primitives. Declared here so the provider depends on a
// capability, not the whole store, and so tests can supply a fake.
type memorySearcher interface {
	HybridSearch(ctx context.Context, q memory.HybridQuery) ([]memory.ScoredRecord, error)
	LexicalSearch(ctx context.Context, q memory.LexicalQuery) ([]memory.ScoredRecord, error)
}

// MemoryProvider exposes a caller's personal memory to the knowledge router.
//
// It is per-user: results are restricted to records the caller owns
// (memory_records.created_by == caller email), the same identity the portal's
// "my knowledge" search scopes on. memory_recall scopes by persona instead, but
// a shared knowledge search must scope by the individual so one user's personal
// memory never reaches another.
//
// It deliberately omits the knowledge dimension. Captured insights and
// remembered knowledge are stored as knowledge-dimension memory rows and are
// owned by the InsightsProvider; surfacing them here too would double-list the
// same record. This provider therefore covers the caller's non-knowledge memory
// (preferences, events, entities, relationships).
type MemoryProvider struct {
	store memorySearcher
}

// NewMemoryProvider builds the memory provider over a memory store.
func NewMemoryProvider(store memorySearcher) *MemoryProvider {
	return &MemoryProvider{store: store}
}

// Name returns the provenance label.
func (*MemoryProvider) Name() string { return SourceMemory }

// Scope marks this provider per-user; the router supplies the caller identity
// and must skip it when that identity is absent.
func (*MemoryProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's active, non-knowledge memory ranked by relevance.
// Hybrid ranking is used when the query carries an embedding, lexical-only
// otherwise, the same hybrid-vs-lexical decision every search surface makes. The
// knowledge dimension is excluded in SQL (not after LIMIT) so the count stays
// honest, and only active rows are returned, matching memory_recall's default so
// stale or superseded memory does not resurface. It fails closed: an empty
// caller email yields no results rather than an unscoped search across all users.
func (p *MemoryProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.Email == "" {
		return nil, nil
	}

	var (
		scored []memory.ScoredRecord
		err    error
	)
	if len(q.Embedding) > 0 {
		scored, err = p.store.HybridSearch(ctx, memory.HybridQuery{
			Embedding:        q.Embedding,
			QueryText:        q.Intent,
			CreatedBy:        q.Caller.Email,
			ExcludeDimension: memory.DimensionKnowledge,
			Status:           memory.StatusActive,
			Limit:            q.Limit,
		})
	} else {
		scored, err = p.store.LexicalSearch(ctx, memory.LexicalQuery{
			QueryText:        q.Intent,
			CreatedBy:        q.Caller.Email,
			ExcludeDimension: memory.DimensionKnowledge,
			Status:           memory.StatusActive,
			Limit:            q.Limit,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("memory search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		hits = append(hits, Hit{
			Text:   scored[i].Record.Content,
			Source: SourceMemory,
			Ref:    scored[i].Record.ID,
			Score:  scored[i].Score,
		})
	}
	return hits, nil
}
