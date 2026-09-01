package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// fakeCatalogIndex is a CatalogIndexSearcher stub recording the query it was
// asked for.
type fakeCatalogIndex struct {
	hits   []CatalogIndexHit
	err    error
	got    CatalogIndexQuery
	called bool
}

func (f *fakeCatalogIndex) SearchCatalogIndex(_ context.Context, q CatalogIndexQuery) ([]CatalogIndexHit, error) {
	f.called = true
	f.got = q
	return f.hits, f.err
}

// TestCatalogIndexArmSurfacesDescriptions is the defect this arm closes: a fact
// applied to a dataset description is reachable from a topical query even when
// DataHub's own keyword search returns nothing for it.
func TestCatalogIndexArmSurfacesDescriptions(t *testing.T) {
	s := &fakeTableSearcher{} // DataHub finds nothing for this wording
	idx := &fakeCatalogIndex{hits: []CatalogIndexHit{{
		URN:         testDatasetURN,
		Name:        "sales.orders",
		Description: "Refunds are subtracted before revenue is recognized.",
		Score:       0.91,
	}}}
	p := NewCatalogProvider(s)
	p.SetIndexSearcher(idx)

	hits, err := p.Search(context.Background(), Query{Intent: "how do we treat refunds", Limit: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len = %d, want 1", len(hits))
	}
	if hits[0].Source != SourceCatalog || hits[0].Ref != testDatasetURN {
		t.Errorf("hit = %+v", hits[0])
	}
	if hits[0].Reference != testDatasetURN {
		t.Errorf("Reference = %q, want the dataset URN so fetch dereferences it", hits[0].Reference)
	}
	if hits[0].Text != "sales.orders\nRefunds are subtracted before revenue is recognized." {
		t.Errorf("Text = %q, want the name and description", hits[0].Text)
	}
	if !idx.called || idx.got.QueryText != "how do we treat refunds" || idx.got.Limit != 7 {
		t.Errorf("index query not forwarded: %+v", idx.got)
	}
}

func TestCatalogIndexArmForwardsEmbedding(t *testing.T) {
	idx := &fakeCatalogIndex{}
	p := NewCatalogProvider(&fakeTableSearcher{})
	p.SetIndexSearcher(idx)

	embedding := []float32{0.1, 0.2}
	if _, err := p.Search(context.Background(), Query{Intent: "refunds", Embedding: embedding}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(idx.got.Embedding) != len(embedding) {
		t.Errorf("Embedding not forwarded: %+v", idx.got.Embedding)
	}
}

// TestCatalogIndexArmLeadsAndDedups pins the merge policy: a dataset both
// sources return is emitted once, at the index's rank.
func TestCatalogIndexArmLeadsAndDedups(t *testing.T) {
	s := &fakeTableSearcher{results: []semantic.TableSearchResult{
		{URN: "urn:li:dataset:keyword-only", Name: "keyword_only"},
		{URN: "urn:li:dataset:both", Name: "both", Description: "from datahub"},
	}}
	idx := &fakeCatalogIndex{hits: []CatalogIndexHit{
		{URN: "urn:li:dataset:both", Name: "both", Description: "from the index"},
	}}
	p := NewCatalogProvider(s)
	p.SetIndexSearcher(idx)

	hits, err := p.Search(context.Background(), Query{Intent: "anything"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2 (the duplicate collapsed)", len(hits))
	}
	if hits[0].Ref != "urn:li:dataset:both" {
		t.Errorf("index hit should lead, got %q", hits[0].Ref)
	}
	if hits[0].Text != "both\nfrom the index" {
		t.Errorf("the surviving copy should be the index's, got %q", hits[0].Text)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("expected descending positional score across the merged order, got %v then %v",
			hits[0].Score, hits[1].Score)
	}
}

// TestCatalogIndexFailureDegrades: the index is an accelerator over a corpus
// DataHub still serves, so losing it costs recall, not the catalog source.
func TestCatalogIndexFailureDegrades(t *testing.T) {
	s := &fakeTableSearcher{results: []semantic.TableSearchResult{
		{URN: "urn:li:dataset:orders", Name: "orders"},
	}}
	idx := &fakeCatalogIndex{err: errors.New("index down")}
	p := NewCatalogProvider(s)
	p.SetIndexSearcher(idx)

	hits, err := p.Search(context.Background(), Query{Intent: "orders"})
	if err != nil {
		t.Fatalf("an index failure must not blank the catalog source: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref != "urn:li:dataset:orders" {
		t.Fatalf("hits = %+v", hits)
	}
}

// TestCatalogTextFailureStillBlanks: the catalog itself erroring means the
// source is unhealthy, which the router must see.
func TestCatalogTextFailureStillBlanks(t *testing.T) {
	s := &fakeTableSearcher{searchErr: errors.New("datahub down")}
	idx := &fakeCatalogIndex{hits: []CatalogIndexHit{{URN: "urn:li:dataset:orders", Name: "orders"}}}
	p := NewCatalogProvider(s)
	p.SetIndexSearcher(idx)

	if _, err := p.Search(context.Background(), Query{Intent: "orders"}); err == nil {
		t.Fatal("expected the catalog error to surface")
	}
}

func TestCatalogIndexSkipsEmptyURNs(t *testing.T) {
	idx := &fakeCatalogIndex{hits: []CatalogIndexHit{{URN: "", Name: "nameless"}}}
	p := NewCatalogProvider(&fakeTableSearcher{})
	p.SetIndexSearcher(idx)

	hits, err := p.Search(context.Background(), Query{Intent: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("a hit with no URN can be neither cited nor fetched: %+v", hits)
	}
}

// TestCatalogIndexRespectsConnectionBoundary: the index is a copy of catalog
// text, so it must not become a way around the persona connection boundary
// (#1108) that the DataHub arm already enforces.
func TestCatalogIndexRespectsConnectionBoundary(t *testing.T) {
	idx := &fakeCatalogIndex{hits: []CatalogIndexHit{{URN: testDatasetURN, Name: "orders"}}}
	p := NewCatalogProvider(&fakeTableSearcher{})
	p.SetIndexSearcher(idx)

	q := Query{Intent: "orders"}
	gate := &connGate{scope: denyAllScope{}}
	q.Caller.conn = gate

	hits, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("index hits must pass the same connection boundary: %+v", hits)
	}
	if gate.withheld != 1 {
		t.Errorf("withheld = %d, want 1 so the caller is told something exists", gate.withheld)
	}
}

// denyAllScope is a ConnectionScope that grants nothing.
type denyAllScope struct{}

func (denyAllScope) ConnectionsForURN(string) []string   { return []string{"restricted"} }
func (denyAllScope) AllowConnection(string, string) bool { return false }

// TestCatalogCandidateMergeInterleaves pins the budget policy: neither arm can
// crowd the other out of the per-source candidate budget, the index still
// leads, and the merged set never exceeds the budget the coverage contract
// promises.
func TestCatalogCandidateMergeInterleaves(t *testing.T) {
	indexed := []catalogCandidate{{urn: "i1"}, {urn: "i2"}, {urn: "i3"}}
	remote := []catalogCandidate{{urn: "r1"}, {urn: "r2"}}

	got := mergeCandidates(0, nil, indexed, remote)
	want := []string{"i1", "r1", "i2", "r2", "i3"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i].urn != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i].urn, want[i])
		}
	}

	// A duplicate keeps the index's copy and does not consume a second slot.
	dup := mergeCandidates(0, nil, []catalogCandidate{{urn: "x", text: "from index"}}, []catalogCandidate{{urn: "x", text: "from datahub"}})
	if len(dup) != 1 || dup[0].text != "from index" {
		t.Errorf("dedup = %+v, want the index copy only", dup)
	}

	// The budget is a hard cap on the merged set.
	capped := mergeCandidates(3, nil, indexed, remote)
	if len(capped) != 3 || capped[0].urn != "i1" || capped[1].urn != "r1" {
		t.Errorf("capped = %+v, want the first three of the interleaved order", capped)
	}

	// One-sided inputs degrade to that side's own order.
	if only := mergeCandidates(0, nil, nil, remote); len(only) != 2 || only[0].urn != "r1" {
		t.Errorf("index-less merge = %+v, want DataHub's order unchanged", only)
	}
	if only := mergeCandidates(0, nil, indexed, nil); len(only) != 3 || only[0].urn != "i1" {
		t.Errorf("datahub-less merge = %+v, want the index order unchanged", only)
	}
}
