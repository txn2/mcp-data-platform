package knowledge

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// SourceMemory is the provenance label for memory-provider hits.
const SourceMemory = "memory"

// entityMatchScore is the relevance assigned to an exact entity-keyed match.
// Entity lookups are not similarity-ranked, so like the legacy recall they take
// the maximum score; per-provider normalization then places them at the top of
// the memory provider's contribution.
const entityMatchScore = 1.0

// memorySearcher is the slice of memory.Store the memory provider needs: the
// two relevance-search primitives plus the entity-keyed lookup. Declared here
// so the provider depends on a capability, not the whole store, and so tests
// can supply a fake.
type memorySearcher interface {
	HybridSearch(ctx context.Context, q memory.HybridQuery) ([]memory.ScoredRecord, error)
	LexicalSearch(ctx context.Context, q memory.LexicalQuery) ([]memory.ScoredRecord, error)
	EntityLookup(ctx context.Context, urn, persona, createdBy string) ([]memory.Record, error)
	// Get reads one memory record by id (the read half of search: a hit's
	// reference dereferenced to the full record).
	Get(ctx context.Context, id string) (*memory.Record, error)
}

// MemoryProvider exposes a caller's personal memory to the knowledge router.
//
// It is per-user: results are restricted to records the caller owns
// (memory_records.created_by == caller email), the same identity the portal's
// "my knowledge" search scopes on. It serves two query shapes: relevance search
// on Intent, and an exact entity-keyed lookup on EntityURNs. Lineage expansion
// of the entity URNs is the Router's responsibility (so it runs once for every
// entity-keyed provider), so this provider takes the URN set as given.
//
// It deliberately omits the knowledge dimension on both paths. Captured
// insights and remembered knowledge are knowledge-dimension memory rows owned
// by the InsightsProvider; surfacing them here too would double-list the same
// record. This provider covers the caller's non-knowledge memory (preferences,
// events, entities, relationships).
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

// Search returns the caller's active, non-knowledge memory. When EntityURNs are
// given it does an exact entity lookup (lineage-expanded when configured); when
// Intent is given it ranks by relevance (hybrid with an embedding, lexical
// otherwise). Results from both paths are merged and de-duplicated by record id.
// It fails closed: an empty caller email yields no results rather than an
// unscoped search across all users.
func (p *MemoryProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
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

// searchByEntity recalls the caller's memory linked to the query's entity URNs
// (already lineage-expanded by the Router when an expander is configured).
// Knowledge-dimension and already-seen records are skipped.
func (p *MemoryProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if len(q.EntityURNs) == 0 {
		return nil, nil
	}

	var hits []Hit
	for _, urn := range q.EntityURNs {
		records, err := p.store.EntityLookup(ctx, urn, q.Caller.Persona, q.Caller.Email)
		if err != nil {
			return nil, fmt.Errorf("memory entity lookup: %w", err)
		}
		for i := range records {
			if records[i].Dimension == memory.DimensionKnowledge || seen[records[i].ID] {
				continue
			}
			seen[records[i].ID] = true
			hits = append(hits, recordHit(records[i], entityMatchScore))
		}
	}
	return hits, nil
}

// searchByText ranks the caller's active, non-knowledge memory by relevance to
// the intent. Hybrid when the query carries an embedding, lexical otherwise.
func (p *MemoryProvider) searchByText(ctx context.Context, q Query, seen map[string]bool) ([]Hit, error) {
	if q.Intent == "" {
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
		if seen[scored[i].Record.ID] {
			continue
		}
		seen[scored[i].Record.ID] = true
		hits = append(hits, recordHit(scored[i].Record, scored[i].Score))
	}
	return hits, nil
}

// recordHit maps a memory record to a knowledge hit, carrying its dimension and
// linked entity URNs as provenance, plus the canonical mcp:memory:<id> reference
// so an agent can read the full record with fetch (#699).
func recordHit(r memory.Record, score float64) Hit {
	return Hit{
		Text:       r.Content,
		Source:     SourceMemory,
		Ref:        r.ID,
		Score:      score,
		Dimension:  r.Dimension,
		EntityURNs: r.EntityURNs,
		Reference:  knowledgepage.MemoryRef(r.ID),
	}
}

// Fetch dereferences an mcp:memory:<id> reference to the full memory record (#699),
// following the AssetsProvider precedent. Memory is per-user, so the read is scoped
// to the caller exactly as Search is: it returns a record only when the caller owns
// it (created_by == caller email), it is active, and it is not a knowledge-dimension
// record (those are insights, addressed by mcp:insight:); anything else, a missing
// id, or an anonymous caller is ErrNotFound, so fetch never reveals a record the
// caller could not have searched (nor even its existence).
func (p *MemoryProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetMemory {
		return nil, false, nil //nolint:nilerr // a non-memory reference is a decline, not a failure
	}
	if caller.Email == "" {
		return nil, true, ErrNotFound
	}
	rec, err := p.store.Get(ctx, parsed.MemoryID)
	if err != nil {
		// The memory store reports a missing id as memory.ErrRecordNotFound (it does
		// NOT surface sql.ErrNoRows), so a stale citation is a clean not-found.
		if errors.Is(err, memory.ErrRecordNotFound) {
			return nil, true, ErrNotFound
		}
		return nil, true, fmt.Errorf("getting memory %s: %w", parsed.MemoryID, err)
	}
	// Fail closed: a non-owner, knowledge-dimension (insight), inactive, or missing
	// record is all indistinguishable to the caller, so neither content nor existence
	// of a record the caller could not search leaks.
	if rec == nil || rec.CreatedBy != caller.Email ||
		rec.Dimension == memory.DimensionKnowledge || rec.Status != memory.StatusActive {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference:  ref,
		Source:     SourceMemory,
		Body:       rec.Content,
		Content:    rec,
		EntityURNs: rec.EntityURNs,
	}, true, nil
}
