package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

type fakeKnowledgePageSearcher struct {
	scored []knowledgepage.ScoredPage
	err    error
	got    knowledgepage.SearchQuery
	called bool
}

func (f *fakeKnowledgePageSearcher) Search(_ context.Context, q knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
}

func TestKnowledgePagesProvider_Metadata(t *testing.T) {
	p := NewKnowledgePagesProvider(&fakeKnowledgePageSearcher{})
	if p.Name() != SourceKnowledgePages {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestKnowledgePagesProvider_NoIntentSkips(t *testing.T) {
	s := &fakeKnowledgePageSearcher{}
	p := NewKnowledgePagesProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil || s.called {
		t.Error("knowledge pages provider should not run without an intent")
	}
}

func TestKnowledgePagesProvider_MapsAndForwards(t *testing.T) {
	s := &fakeKnowledgePageSearcher{
		scored: []knowledgepage.ScoredPage{
			{Page: knowledgepage.Page{ID: "kp1", Title: "Fiscal Calendar", Summary: "How quarters are defined"}, Score: 0.8},
			{Page: knowledgepage.Page{ID: "kp2", Title: "Seasons"}, Score: 0.4},
		},
	}
	p := NewKnowledgePagesProvider(s)
	hits, err := p.Search(context.Background(), Query{Intent: "fiscal", Embedding: []float32{0.1}, Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.got.QueryText != "fiscal" || s.got.Limit != 5 || len(s.got.Embedding) == 0 {
		t.Errorf("query not forwarded: %+v", s.got)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Source != SourceKnowledgePages || hits[0].Ref != "kp1" || hits[0].Text != "Fiscal Calendar\nHow quarters are defined" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	// No-summary page renders as just the title.
	if hits[1].Text != "Seasons" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "Seasons")
	}
}

func TestKnowledgePagesProvider_SearchError(t *testing.T) {
	p := NewKnowledgePagesProvider(&fakeKnowledgePageSearcher{err: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
