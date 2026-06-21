package knowledge

import (
	"context"
	"errors"
	"testing"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

type fakeInsightSearcher struct {
	scored []knowledgekit.ScoredInsight
	err    error
	got    knowledgekit.InsightSearchQuery
	called bool
}

func (f *fakeInsightSearcher) Search(_ context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
}

func TestInsightsProvider_Metadata(t *testing.T) {
	p := NewInsightsProvider(&fakeInsightSearcher{})
	if p.Name() != SourceInsights {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopePerUser {
		t.Errorf("Scope = %v, want per-user", p.Scope())
	}
}

func TestInsightsProvider_FailsClosedWithoutEmail(t *testing.T) {
	s := &fakeInsightSearcher{}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{Caller: Caller{UserID: "uuid-only"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits, got %+v", hits)
	}
	if s.called {
		t.Error("searcher must not run without a caller email")
	}
}

func TestInsightsProvider_ScopesAndMaps(t *testing.T) {
	s := &fakeInsightSearcher{
		scored: []knowledgekit.ScoredInsight{
			{Insight: knowledgekit.Insight{ID: "i1", InsightText: "churn = ..."}, Score: 0.7},
		},
	}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent:    "churn",
		Embedding: []float32{0.1},
		Caller:    Caller{Email: "a@example.com"},
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.got.CapturedBy != "a@example.com" {
		t.Errorf("CapturedBy = %q, want scoped to caller email", s.got.CapturedBy)
	}
	if s.got.Limit != 5 || len(s.got.Embedding) == 0 {
		t.Errorf("query not forwarded: %+v", s.got)
	}
	if len(hits) != 1 || hits[0].Source != SourceInsights || hits[0].Ref != "i1" || hits[0].Text != "churn = ..." {
		t.Errorf("unexpected hit mapping: %+v", hits)
	}
}

func TestInsightsProvider_SearchError(t *testing.T) {
	s := &fakeInsightSearcher{err: errors.New("boom")}
	p := NewInsightsProvider(s)
	_, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{Email: "a@example.com"}})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
