package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

type fakePageSearcher struct {
	scored     []knowledgepage.ScoredPage
	err        error
	got        knowledgepage.SearchQuery
	called     bool
	pages      []knowledgepage.PageRef // reverse-lookup result
	reverseErr error
	gotRef     knowledgepage.EntityRef
	page       *knowledgepage.Page // Get result
	getErr     error
	gotGetID   string
}

func (f *fakePageSearcher) Get(_ context.Context, id string) (*knowledgepage.Page, error) {
	f.gotGetID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.page, nil
}

func (f *fakePageSearcher) Search(_ context.Context, q knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
}

func (f *fakePageSearcher) ListPagesReferencing(_ context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	f.gotRef = ref
	return f.pages, f.reverseErr
}

func TestKnowledgePagesProvider_Metadata(t *testing.T) {
	p := NewKnowledgePagesProvider(&fakePageSearcher{})
	if p.Name() != SourceKnowledgePages {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestKnowledgePagesProvider_NoIntentSkips(t *testing.T) {
	s := &fakePageSearcher{}
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
	s := &fakePageSearcher{
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
	if hits[0].Reference != "mcp:knowledge_page:kp1" {
		t.Errorf("canonical reference = %q, want mcp:knowledge_page:kp1", hits[0].Reference)
	}
	// No-summary page renders as just the title.
	if hits[1].Text != "Seasons" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "Seasons")
	}
}

func TestKnowledgePagesProvider_SearchError(t *testing.T) {
	p := NewKnowledgePagesProvider(&fakePageSearcher{err: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

// TestKnowledgePagesProvider_ReverseLookupByEntity proves #634: an entity-keyed
// search returns the pages that REFERENCE the entity, attributed to that URN.
func TestKnowledgePagesProvider_ReverseLookupByEntity(t *testing.T) {
	s := &fakePageSearcher{pages: []knowledgepage.PageRef{
		{ID: "kp1", Slug: "vocab", Title: "ACME Vocabulary"},
		{ID: "kp2", Slug: "guide", Title: "Query Guide"},
	}}
	p := NewKnowledgePagesProvider(s)
	urn := "mcp:connection:(trino,acme)"
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{urn}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Reference != "mcp:knowledge_page:kp1" {
		t.Errorf("entity-path canonical reference = %q, want mcp:knowledge_page:kp1", hits[0].Reference)
	}
	if hits[0].Source != SourceKnowledgePages || hits[0].Ref != "kp1" || hits[0].Text != "ACME Vocabulary" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	if len(hits[0].EntityURNs) != 1 || hits[0].EntityURNs[0] != urn {
		t.Errorf("hit must be attributed to the queried entity: %+v", hits[0].EntityURNs)
	}
	if s.gotRef.TargetType != knowledgepage.RefTargetConnection || s.gotRef.ConnectionName != "acme" {
		t.Errorf("URN should parse to a connection ref: %+v", s.gotRef)
	}
}

// TestKnowledgePagesProvider_EntityAndTextMergeDedup proves a page returned by both
// the reverse lookup and the text search appears once.
func TestKnowledgePagesProvider_EntityAndTextMergeDedup(t *testing.T) {
	s := &fakePageSearcher{
		pages: []knowledgepage.PageRef{{ID: "kp1", Title: "Seasons"}},
		scored: []knowledgepage.ScoredPage{
			{Page: knowledgepage.Page{ID: "kp1", Title: "Seasons"}, Score: 0.9},
			{Page: knowledgepage.Page{ID: "kp2", Title: "Other"}, Score: 0.5},
		},
	}
	p := NewKnowledgePagesProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent:     "seasons",
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,x.y.z,PROD)"},
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2 (kp1 deduped across entity+text)", len(hits))
	}
	// Entity hit (kp1) comes first, then the text-only hit (kp2).
	if hits[0].Ref != "kp1" || hits[1].Ref != "kp2" {
		t.Errorf("unexpected refs/order: %s, %s", hits[0].Ref, hits[1].Ref)
	}
}

// TestKnowledgePagesProvider_ReverseLookupError covers a reverse-lookup failure being
// skipped (not fatal) while the text path still runs; and an unparseable URN skipped.
func TestKnowledgePagesProvider_ReverseLookupError(t *testing.T) {
	s := &fakePageSearcher{
		reverseErr: errors.New("boom"),
		scored:     []knowledgepage.ScoredPage{{Page: knowledgepage.Page{ID: "kp1", Title: "T"}, Score: 0.5}},
	}
	p := NewKnowledgePagesProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent:     "q",
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,x.y.z,PROD)"},
	})
	if err != nil {
		t.Fatalf("a reverse-lookup error must not fail the search: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref != "kp1" {
		t.Fatalf("the text path should still produce a hit: %+v", hits)
	}
}

func TestKnowledgePagesProvider_UnparseableURNSkipped(t *testing.T) {
	s := &fakePageSearcher{pages: []knowledgepage.PageRef{{ID: "kp1", Title: "T"}}}
	p := NewKnowledgePagesProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"garbage"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("an unparseable URN should yield no hits, got %+v", hits)
	}
	if (s.gotRef != knowledgepage.EntityRef{}) {
		t.Errorf("reverse lookup should be skipped for an unparseable URN, got %+v", s.gotRef)
	}
}

func TestPagesProvider_Fetch(t *testing.T) {
	t.Run("returns full body for a knowledge_page reference", func(t *testing.T) {
		s := &fakePageSearcher{page: &knowledgepage.Page{
			ID:    "kp_1",
			Title: "Fiscal Calendar",
			Body:  "# Fiscal Calendar\n\nQ1 begins in February.",
		}}
		ref := knowledgepage.PageReference("kp_1")
		doc, owned, err := NewKnowledgePagesProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v, want owned, no error", owned, err)
		}
		if s.gotGetID != "kp_1" {
			t.Errorf("Get id = %q, want kp_1", s.gotGetID)
		}
		if doc.Body != "# Fiscal Calendar\n\nQ1 begins in February." {
			t.Errorf("Body = %q", doc.Body)
		}
		if doc.Source != SourceKnowledgePages || doc.Reference != ref || doc.Title != "Fiscal Calendar" {
			t.Errorf("doc = %+v", doc)
		}
	})

	t.Run("declines a non-page reference", func(t *testing.T) {
		s := &fakePageSearcher{}
		_, owned, err := NewKnowledgePagesProvider(s).Fetch(context.Background(), "mcp:asset:a1", Caller{})
		if owned || err != nil {
			t.Errorf("owned=%v err=%v, want declined", owned, err)
		}
		if s.gotGetID != "" {
			t.Errorf("Get should not be called for a non-page reference, got %q", s.gotGetID)
		}
	})

	t.Run("missing page is not-found", func(t *testing.T) {
		s := &fakePageSearcher{getErr: knowledgepage.ErrNotFound}
		_, owned, err := NewKnowledgePagesProvider(s).Fetch(context.Background(), knowledgepage.PageReference("gone"), Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("soft-deleted page is not-found", func(t *testing.T) {
		del := time.Now()
		s := &fakePageSearcher{page: &knowledgepage.Page{ID: "kp_1", DeletedAt: &del}}
		_, owned, err := NewKnowledgePagesProvider(s).Fetch(context.Background(), knowledgepage.PageReference("kp_1"), Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound for a deleted page", owned, err)
		}
	})

	t.Run("store error surfaces as a real error", func(t *testing.T) {
		s := &fakePageSearcher{getErr: errors.New("boom")}
		_, owned, err := NewKnowledgePagesProvider(s).Fetch(context.Background(), knowledgepage.PageReference("kp_1"), Caller{})
		if !owned || err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + a non-not-found error", owned, err)
		}
	})
}
