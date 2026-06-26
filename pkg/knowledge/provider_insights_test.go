package knowledge

import (
	"context"
	"errors"
	"testing"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// fakeInsightStore is a SearchableInsightStore stub: it records the text-search
// query and entity-list filters it was given and returns canned results.
type fakeInsightStore struct {
	// text path
	scored       []knowledgekit.ScoredInsight
	searchErr    error
	gotSearch    knowledgekit.InsightSearchQuery
	searchCalled bool

	// entity path
	byURN     map[string][]knowledgekit.Insight
	listErr   error
	gotFilter []knowledgekit.InsightFilter
}

func (f *fakeInsightStore) Search(_ context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	f.searchCalled = true
	f.gotSearch = q
	return f.scored, f.searchErr
}

func (f *fakeInsightStore) List(_ context.Context, filter knowledgekit.InsightFilter) ([]knowledgekit.Insight, int, error) {
	f.gotFilter = append(f.gotFilter, filter)
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	recs := f.byURN[filter.EntityURN]
	return recs, len(recs), nil
}

// TestInsightsProvider_TextPathRetractsNonLive is the #684 regression: an
// unfiltered text/intent search must drop rejected/superseded/rolled-back insights,
// exactly as the entity path does, so a "what do we know" lookup never surfaces
// retracted knowledge.
func TestInsightsProvider_TextPathRetractsNonLive(t *testing.T) {
	s := &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
		{Insight: knowledgekit.Insight{ID: "live-pending", Status: knowledgekit.StatusPending}, Score: 0.9},
		{Insight: knowledgekit.Insight{ID: "live-applied", Status: knowledgekit.StatusApplied}, Score: 0.8},
		{Insight: knowledgekit.Insight{ID: "dead-superseded", Status: knowledgekit.StatusSuperseded}, Score: 0.95},
		{Insight: knowledgekit.Insight{ID: "dead-rejected", Status: knowledgekit.StatusRejected}, Score: 0.7},
		{Insight: knowledgekit.Insight{ID: "dead-rolledback", Status: knowledgekit.StatusRolledBack}, Score: 0.6},
	}}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{Email: "a@example.com"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Ref] = true
	}
	for _, live := range []string{"live-pending", "live-applied"} {
		if !got[live] {
			t.Errorf("live insight %q was dropped from text search", live)
		}
	}
	for _, dead := range []string{"dead-superseded", "dead-rejected", "dead-rolledback"} {
		if got[dead] {
			t.Errorf("retracted insight %q surfaced in unfiltered text search (#684)", dead)
		}
	}
}

// TestInsightsProvider_TextPathHonorsExplicitStatus confirms the retraction only
// applies when no status was requested: an explicit status=superseded still returns
// superseded insights (the store does that filtering; the provider must not re-drop).
func TestInsightsProvider_TextPathHonorsExplicitStatus(t *testing.T) {
	s := &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
		{Insight: knowledgekit.Insight{ID: "sup", Status: knowledgekit.StatusSuperseded}, Score: 0.9},
	}}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent: "q", Status: knowledgekit.StatusSuperseded, Caller: Caller{Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref != "sup" {
		t.Errorf("explicit status=superseded must return the superseded insight, got %+v", hits)
	}
}

func TestInsightsProvider_Metadata(t *testing.T) {
	p := NewInsightsProvider(&fakeInsightStore{})
	if p.Name() != SourceInsights {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopePerUser {
		t.Errorf("Scope = %v, want per-user", p.Scope())
	}
}

func TestInsightsProvider_FailsClosedWithoutEmail(t *testing.T) {
	s := &fakeInsightStore{}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent:     "q",
		EntityURNs: []string{"urn:x"},
		Caller:     Caller{UserID: "uuid-only"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("expected no hits, got %+v", hits)
	}
	if s.searchCalled || len(s.gotFilter) != 0 {
		t.Error("store must not be queried without a caller email")
	}
}

func TestInsightsProvider_TextScopesAndMaps(t *testing.T) {
	s := &fakeInsightStore{
		scored: []knowledgekit.ScoredInsight{
			{Insight: knowledgekit.Insight{ID: "i1", InsightText: "churn = ...", Status: knowledgekit.StatusApproved, CapturedBy: "author@example.com"}, Score: 0.7},
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
	if s.gotSearch.CapturedBy != "a@example.com" {
		t.Errorf("CapturedBy = %q, want scoped to caller email", s.gotSearch.CapturedBy)
	}
	if s.gotSearch.Limit != 5 || len(s.gotSearch.Embedding) == 0 {
		t.Errorf("query not forwarded: %+v", s.gotSearch)
	}
	if len(hits) != 1 || hits[0].Source != SourceInsights || hits[0].Ref != "i1" || hits[0].Text != "churn = ..." {
		t.Errorf("unexpected hit mapping: %+v", hits)
	}
	if hits[0].Status != knowledgekit.StatusApproved {
		t.Errorf("status not carried as provenance: %+v", hits[0])
	}
	if hits[0].CapturedBy != "author@example.com" {
		t.Errorf("author not carried on hit: %+v", hits[0])
	}
}

func TestInsightsProvider_SearchError(t *testing.T) {
	s := &fakeInsightStore{searchErr: errors.New("boom")}
	p := NewInsightsProvider(s)
	_, err := p.Search(context.Background(), Query{Intent: "q", Caller: Caller{Email: "a@example.com"}})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestInsightsProvider_EntityLookupScopedToCaller(t *testing.T) {
	urn := "urn:li:dataset:orders"
	s := &fakeInsightStore{
		byURN: map[string][]knowledgekit.Insight{
			urn: {
				{ID: "i1", InsightText: "amount is gross margin", Status: knowledgekit.StatusApproved, EntityURNs: []string{urn}},
			},
		},
	}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{urn},
		Caller:     Caller{Email: "a@example.com"},
		Limit:      9,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.searchCalled {
		t.Error("text search must not run for an entity-only query")
	}
	if len(s.gotFilter) != 1 {
		t.Fatalf("expected one entity list call, got %+v", s.gotFilter)
	}
	got := s.gotFilter[0]
	if got.EntityURN != urn || got.CapturedBy != "a@example.com" || got.Limit != 9 {
		t.Errorf("entity list not scoped/forwarded: %+v", got)
	}
	if len(hits) != 1 || hits[0].Source != SourceInsights || hits[0].Ref != "i1" || hits[0].Score != entityMatchScore {
		t.Errorf("unexpected entity hit: %+v", hits)
	}
	if len(hits[0].EntityURNs) != 1 || hits[0].EntityURNs[0] != urn {
		t.Errorf("entity urns not carried: %+v", hits[0])
	}
}

func TestInsightsProvider_EntityLookupDropsRetractedWhenNoStatus(t *testing.T) {
	urn := "urn:li:dataset:orders"
	s := &fakeInsightStore{
		byURN: map[string][]knowledgekit.Insight{
			urn: {
				{ID: "live", InsightText: "kept", Status: knowledgekit.StatusApproved, EntityURNs: []string{urn}},
				{ID: "rej", InsightText: "rejected", Status: knowledgekit.StatusRejected, EntityURNs: []string{urn}},
				{ID: "sup", InsightText: "superseded", Status: knowledgekit.StatusSuperseded, EntityURNs: []string{urn}},
				{ID: "rb", InsightText: "rolled back", Status: knowledgekit.StatusRolledBack, EntityURNs: []string{urn}},
			},
		},
	}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{urn},
		Caller:     Caller{Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref != "live" {
		t.Errorf("expected only the live insight, got %+v", hits)
	}
}

func TestInsightsProvider_EntityLookupKeepsRetractedWhenStatusRequested(t *testing.T) {
	urn := "urn:li:dataset:orders"
	s := &fakeInsightStore{
		byURN: map[string][]knowledgekit.Insight{
			urn: {{ID: "rej", InsightText: "rejected", Status: knowledgekit.StatusRejected, EntityURNs: []string{urn}}},
		},
	}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{urn},
		Status:     knowledgekit.StatusRejected,
		Caller:     Caller{Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.gotFilter[0].Status != knowledgekit.StatusRejected {
		t.Errorf("status not forwarded to store: %+v", s.gotFilter[0])
	}
	// An explicit status request is honored verbatim, so the rejected insight surfaces.
	if len(hits) != 1 || hits[0].Ref != "rej" {
		t.Errorf("expected the explicitly requested rejected insight, got %+v", hits)
	}
}

func TestInsightsProvider_EntityAndTextDedup(t *testing.T) {
	urn := "urn:li:dataset:orders"
	dup := knowledgekit.Insight{ID: "dup", InsightText: "dup", Status: knowledgekit.StatusApproved, EntityURNs: []string{urn}}
	s := &fakeInsightStore{
		byURN:  map[string][]knowledgekit.Insight{urn: {dup}},
		scored: []knowledgekit.ScoredInsight{{Insight: dup, Score: 0.5}},
	}
	p := NewInsightsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent:     "dup",
		EntityURNs: []string{urn},
		Caller:     Caller{Email: "a@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected de-duplicated single hit, got %d: %+v", len(hits), hits)
	}
	// The entity path scores it at the max; the text path must not re-add it.
	if hits[0].Score != entityMatchScore {
		t.Errorf("expected entity-path score, got %v", hits[0].Score)
	}
}

func TestInsightsProvider_EntityListError(t *testing.T) {
	s := &fakeInsightStore{listErr: errors.New("db down")}
	p := NewInsightsProvider(s)
	_, err := p.Search(context.Background(), Query{
		EntityURNs: []string{"urn:x"},
		Caller:     Caller{Email: "a@example.com"},
	})
	if err == nil {
		t.Fatal("expected entity list error to propagate")
	}
}
