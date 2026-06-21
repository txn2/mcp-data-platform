package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

type fakeTableSearcher struct {
	results []semantic.TableSearchResult
	err     error
	got     semantic.SearchFilter
	called  bool
}

func (f *fakeTableSearcher) SearchTables(_ context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	f.called = true
	f.got = filter
	return f.results, f.err
}

func TestDatahubProvider_Metadata(t *testing.T) {
	p := NewDatahubProvider(&fakeTableSearcher{})
	if p.Name() != SourceDatahub {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestDatahubProvider_NoIntentSkips(t *testing.T) {
	s := &fakeTableSearcher{}
	p := NewDatahubProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil || s.called {
		t.Error("datahub provider should not run without an intent")
	}
}

func TestDatahubProvider_MapsAndRanks(t *testing.T) {
	s := &fakeTableSearcher{
		results: []semantic.TableSearchResult{
			{URN: "urn:li:dataset:orders", Name: "orders", Description: "order facts"},
			{URN: "urn:li:dataset:returns", Name: "returns"},
		},
	}
	p := NewDatahubProvider(s)
	hits, err := p.Search(context.Background(), Query{Intent: "orders", Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.got.Query != "orders" || s.got.Limit != 5 {
		t.Errorf("filter not forwarded: %+v", s.got)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	// First result ranks above the second (positional score).
	if hits[0].Score <= hits[1].Score {
		t.Errorf("expected descending positional score, got %v then %v", hits[0].Score, hits[1].Score)
	}
	if hits[0].Source != SourceDatahub || hits[0].Ref != "urn:li:dataset:orders" || hits[0].Text != "orders\norder facts" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	if len(hits[0].EntityURNs) != 1 || hits[0].EntityURNs[0] != "urn:li:dataset:orders" {
		t.Errorf("hit[0] entity urns = %+v", hits[0].EntityURNs)
	}
	// No-description result renders as just the name.
	if hits[1].Text != "returns" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "returns")
	}
}

func TestDatahubProvider_SearchError(t *testing.T) {
	p := NewDatahubProvider(&fakeTableSearcher{err: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestPositionalScore(t *testing.T) {
	if positionalScore(0, 1) != entityMatchScore {
		t.Errorf("single result should score %v", entityMatchScore)
	}
	if positionalScore(0, 3) <= positionalScore(2, 3) {
		t.Error("first of three should outrank last")
	}
}
