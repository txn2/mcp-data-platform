package knowledge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// fakeInsightStore is a SearchableInsightStore stub: it records the text-search
// queries and entity-list filters it was given and returns canned results. Both
// are recorded as slices because the provider runs two arms per search (the
// caller's own insights, then the organization's applied ones), so a test that
// looked at one call could not tell the arms apart.
type fakeInsightStore struct {
	// text path
	scored       []knowledgekit.ScoredInsight
	searchErr    error
	gotSearches  []knowledgekit.InsightSearchQuery
	searchCalled bool

	// entity path
	byURN     map[string][]knowledgekit.Insight
	listErr   error
	gotFilter []knowledgekit.InsightFilter

	// get path
	getInsight *knowledgekit.Insight
	getErr     error
	gotGetID   string
}

func (f *fakeInsightStore) Get(_ context.Context, id string) (*knowledgekit.Insight, error) {
	f.gotGetID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getInsight, nil
}

func (f *fakeInsightStore) Search(_ context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	f.searchCalled = true
	f.gotSearches = append(f.gotSearches, q)
	return f.scored, f.searchErr
}

// ownerSearch returns the query the owner arm issued (the first one; the shared
// arm always follows it).
func (f *fakeInsightStore) ownerSearch(t *testing.T) knowledgekit.InsightSearchQuery {
	t.Helper()
	if len(f.gotSearches) == 0 {
		t.Fatal("no text search was issued")
	}
	return f.gotSearches[0]
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
		// A rollback returns an insight to pending (#1257), so it is live again for
		// its capturer exactly as it was before it was applied.
		{Insight: knowledgekit.Insight{
			ID: "live-returned", Status: knowledgekit.StatusPending,
			AppliedBy: "admin@example.com", ChangesetRef: "cs-1",
		}, Score: 0.6},
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
	for _, live := range []string{"live-pending", "live-applied", "live-returned"} {
		if !got[live] {
			t.Errorf("live insight %q was dropped from text search", live)
		}
	}
	for _, dead := range []string{"dead-superseded", "dead-rejected"} {
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
	owner := s.ownerSearch(t)
	if owner.CapturedBy != "a@example.com" || owner.Shared {
		t.Errorf("owner arm = %+v, want scoped to caller email and not shared", owner)
	}
	if owner.Limit != 5 || len(owner.Embedding) == 0 {
		t.Errorf("query not forwarded: %+v", owner)
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
	got := s.gotFilter[0]
	if got.EntityURN != urn || got.CapturedBy != "a@example.com" || got.Shared || got.Limit != 9 {
		t.Errorf("owner entity list not scoped/forwarded: %+v", got)
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

func TestInsightsProvider_Fetch(t *testing.T) {
	const owner = "alice@example.com"
	ref := knowledgepage.InsightRef("ins_1")
	live := func() *knowledgekit.Insight {
		return &knowledgekit.Insight{ID: "ins_1", CapturedBy: owner, Status: knowledgekit.StatusApproved, InsightText: "orders.total excludes tax", EntityURNs: []string{"urn:li:dataset:orders"}}
	}

	t.Run("returns the owner's live insight", func(t *testing.T) {
		s := &fakeInsightStore{getInsight: live()}
		doc, owned, err := NewInsightsProvider(s).Fetch(context.Background(), ref, Caller{Email: owner})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v", owned, err)
		}
		if s.gotGetID != "ins_1" {
			t.Errorf("Get id = %q", s.gotGetID)
		}
		if doc.Source != SourceInsights || doc.Body != "orders.total excludes tax" {
			t.Errorf("doc = %+v", doc)
		}
		if len(doc.EntityURNs) != 1 {
			t.Errorf("EntityURNs = %+v", doc.EntityURNs)
		}
	})

	t.Run("declines a non-insight reference", func(t *testing.T) {
		s := &fakeInsightStore{}
		_, owned, err := NewInsightsProvider(s).Fetch(context.Background(), "mcp:memory:m1", Caller{Email: owner})
		if owned || err != nil {
			t.Errorf("owned=%v err=%v, want declined", owned, err)
		}
		if s.gotGetID != "" {
			t.Errorf("Get must not be called for a non-insight reference")
		}
	})

	t.Run("anonymous caller is not-found", func(t *testing.T) {
		s := &fakeInsightStore{getInsight: live()}
		_, owned, err := NewInsightsProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) || s.gotGetID != "" {
			t.Errorf("owned=%v err=%v get=%q, want owned + ErrNotFound + no Get", owned, err, s.gotGetID)
		}
	})

	t.Run("another owner's insight is not-found", func(t *testing.T) {
		in := live()
		in.CapturedBy = "bob@example.com"
		s := &fakeInsightStore{getInsight: in}
		_, owned, err := NewInsightsProvider(s).Fetch(context.Background(), ref, Caller{Email: owner})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for another owner", owned, err)
		}
	})

	t.Run("a retracted insight is still fetchable by its owner", func(t *testing.T) {
		// Search retracts non-live insights only from the default discovery path; an
		// explicit status query still surfaces them with a reference, so fetch must
		// dereference any owned insight regardless of status.
		in := live()
		in.Status = knowledgekit.StatusSuperseded
		s := &fakeInsightStore{getInsight: in}
		doc, owned, err := NewInsightsProvider(s).Fetch(context.Background(), ref, Caller{Email: owner})
		if !owned || err != nil || doc.Body != "orders.total excludes tax" {
			t.Errorf("owned=%v err=%v doc=%+v, want the retracted insight returned to its owner", owned, err, doc)
		}
	})

	t.Run("a stale id (the store's not-found sentinel) is not-found", func(t *testing.T) {
		// Insights are memory_records behind the adapter, so a missing id surfaces
		// memory.ErrRecordNotFound (wrapped), NOT sql.ErrNoRows.
		s := &fakeInsightStore{getErr: fmt.Errorf("getting insight record: %w", memory.ErrRecordNotFound)}
		_, owned, err := NewInsightsProvider(s).Fetch(context.Background(), ref, Caller{Email: owner})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound", owned, err)
		}
	})

	t.Run("a genuine store error surfaces", func(t *testing.T) {
		s := &fakeInsightStore{getErr: errors.New("db down")}
		_, owned, err := NewInsightsProvider(s).Fetch(context.Background(), ref, Caller{Email: owner})
		if !owned || err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want a non-not-found error", owned, err)
		}
	})
}

// scopingInsightStore models the real store contract the shared arm depends on:
// List and Search return only records matching the owner predicate, and the
// Shared flag replaces that predicate with "applied, any owner" (the override
// memoryInsightAdapter.sharedInsightScope performs). A fake that ignored the
// scope would let a provider bug that leaks another owner's private insight pass
// unnoticed, which is the whole risk of this feature.
type scopingInsightStore struct {
	all []knowledgekit.Insight
}

func (s *scopingInsightStore) visible(capturedBy, status string, shared bool, urn string) []knowledgekit.Insight {
	var out []knowledgekit.Insight
	for _, in := range s.all {
		switch {
		case shared && in.Status != knowledgekit.StatusApplied:
			continue
		case !shared && in.CapturedBy != capturedBy:
			continue
		}
		if status != "" && in.Status != status {
			continue
		}
		if urn != "" && !slices.Contains(in.EntityURNs, urn) {
			continue
		}
		out = append(out, in)
	}
	return out
}

func (s *scopingInsightStore) Search(_ context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	visible := s.visible(q.CapturedBy, q.Status, q.Shared, "")
	out := make([]knowledgekit.ScoredInsight, 0, len(visible))
	for _, in := range visible {
		out = append(out, knowledgekit.ScoredInsight{Insight: in, Score: 0.5})
	}
	return out, nil
}

func (s *scopingInsightStore) List(_ context.Context, f knowledgekit.InsightFilter) ([]knowledgekit.Insight, int, error) {
	out := s.visible(f.CapturedBy, f.Status, f.Shared, f.EntityURN)
	return out, len(out), nil
}

func (s *scopingInsightStore) Get(_ context.Context, id string) (*knowledgekit.Insight, error) {
	for i := range s.all {
		if s.all[i].ID == id {
			return &s.all[i], nil
		}
	}
	return nil, fmt.Errorf("getting insight record: %w", memory.ErrRecordNotFound)
}

// orgInsightsURN is the entity every fixture insight hangs off, so the entity
// arm and the text arm run over the same corpus.
const orgInsightsURN = "urn:li:dataset:orders"

// orgInsights is the fixture for the cross-identity tests: one capturer (bob)
// with an insight at every status, plus one of the caller's own.
func orgInsights() []knowledgekit.Insight {
	const urn = orgInsightsURN
	mk := func(id, by, status string) knowledgekit.Insight {
		return knowledgekit.Insight{
			ID: id, CapturedBy: by, Status: status,
			InsightText: id + " text", EntityURNs: []string{urn},
		}
	}
	return []knowledgekit.Insight{
		mk("bob-applied", "bob@example.com", knowledgekit.StatusApplied),
		mk("bob-pending", "bob@example.com", knowledgekit.StatusPending),
		mk("bob-approved", "bob@example.com", knowledgekit.StatusApproved),
		mk("bob-rejected", "bob@example.com", knowledgekit.StatusRejected),
		mk("bob-superseded", "bob@example.com", knowledgekit.StatusSuperseded),
		mk("alice-pending", "alice@example.com", knowledgekit.StatusPending),
	}
}

// TestInsightsProvider_SharedArm is the #980 B2 acceptance test: an insight that
// was reviewed and applied by one person is knowledge the organization has, and
// must reach a different person's search, while everything short of applied
// stays private to its capturer. The benchmark measured this as a cross-identity
// transfer gap against a much higher personal-recall rate.
func TestInsightsProvider_SharedArm(t *testing.T) {
	const (
		urn   = orgInsightsURN
		alice = "alice@example.com"
	)

	// Both query shapes must behave the same way: an agent reaches insights
	// either by naming an entity or by asking a question.
	for _, arm := range []struct {
		name  string
		query Query
	}{
		{"entity path", Query{EntityURNs: []string{urn}, Caller: Caller{Email: alice}}},
		{"text path", Query{Intent: "orders", Caller: Caller{Email: alice}}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			s := &scopingInsightStore{all: orgInsights()}
			hits, err := NewInsightsProvider(s).Search(context.Background(), arm.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := map[string]bool{}
			for _, h := range hits {
				got[h.Ref] = true
			}

			if !got["bob-applied"] {
				t.Errorf("another capturer's applied insight did not reach the caller: %+v", hits)
			}
			if !got["alice-pending"] {
				t.Errorf("caller's own insight was dropped: %+v", hits)
			}
			for _, private := range []string{
				"bob-pending", "bob-approved", "bob-rejected", "bob-superseded",
			} {
				if got[private] {
					t.Errorf("another capturer's %q insight leaked into the caller's search", private)
				}
			}
		})
	}
}

// TestInsightsProvider_SharedArmAttributesTheCapturer checks that a hit from
// another person carries their email, so the caller can see whose knowledge they
// are reading rather than mistaking it for their own capture.
func TestInsightsProvider_SharedArmAttributesTheCapturer(t *testing.T) {
	const urn = orgInsightsURN
	s := &scopingInsightStore{all: orgInsights()}
	hits, err := NewInsightsProvider(s).Search(context.Background(), Query{
		EntityURNs: []string{urn},
		Caller:     Caller{Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, h := range hits {
		if h.Ref == "bob-applied" {
			if h.CapturedBy != "bob@example.com" {
				t.Errorf("shared hit not attributed to its capturer: %+v", h)
			}
			return
		}
	}
	t.Fatal("the applied insight was not returned at all")
}

// TestInsightsProvider_SharedArmSkippedForOtherStatuses confirms the shared arm
// does not contradict an explicit status filter. It only ever returns applied
// insights, so running it for a "show me my pending captures" query would answer
// with records the caller excluded.
func TestInsightsProvider_SharedArmSkippedForOtherStatuses(t *testing.T) {
	s := &fakeInsightStore{}
	p := NewInsightsProvider(s)
	if _, err := p.Search(context.Background(), Query{
		Intent: "q", Status: knowledgekit.StatusPending, Caller: Caller{Email: "alice@example.com"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.gotSearches) != 1 {
		t.Fatalf("expected only the owner arm for an explicit non-applied status, got %+v", s.gotSearches)
	}
	if s.gotSearches[0].Shared {
		t.Errorf("owner arm ran with Shared set: %+v", s.gotSearches[0])
	}
}

// TestInsightsProvider_SharedArmRunsForAppliedStatus is the complement: an
// explicit applied filter is exactly what the shared arm serves, so it must run.
func TestInsightsProvider_SharedArmRunsForAppliedStatus(t *testing.T) {
	s := &fakeInsightStore{}
	p := NewInsightsProvider(s)
	if _, err := p.Search(context.Background(), Query{
		Intent: "q", Status: knowledgekit.StatusApplied, Caller: Caller{Email: "alice@example.com"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.gotSearches) != 2 || !s.gotSearches[1].Shared {
		t.Fatalf("expected an owner arm then a shared arm, got %+v", s.gotSearches)
	}
	if s.gotSearches[1].CapturedBy != "" {
		t.Errorf("shared arm carried an owner predicate: %+v", s.gotSearches[1])
	}
}

// TestInsightsProvider_OwnCopyWinsDeduplication checks the arm order: when the
// caller's own insight is itself applied, it is returned once, read under their
// own identity rather than as a shared record.
func TestInsightsProvider_OwnCopyWinsDeduplication(t *testing.T) {
	const urn = orgInsightsURN
	s := &scopingInsightStore{all: []knowledgekit.Insight{{
		ID: "alice-applied", CapturedBy: "alice@example.com", Status: knowledgekit.StatusApplied,
		InsightText: "amount is gross margin", EntityURNs: []string{urn},
	}}}
	hits, err := NewInsightsProvider(s).Search(context.Background(), Query{
		EntityURNs: []string{urn},
		Caller:     Caller{Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("own applied insight returned %d times, want 1: %+v", len(hits), hits)
	}
}

// TestInsightsProvider_FetchMatchesSearchReach is the #980 B2 read-side
// acceptance test: search hands out an mcp:insight:<id> reference for another
// person's applied insight, so fetch must dereference exactly that, and nothing
// short of applied.
func TestInsightsProvider_FetchMatchesSearchReach(t *testing.T) {
	const (
		urn   = orgInsightsURN
		alice = "alice@example.com"
	)
	s := &scopingInsightStore{all: orgInsights()}
	p := NewInsightsProvider(s)

	t.Run("another capturer's applied insight is fetchable", func(t *testing.T) {
		doc, owned, err := p.Fetch(context.Background(), knowledgepage.InsightRef("bob-applied"), Caller{Email: alice})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v, want the applied insight dereferenced", owned, err)
		}
		if doc.Body != "bob-applied text" {
			t.Errorf("unexpected document: %+v", doc)
		}
	})

	t.Run("another capturer's unapplied insight is not-found", func(t *testing.T) {
		for _, id := range []string{"bob-pending", "bob-approved", "bob-rejected", "bob-superseded"} {
			_, owned, err := p.Fetch(context.Background(), knowledgepage.InsightRef(id), Caller{Email: alice})
			if !owned || !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: owned=%v err=%v, want ErrNotFound", id, owned, err)
			}
		}
	})

	t.Run("an anonymous caller still gets nothing", func(t *testing.T) {
		_, owned, err := p.Fetch(context.Background(), knowledgepage.InsightRef("bob-applied"), Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for an anonymous caller", owned, err)
		}
	})
}

// TestInsightsProvider_AnonymousCallerReachesNothing pins the reason the
// provider stays ScopePerUser: the search toolkit builds an anonymous caller
// when a request carries no platform context, and applied insights are internal
// organization knowledge, not public-share content.
func TestInsightsProvider_AnonymousCallerReachesNothing(t *testing.T) {
	s := &scopingInsightStore{all: orgInsights()}
	p := NewInsightsProvider(s)
	if p.Scope() != ScopePerUser {
		t.Errorf("Scope() = %v, want ScopePerUser so the Router refuses anonymous callers", p.Scope())
	}
	hits, err := p.Search(context.Background(), Query{Intent: "orders", Caller: Caller{}})
	if err != nil || len(hits) != 0 {
		t.Errorf("hits=%+v err=%v, want nothing for an anonymous caller", hits, err)
	}
}

// TestInsightsProvider_MergedArmsRespectLimit checks the provider does not hand
// the Router more candidates than it asked for. Each arm queries the store with
// the same limit, so an unmerged concatenation would let insights contribute
// twice the per-source candidate pool and overstate its coverage count.
func TestInsightsProvider_MergedArmsRespectLimit(t *testing.T) {
	const urn = orgInsightsURN
	all := make([]knowledgekit.Insight, 0, 12)
	for i := range 6 {
		all = append(all,
			knowledgekit.Insight{
				ID: fmt.Sprintf("alice-%d", i), CapturedBy: "alice@example.com",
				Status: knowledgekit.StatusPending, EntityURNs: []string{urn},
			},
			knowledgekit.Insight{
				ID: fmt.Sprintf("bob-%d", i), CapturedBy: "bob@example.com",
				Status: knowledgekit.StatusApplied, EntityURNs: []string{urn},
			},
		)
	}
	s := &scopingInsightStore{all: all}
	hits, err := NewInsightsProvider(s).Search(context.Background(), Query{
		EntityURNs: []string{urn},
		Caller:     Caller{Email: "alice@example.com"},
		Limit:      6,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 6 {
		t.Fatalf("returned %d candidates for a limit of 6: %+v", len(hits), hits)
	}
	// The owner arm is kept first, so a trim drops the organization's copy.
	for _, h := range hits {
		if h.CapturedBy == "bob@example.com" {
			t.Errorf("shared hit displaced an owned one under a tight limit: %+v", hits)
			break
		}
	}
}
