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
	err        error
	gotHybrid  memory.HybridQuery
	gotLexical memory.LexicalQuery
	hybridHit  bool
	lexicalHit bool
}

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
	hits, err := p.Search(context.Background(), Query{Caller: Caller{UserID: "uuid-only"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits, got %+v", hits)
	}
	if store.hybridHit || store.lexicalHit {
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
	if len(hits) != 1 || hits[0].Source != SourceMemory || hits[0].Ref != "m1" || hits[0].Text != "hello" {
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
	if store.gotLexical.CreatedBy != "a@example.com" {
		t.Errorf("CreatedBy = %q", store.gotLexical.CreatedBy)
	}
	if store.gotLexical.ExcludeDimension != memory.DimensionKnowledge {
		t.Errorf("ExcludeDimension = %q, want knowledge", store.gotLexical.ExcludeDimension)
	}
	if store.gotLexical.Status != memory.StatusActive {
		t.Errorf("Status = %q, want active", store.gotLexical.Status)
	}
}

// The knowledge dimension is excluded in SQL (via ExcludeDimension) rather than
// after LIMIT, so the provider must request the exclusion on every query; this
// asserts it does, on both the hybrid and lexical arms.
func TestMemoryProvider_RequestsKnowledgeDimensionExclusion(t *testing.T) {
	store := &fakeMemoryStore{}
	p := NewMemoryProvider(store)

	if _, err := p.Search(context.Background(), Query{Intent: "q", Embedding: []float32{0.1}, Caller: Caller{Email: "a@example.com"}}); err != nil {
		t.Fatalf("hybrid search error: %v", err)
	}
	if store.gotHybrid.ExcludeDimension != memory.DimensionKnowledge {
		t.Errorf("hybrid ExcludeDimension = %q, want knowledge", store.gotHybrid.ExcludeDimension)
	}

	store = &fakeMemoryStore{}
	p = NewMemoryProvider(store)
	if _, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{Email: "a@example.com"}}); err != nil {
		t.Fatalf("lexical search error: %v", err)
	}
	if store.gotLexical.ExcludeDimension != memory.DimensionKnowledge {
		t.Errorf("lexical ExcludeDimension = %q, want knowledge", store.gotLexical.ExcludeDimension)
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
