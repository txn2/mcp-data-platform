package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

type fakeMemoryStore struct {
	hybrid     []memory.ScoredRecord
	lexical    []memory.ScoredRecord
	entity     map[string][]memory.Record // urn -> records
	err        error
	gotHybrid  memory.HybridQuery
	gotLexical memory.LexicalQuery
	gotEntity  []entityCall
	hybridHit  bool
	lexicalHit bool
}

type entityCall struct{ urn, persona, createdBy string }

func (f *fakeMemoryStore) HybridSearch(_ context.Context, q memory.HybridQuery) ([]memory.ScoredRecord, error) {
	f.hybridHit = true
	f.gotHybrid = q
	return f.hybrid, f.err
}

func (f *fakeMemoryStore) LexicalSearch(_ context.Context, q memory.LexicalQuery) ([]memory.ScoredRecord, error) {
	f.lexicalHit = true
	f.gotLexical = q
	return f.lexical, f.err
}

func (f *fakeMemoryStore) EntityLookup(_ context.Context, urn, persona, createdBy string) ([]memory.Record, error) {
	f.gotEntity = append(f.gotEntity, entityCall{urn, persona, createdBy})
	if f.err != nil {
		return nil, f.err
	}
	return f.entity[urn], nil
}

func TestMemoryProvider_Metadata(t *testing.T) {
	p := NewMemoryProvider(&fakeMemoryStore{})
	if p.Name() != SourceMemory {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopePerUser {
		t.Errorf("Scope = %v, want per-user", p.Scope())
	}
}

func TestMemoryProvider_FailsClosedWithoutEmail(t *testing.T) {
	store := &fakeMemoryStore{}
	p := NewMemoryProvider(store)
	hits, err := p.Search(context.Background(), Query{Intent: "q", EntityURNs: []string{"urn:x"}, Caller: Caller{UserID: "uuid-only"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits, got %+v", hits)
	}
	if store.hybridHit || store.lexicalHit || len(store.gotEntity) != 0 {
		t.Error("store must not be queried without a caller email")
	}
}

func TestMemoryProvider_HybridWhenEmbeddingPresent(t *testing.T) {
	store := &fakeMemoryStore{
		hybrid: []memory.ScoredRecord{
			{Record: memory.Record{ID: "m1", Content: "hello", Dimension: memory.DimensionPreference}, Score: 0.9},
		},
	}
	p := NewMemoryProvider(store)
	hits, err := p.Search(context.Background(), Query{
		Intent:    "q",
		Embedding: []float32{0.1},
		Caller:    Caller{Email: "a@example.com"},
		Limit:     7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !store.hybridHit || store.lexicalHit {
		t.Fatal("expected hybrid search, not lexical")
	}
	if store.gotHybrid.CreatedBy != "a@example.com" {
		t.Errorf("CreatedBy = %q, want scoped to caller email", store.gotHybrid.CreatedBy)
	}
	if store.gotHybrid.Limit != 7 {
		t.Errorf("Limit = %d, want 7", store.gotHybrid.Limit)
	}
	if store.gotHybrid.ExcludeDimension != memory.DimensionKnowledge {
		t.Errorf("ExcludeDimension = %q, want knowledge", store.gotHybrid.ExcludeDimension)
	}
	if store.gotHybrid.Status != memory.StatusActive {
		t.Errorf("Status = %q, want active", store.gotHybrid.Status)
	}
	if len(hits) != 1 || hits[0].Source != SourceMemory || hits[0].Ref != "m1" || hits[0].Text != "hello" || hits[0].Dimension != memory.DimensionPreference {
		t.Errorf("unexpected hit mapping: %+v", hits)
	}
}

func TestMemoryProvider_LexicalWhenNoEmbedding(t *testing.T) {
	store := &fakeMemoryStore{
		lexical: []memory.ScoredRecord{
			{Record: memory.Record{ID: "m2", Content: "x", Dimension: memory.DimensionEvent}, Score: 0.3},
		},
	}
	p := NewMemoryProvider(store)
	_, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{Email: "a@example.com"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.hybridHit || !store.lexicalHit {
		t.Fatal("expected lexical search, not hybrid")
	}
	if store.gotLexical.ExcludeDimension != memory.DimensionKnowledge || store.gotLexical.Status != memory.StatusActive {
		t.Errorf("lexical query not scoped: %+v", store.gotLexical)
	}
}

func TestMemoryProvider_EntityLookupScopedToCaller(t *testing.T) {
	store := &fakeMemoryStore{
		entity: map[string][]memory.Record{
			"urn:li:dataset:orders": {
				{ID: "e1", Content: "orders note", Dimension: memory.DimensionEntity, EntityURNs: []string{"urn:li:dataset:orders"}},
				{ID: "k1", Content: "insight row", Dimension: memory.DimensionKnowledge},
			},
		},
	}
	p := NewMemoryProvider(store)
	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{"urn:li:dataset:orders"},
		Caller:     Caller{Email: "a@example.com", Persona: "analyst"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Scope key forwarded.
	if len(store.gotEntity) != 1 || store.gotEntity[0].createdBy != "a@example.com" || store.gotEntity[0].persona != "analyst" {
		t.Fatalf("entity lookup not scoped to caller: %+v", store.gotEntity)
	}
	// Knowledge-dimension row excluded (owned by insights provider).
	if len(hits) != 1 || hits[0].Ref != "e1" || hits[0].Score != entityMatchScore {
		t.Errorf("expected only the non-knowledge entity hit at max score, got %+v", hits)
	}
}

func TestMemoryProvider_LooksUpEveryEntityURN(t *testing.T) {
	// The router hands the provider the already lineage-expanded URN set; the
	// provider looks each up and unions the records.
	store := &fakeMemoryStore{
		entity: map[string][]memory.Record{
			"urn:a": {{ID: "ra", Content: "a", Dimension: memory.DimensionEntity}},
			"urn:b": {{ID: "rb", Content: "b", Dimension: memory.DimensionEntity}},
		},
	}
	p := NewMemoryProvider(store)
	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{"urn:a", "urn:b"},
		Caller:     Caller{Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits across both URNs, got %d: %+v", len(hits), hits)
	}
}

func TestMemoryProvider_EntityAndTextDedup(t *testing.T) {
	// The same record id reachable by both entity and text paths appears once.
	rec := memory.Record{ID: "dup", Content: "dup", Dimension: memory.DimensionEntity}
	store := &fakeMemoryStore{
		entity:  map[string][]memory.Record{"urn:x": {rec}},
		lexical: []memory.ScoredRecord{{Record: rec, Score: 0.4}},
	}
	p := NewMemoryProvider(store)
	hits, err := p.Search(context.Background(), Query{
		Intent:     "dup",
		EntityURNs: []string{"urn:x"},
		Caller:     Caller{Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected de-duplicated single hit, got %d: %+v", len(hits), hits)
	}
}

func TestMemoryProvider_SearchError(t *testing.T) {
	store := &fakeMemoryStore{err: errors.New("db down")}
	p := NewMemoryProvider(store)
	_, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{Email: "a@example.com"}})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
