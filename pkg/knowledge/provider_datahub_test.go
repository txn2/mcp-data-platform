package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// a valid trino dataset URN and the dotted table name memory.ParseURNToTable
// derives from it.
const (
	testDatasetURN   = "urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.os_acme_transactions,PROD)"
	testDatasetTable = "opensearch.default.os_acme_transactions"
)

// fakeTableSearcher is a tableSearcher stub recording the text-search filter and
// the entity table identifiers it was asked for.
type fakeTableSearcher struct {
	// text path
	results      []semantic.TableSearchResult
	searchErr    error
	got          semantic.SearchFilter
	searchCalled bool

	// entity path
	byTable   map[string]*semantic.TableContext // table.String() -> context
	ctxErr    error
	gotTables []semantic.TableIdentifier
}

func (f *fakeTableSearcher) SearchTables(_ context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	f.searchCalled = true
	f.got = filter
	return f.results, f.searchErr
}

func (f *fakeTableSearcher) GetTableContext(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	f.gotTables = append(f.gotTables, table)
	if f.ctxErr != nil {
		return nil, f.ctxErr
	}
	return f.byTable[table.String()], nil
}

func TestDatahubProvider_Metadata(t *testing.T) {
	p := NewCatalogProvider(&fakeTableSearcher{})
	if p.Name() != SourceCatalog {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestDatahubProvider_NoIntentNoEntitySkips(t *testing.T) {
	s := &fakeTableSearcher{}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil || s.searchCalled {
		t.Error("datahub provider should do nothing with neither intent nor entity urns")
	}
}

func TestDatahubProvider_TextMapsAndRanks(t *testing.T) {
	s := &fakeTableSearcher{
		results: []semantic.TableSearchResult{
			{URN: "urn:li:dataset:orders", Name: "orders", Description: "order facts"},
			{URN: "urn:li:dataset:returns", Name: "returns"},
		},
	}
	p := NewCatalogProvider(s)
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
	if hits[0].Score <= hits[1].Score {
		t.Errorf("expected descending positional score, got %v then %v", hits[0].Score, hits[1].Score)
	}
	if hits[0].Source != SourceCatalog || hits[0].Ref != "urn:li:dataset:orders" || hits[0].Text != "orders\norder facts" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	if len(hits[0].EntityURNs) != 1 || hits[0].EntityURNs[0] != "urn:li:dataset:orders" {
		t.Errorf("hit[0] entity urns = %+v", hits[0].EntityURNs)
	}
	if hits[1].Text != "returns" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "returns")
	}
}

func TestDatahubProvider_TextSearchError(t *testing.T) {
	p := NewCatalogProvider(&fakeTableSearcher{searchErr: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestDatahubProvider_EntityLookupReturnsCatalogEntity(t *testing.T) {
	s := &fakeTableSearcher{
		byTable: map[string]*semantic.TableContext{
			testDatasetTable: {URN: testDatasetURN, Description: "acme transactions"},
		},
	}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.searchCalled {
		t.Error("text search must not run for an entity-only query")
	}
	if len(s.gotTables) != 1 || s.gotTables[0].String() != testDatasetTable {
		t.Fatalf("entity lookup parsed wrong table: %+v", s.gotTables)
	}
	if len(hits) != 1 || hits[0].Source != SourceCatalog || hits[0].Ref != testDatasetURN || hits[0].Score != entityMatchScore {
		t.Fatalf("unexpected entity hit: %+v", hits)
	}
	if hits[0].Text != testDatasetTable+"\nacme transactions" {
		t.Errorf("hit text = %q", hits[0].Text)
	}
	if len(hits[0].EntityURNs) != 1 || hits[0].EntityURNs[0] != testDatasetURN {
		t.Errorf("hit entity urns = %+v", hits[0].EntityURNs)
	}
}

func TestDatahubProvider_EntityLookupNoDescriptionUsesName(t *testing.T) {
	s := &fakeTableSearcher{
		byTable: map[string]*semantic.TableContext{
			testDatasetTable: {URN: testDatasetURN},
		},
	}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 || hits[0].Text != testDatasetTable {
		t.Errorf("expected the dotted table name as text, got %+v", hits)
	}
}

func TestDatahubProvider_EntityLookupMissReturnsNothing(t *testing.T) {
	// A valid URN the catalog cannot resolve (nil context) yields no hit, so a
	// non-existent URN never produces a false match.
	s := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{}}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for an unresolved URN, got %+v", hits)
	}
}

func TestDatahubProvider_EntityLookupEmptyURNContextSkipped(t *testing.T) {
	// A context with no URN (phantom entity) is not a real match.
	s := &fakeTableSearcher{
		byTable: map[string]*semantic.TableContext{testDatasetTable: {Description: "ghost"}},
	}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for an empty-URN context, got %+v", hits)
	}
}

func TestDatahubProvider_EntityLookupSkipsUnparseableURN(t *testing.T) {
	s := &fakeTableSearcher{}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:not-a-dataset"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 || len(s.gotTables) != 0 {
		t.Errorf("an unparseable URN must be skipped without a catalog call: hits=%+v tables=%+v", hits, s.gotTables)
	}
}

func TestDatahubProvider_EntityLookupToleratesCatalogError(t *testing.T) {
	// A catalog error on the entity path is skipped (the URN set is probed
	// across many lineage neighbors), not surfaced as a provider failure.
	s := &fakeTableSearcher{ctxErr: errors.New("datahub down")}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("entity-path catalog error must not fail the provider: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits, got %+v", hits)
	}
}

func TestDatahubProvider_EntityAndTextDedupByURN(t *testing.T) {
	s := &fakeTableSearcher{
		byTable: map[string]*semantic.TableContext{
			testDatasetTable: {URN: testDatasetURN, Description: "acme transactions"},
		},
		results: []semantic.TableSearchResult{
			{URN: testDatasetURN, Name: "os_acme_transactions"},
		},
	}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{Intent: "transactions", EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected the URN de-duplicated to a single hit, got %d: %+v", len(hits), hits)
	}
	// The entity path won; the text path skipped the already-seen URN.
	if hits[0].Score != entityMatchScore {
		t.Errorf("expected entity-path hit to win, got score %v", hits[0].Score)
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

func TestCatalogProvider_Fetch(t *testing.T) {
	t.Run("returns catalog context for a dataset reference", func(t *testing.T) {
		f := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{
			testDatasetTable: {URN: testDatasetURN, Description: "ACME transactions"},
		}}
		doc, owned, err := NewCatalogProvider(f).Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v, want owned, no error", owned, err)
		}
		if doc.Source != SourceCatalog || doc.Reference != testDatasetURN {
			t.Errorf("doc = %+v", doc)
		}
		tc, ok := doc.Content.(*semantic.TableContext)
		if !ok || tc.Description != "ACME transactions" {
			t.Errorf("Content = %+v, want the TableContext", doc.Content)
		}
		if len(doc.EntityURNs) != 1 || doc.EntityURNs[0] != testDatasetURN {
			t.Errorf("EntityURNs = %+v", doc.EntityURNs)
		}
	})

	t.Run("declines a non-dataset reference", func(t *testing.T) {
		f := &fakeTableSearcher{}
		_, owned, err := NewCatalogProvider(f).Fetch(context.Background(), "urn:li:document:d", Caller{})
		if owned || err != nil {
			t.Errorf("owned=%v err=%v, want declined", owned, err)
		}
		if len(f.gotTables) != 0 {
			t.Errorf("GetTableContext must not be called for a document reference")
		}
	})

	t.Run("unknown dataset is not-found", func(t *testing.T) {
		f := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{}} // empty -> nil context
		_, owned, err := NewCatalogProvider(f).Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("unparseable dataset urn is not-found", func(t *testing.T) {
		f := &fakeTableSearcher{}
		_, owned, err := NewCatalogProvider(f).Fetch(context.Background(), "urn:li:dataset:garbage", Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("a catalog lookup error is not-found, matching the search entity path's skip", func(t *testing.T) {
		// DataHub reports a missing/deleted entity as an error; the search entity path
		// skips it, so fetch reports a clean not-found rather than a hard failure.
		f := &fakeTableSearcher{ctxErr: errors.New("GetEntity: entity not found")}
		_, owned, err := NewCatalogProvider(f).Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})
}

// catalogScope maps the two test dataset URNs onto distinct connections and
// grants only the first, so a caller sees one dataset, is told about the other,
// and keeps the one no connection claims.
func catalogScope() stubScope {
	return stubScope{
		allowed: map[string]bool{"warehouse-a": true},
		urns: map[string][]string{
			mineURN:   {"warehouse-a"},
			theirsURN: {"warehouse-b"},
		},
	}
}

const (
	mineURN     = "urn:li:dataset:(urn:li:dataPlatform:trino-a,db.public.orders,PROD)"
	theirsURN   = "urn:li:dataset:(urn:li:dataPlatform:trino-b,db.public.payroll,PROD)"
	unmappedURN = "urn:li:dataset:(urn:li:dataPlatform:mystery,db.public.notes,PROD)"
)

func TestDatahubProvider_TextArmHidesDatasetsOfDeniedConnections(t *testing.T) {
	s := &fakeTableSearcher{results: []semantic.TableSearchResult{
		{URN: mineURN, Name: "db.public.orders"},
		{URN: theirsURN, Name: "db.public.payroll"},
		{URN: unmappedURN, Name: "db.public.notes"},
	}}
	p := NewCatalogProvider(s)
	caller, gate := scopedCaller(catalogScope())

	hits, err := p.Search(context.Background(), Query{Intent: "records", Limit: 10, Caller: caller})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	refs := make([]string, len(hits))
	for i, h := range hits {
		refs[i] = h.Ref
	}
	if len(hits) != 2 || refs[0] != mineURN || refs[1] != unmappedURN {
		t.Fatalf("want the permitted and the unmappable dataset, got %v", refs)
	}
	if gate.withheld != 1 {
		t.Errorf("withheld = %d, want 1", gate.withheld)
	}
}

func TestDatahubProvider_EntityArmHidesDatasetsOfDeniedConnections(t *testing.T) {
	s := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{
		"db.public.orders":  {URN: mineURN, Description: "orders"},
		"db.public.payroll": {URN: theirsURN, Description: "payroll"},
		"db.public.absent":  nil,
	}}
	p := NewCatalogProvider(s)
	caller, gate := scopedCaller(catalogScope())

	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{mineURN, theirsURN, "urn:li:dataset:(urn:li:dataPlatform:trino-b,db.public.absent,PROD)"},
		Caller:     caller,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref != mineURN {
		t.Fatalf("want only the permitted dataset, got %+v", hits)
	}
	// The denied URN resolved to a real entity and is counted; the one that
	// resolves to nothing is not, so the count means "exists but hidden".
	if gate.withheld != 1 {
		t.Errorf("withheld = %d, want 1", gate.withheld)
	}
}

func TestDatahubProvider_FetchDeniedDatasetIsNotFound(t *testing.T) {
	s := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{
		"db.public.payroll": {URN: theirsURN, Description: "payroll"},
		"db.public.orders":  {URN: mineURN, Description: "orders"},
	}}
	p := NewCatalogProvider(s)
	caller, _ := scopedCaller(catalogScope())

	doc, owned, err := p.Fetch(context.Background(), theirsURN, caller)
	if !owned {
		t.Fatal("a dataset URN is owned by the catalog provider even when denied")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if doc != nil {
		t.Errorf("denied fetch must return no document, got %+v", doc)
	}

	// The permitted dataset still resolves through the same path.
	doc, owned, err = p.Fetch(context.Background(), mineURN, caller)
	if !owned || err != nil || doc == nil {
		t.Fatalf("permitted fetch: doc=%+v owned=%v err=%v", doc, owned, err)
	}
}

// craftedURN names a real table under a platform no connection claims. The
// catalog resolves a dataset from the TABLE identifier and re-derives the
// platform itself, so the boundary must judge the URN the catalog returned, not
// the one the caller wrote — otherwise this reference reads around it.
const craftedURN = "urn:li:dataset:(urn:li:dataPlatform:mystery,db.public.payroll,PROD)"

func TestDatahubProvider_CraftedURNCannotEvadeTheBoundary(t *testing.T) {
	newSearcher := func() *fakeTableSearcher {
		return &fakeTableSearcher{byTable: map[string]*semantic.TableContext{
			"db.public.payroll": {URN: theirsURN, Description: "payroll"},
		}}
	}
	caller, gate := scopedCaller(catalogScope())

	hits, err := NewCatalogProvider(newSearcher()).Search(context.Background(), Query{
		EntityURNs: []string{craftedURN},
		Caller:     caller,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("a crafted platform must not surface a denied dataset, got %+v", hits)
	}
	if gate.withheld != 1 {
		t.Errorf("withheld = %d, want 1", gate.withheld)
	}

	doc, owned, err := NewCatalogProvider(newSearcher()).Fetch(context.Background(), craftedURN, caller)
	if !owned || !errors.Is(err, ErrNotFound) || doc != nil {
		t.Fatalf("crafted fetch: doc=%+v owned=%v err=%v, want owned + ErrNotFound", doc, owned, err)
	}
}
