package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/query"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

const (
	verifiableOrdersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"
	verifiableSalesURN  = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"
	verifiableGoneURN   = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.gone,PROD)"
)

// fakeVerifier resolves a fixed set of URNs and counts the passes it was asked
// to make, so a test can prove a page of hits costs one resolution rather than
// one per hit.
type fakeVerifier struct {
	byURN  map[string]query.Verifiable
	passes int
	gotAll [][]string
}

func (f *fakeVerifier) Verifiables(_ context.Context, urns []string) map[string]query.Verifiable {
	f.passes++
	f.gotAll = append(f.gotAll, urns)
	out := make(map[string]query.Verifiable)
	for _, urn := range urns {
		if v, ok := f.byURN[urn]; ok {
			out[urn] = v
		}
	}
	return out
}

func ordersVerifier() *fakeVerifier {
	return &fakeVerifier{byURN: map[string]query.Verifiable{
		verifiableOrdersURN: {
			URN:        verifiableOrdersURN,
			QueryTable: "iceberg.retail.orders",
			Connection: "primary",
		},
	}}
}

// A search that returns several insights about the same entity resolves that
// entity once for the whole page, and marks every hit whose entity resolved.
func TestInsightsProvider_MarksHitsInOnePass(t *testing.T) {
	s := &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
		{Insight: knowledgekit.Insight{
			ID: "a", Status: knowledgekit.StatusApplied, EntityURNs: []string{verifiableOrdersURN},
		}, Score: 0.9},
		{Insight: knowledgekit.Insight{
			ID: "b", Status: knowledgekit.StatusApplied, EntityURNs: []string{verifiableOrdersURN},
		}, Score: 0.8},
		{Insight: knowledgekit.Insight{
			ID: "c", Status: knowledgekit.StatusApplied, EntityURNs: []string{verifiableGoneURN},
		}, Score: 0.7},
		{Insight: knowledgekit.Insight{ID: "d", Status: knowledgekit.StatusApplied}, Score: 0.6},
	}}
	p := NewInsightsProvider(s)
	v := ordersVerifier()
	p.SetVerifier(v)

	hits, err := p.Search(context.Background(), Query{
		Intent: "orders", Caller: Caller{Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if v.passes != 1 {
		t.Errorf("resolution passes = %d, want 1 for the whole page", v.passes)
	}
	if len(v.gotAll) == 1 && len(v.gotAll[0]) != 2 {
		t.Errorf("resolved URNs = %v, want the two distinct entities the page names", v.gotAll[0])
	}

	marked := map[string]bool{}
	for _, h := range hits {
		marked[h.Ref] = h.Verifiable != nil
	}
	for ref, want := range map[string]bool{"a": true, "b": true, "c": false, "d": false} {
		if marked[ref] != want {
			t.Errorf("hit %s marked = %v, want %v", ref, marked[ref], want)
		}
	}
}

// A record linked to several entities names the first one that resolves, in the
// order the record carries them — not whichever the resolver happened to return.
func TestInsightsProvider_MarksFirstResolvingEntity(t *testing.T) {
	s := &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
		{Insight: knowledgekit.Insight{
			ID:         "multi",
			Status:     knowledgekit.StatusApplied,
			EntityURNs: []string{verifiableGoneURN, verifiableOrdersURN, verifiableSalesURN},
		}, Score: 0.9},
	}}
	p := NewInsightsProvider(s)
	v := ordersVerifier()
	v.byURN[verifiableSalesURN] = query.Verifiable{
		URN: verifiableSalesURN, QueryTable: "iceberg.retail.daily_sales",
	}
	p.SetVerifier(v)

	hits, err := p.Search(context.Background(), Query{
		Intent: "orders", Caller: Caller{Email: "alice@example.com"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Verifiable == nil {
		t.Fatalf("hits = %+v, want one marked hit", hits)
	}
	if hits[0].Verifiable.URN != verifiableOrdersURN {
		t.Errorf("marked URN = %q, want the first entity that resolves (%q)",
			hits[0].Verifiable.URN, verifiableOrdersURN)
	}
}

// With no verifier, or with one that resolves nothing, the hits are untouched.
func TestInsightsProvider_UnmarkedWithoutResolution(t *testing.T) {
	tests := []struct {
		name     string
		verifier EntityVerifier
	}{
		{name: "no verifier wired"},
		{name: "nothing resolves", verifier: &fakeVerifier{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
				{Insight: knowledgekit.Insight{
					ID: "a", Status: knowledgekit.StatusApplied, EntityURNs: []string{verifiableOrdersURN},
				}, Score: 0.9},
			}}
			p := NewInsightsProvider(s)
			p.SetVerifier(tt.verifier)

			hits, err := p.Search(context.Background(), Query{
				Intent: "orders", Caller: Caller{Email: "alice@example.com"},
			})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(hits) != 1 {
				t.Fatalf("hits = %+v, want one", hits)
			}
			if hits[0].Verifiable != nil {
				t.Errorf("hit carries a marker with nothing resolved: %+v", hits[0].Verifiable)
			}
		})
	}
}

// A page with nothing to resolve against must not spend a lookup pass: no hits
// at all, or hits linked to no catalog entity.
func TestInsightsProvider_NothingToResolveSpendsNoPass(t *testing.T) {
	tests := []struct {
		name     string
		store    *fakeInsightStore
		wantHits int
	}{
		{name: "no hits at all", store: &fakeInsightStore{}},
		{
			name: "hits linked to no entity",
			store: &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
				{Insight: knowledgekit.Insight{ID: "a", Status: knowledgekit.StatusApplied}, Score: 0.9},
			}},
			wantHits: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewInsightsProvider(tt.store)
			v := ordersVerifier()
			p.SetVerifier(v)

			hits, err := p.Search(context.Background(), Query{
				Intent: "orders", Caller: Caller{Email: "alice@example.com"},
			})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(hits) != tt.wantHits {
				t.Fatalf("hits = %+v, want %d", hits, tt.wantHits)
			}
			if v.passes != 0 {
				t.Errorf("resolution passes = %d, want 0", v.passes)
			}
		})
	}
}

// The fetched record carries the same marker its search hit did, and an insight
// linked to no entity resolves nothing.
func TestInsightsProvider_FetchMarksTheRecord(t *testing.T) {
	tests := []struct {
		name       string
		entityURNs []string
		wantMarked bool
		wantPasses int
	}{
		{
			name:       "resolvable entity",
			entityURNs: []string{verifiableOrdersURN},
			wantMarked: true,
			wantPasses: 1,
		},
		{
			name:       "unresolvable entity",
			entityURNs: []string{verifiableGoneURN},
			wantPasses: 1,
		},
		{
			name: "no linked entity spends no pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeInsightStore{getInsight: &knowledgekit.Insight{
				ID:          "i-1",
				CapturedBy:  "alice@example.com",
				Status:      knowledgekit.StatusApplied,
				InsightText: "The orders table holds 1140 rows.",
				EntityURNs:  tt.entityURNs,
			}}
			p := NewInsightsProvider(s)
			v := ordersVerifier()
			p.SetVerifier(v)

			doc, owned, err := p.Fetch(context.Background(), "mcp:insight:i-1",
				Caller{Email: "alice@example.com"})
			if err != nil || !owned || doc == nil {
				t.Fatalf("fetch = (%+v, %v, %v)", doc, owned, err)
			}
			if got := doc.Verifiable != nil; got != tt.wantMarked {
				t.Errorf("document marked = %v, want %v", got, tt.wantMarked)
			}
			if v.passes != tt.wantPasses {
				t.Errorf("resolution passes = %d, want %d", v.passes, tt.wantPasses)
			}
		})
	}
}

// boundedScope denies one connection and attributes every URN containing its
// name to it, standing in for the persona connection boundary the Router
// attaches to a caller.
type boundedScope struct {
	denied string
}

func (b boundedScope) AllowConnection(_, connection string) bool { return connection != b.denied }

func (b boundedScope) ConnectionsForURN(urn string) []string {
	if strings.Contains(urn, b.denied) {
		return []string{b.denied}
	}
	return []string{"primary"}
}

// An insight is not connection-scoped — reading a colleague's conclusion about a
// warehouse you cannot query is the point of shared knowledge — but the marker
// is topology: it names a connection and asserts the entity is reachable there,
// which is exactly what the persona connection boundary withholds (#1108). The
// hit must still be delivered; only its marker is withheld, and no lookup is
// issued against the denied connection.
func TestInsightsProvider_MarkerHonorsTheConnectionBoundary(t *testing.T) {
	const financeURN = "urn:li:dataset:(urn:li:dataPlatform:trino,finance.gl.ledger,PROD)"

	tests := []struct {
		name       string
		urn        string
		wantMarked bool
		wantAsked  int
	}{
		{name: "reachable connection", urn: verifiableOrdersURN, wantMarked: true, wantAsked: 1},
		{name: "denied connection", urn: financeURN},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeInsightStore{scored: []knowledgekit.ScoredInsight{
				{Insight: knowledgekit.Insight{
					ID: "a", Status: knowledgekit.StatusApplied, EntityURNs: []string{tt.urn},
				}, Score: 0.9},
			}}
			p := NewInsightsProvider(s)
			v := ordersVerifier()
			v.byURN[financeURN] = query.Verifiable{
				URN: financeURN, QueryTable: "finance.gl.ledger", Connection: "finance",
			}
			p.SetVerifier(v)

			caller := Caller{Email: "alice@example.com", Persona: "analyst"}
			caller.conn = &connGate{scope: boundedScope{denied: "finance"}}

			hits, err := p.Search(context.Background(), Query{Intent: "ledger", Caller: caller})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(hits) != 1 {
				t.Fatalf("hits = %+v, want the insight itself delivered either way", hits)
			}
			if got := hits[0].Verifiable != nil; got != tt.wantMarked {
				t.Errorf("hit marked = %v, want %v", got, tt.wantMarked)
			}
			asked := 0
			for _, set := range v.gotAll {
				asked += len(set)
			}
			if asked != tt.wantAsked {
				t.Errorf("entities looked up = %d, want %d (a denied connection is never probed)", asked, tt.wantAsked)
			}
		})
	}
}

// Fetch must not disclose topology a search would have withheld.
func TestInsightsProvider_FetchMarkerHonorsTheConnectionBoundary(t *testing.T) {
	const financeURN = "urn:li:dataset:(urn:li:dataPlatform:trino,finance.gl.ledger,PROD)"

	s := &fakeInsightStore{getInsight: &knowledgekit.Insight{
		ID:         "i-1",
		CapturedBy: "alice@example.com",
		Status:     knowledgekit.StatusApplied,
		EntityURNs: []string{financeURN},
	}}
	p := NewInsightsProvider(s)
	v := ordersVerifier()
	v.byURN[financeURN] = query.Verifiable{
		URN: financeURN, QueryTable: "finance.gl.ledger", Connection: "finance",
	}
	p.SetVerifier(v)

	caller := Caller{Email: "alice@example.com", Persona: "analyst"}
	caller.conn = &connGate{scope: boundedScope{denied: "finance"}}

	doc, _, err := p.Fetch(context.Background(), "mcp:insight:i-1", caller)
	if err != nil || doc == nil {
		t.Fatalf("fetch = (%+v, %v)", doc, err)
	}
	if doc.Verifiable != nil {
		t.Errorf("fetched record names a connection the persona may not reach: %+v", doc.Verifiable)
	}
	if v.passes != 0 {
		t.Errorf("resolution passes = %d, want 0: a denied entity is never probed", v.passes)
	}
}

func TestInsightsProvider_FetchUnmarkedWithoutVerifier(t *testing.T) {
	s := &fakeInsightStore{getInsight: &knowledgekit.Insight{
		ID:         "i-1",
		CapturedBy: "alice@example.com",
		Status:     knowledgekit.StatusApplied,
		EntityURNs: []string{verifiableOrdersURN},
	}}
	doc, _, err := NewInsightsProvider(s).Fetch(context.Background(), "mcp:insight:i-1",
		Caller{Email: "alice@example.com"})
	if err != nil || doc == nil {
		t.Fatalf("fetch = (%+v, %v)", doc, err)
	}
	if doc.Verifiable != nil {
		t.Errorf("fetched record carries a marker with no verifier wired: %+v", doc.Verifiable)
	}
}
