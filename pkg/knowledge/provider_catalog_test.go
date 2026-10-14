package knowledge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// a valid trino dataset URN and the dotted table name memory.ParseURNToTable
// derives from it.
const (
	testDatasetURN   = "mcp:dataset:(trino,acme,opensearch.default.os_acme_transactions)"
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
	tc, held := f.byTable[table.String()]
	if !held {
		// A table the catalog does not hold is semantic.ErrNotFound (#1610).
		return nil, fmt.Errorf("catalog holds no entity for %s: %w", table, semantic.ErrNotFound)
	}
	return tc, nil
}

func TestCatalogProvider_Metadata(t *testing.T) {
	p := NewCatalogProvider(&fakeTableSearcher{})
	if p.Name() != SourceCatalog {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestCatalogProvider_NoIntentNoEntitySkips(t *testing.T) {
	s := &fakeTableSearcher{}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil || s.searchCalled {
		t.Error("the catalog provider should do nothing with neither intent nor entity urns")
	}
}

func TestCatalogProvider_TextMapsAndRanks(t *testing.T) {
	s := &fakeTableSearcher{
		results: []semantic.TableSearchResult{
			{URN: "mcp:dataset:(trino,w,c.s.orders)", Name: "orders", Description: "order facts"},
			{URN: "mcp:dataset:(trino,w,c.s.returns)", Name: "returns"},
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
	if hits[0].Source != SourceCatalog || hits[0].Ref != "mcp:dataset:(trino,w,c.s.orders)" || hits[0].Text != "orders\norder facts" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	if len(hits[0].EntityURNs) != 1 || hits[0].EntityURNs[0] != "mcp:dataset:(trino,w,c.s.orders)" {
		t.Errorf("hit[0] entity urns = %+v", hits[0].EntityURNs)
	}
	if hits[1].Text != "returns" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "returns")
	}
}

func TestCatalogProvider_TextSearchError(t *testing.T) {
	p := NewCatalogProvider(&fakeTableSearcher{searchErr: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestCatalogProvider_EntityLookupReturnsCatalogEntity(t *testing.T) {
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

func TestCatalogProvider_EntityLookupNoDescriptionUsesName(t *testing.T) {
	// An entity with no description of its own falls back to the dotted table
	// name as its hit text.
	s := &fakeTableSearcher{
		byTable: map[string]*semantic.TableContext{
			testDatasetTable: {URN: testDatasetURN, Tags: []string{"pii"}},
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

// TestCatalogProvider_EntityLookupSkipsACatalogMiss is #1605 on the search
// side. An entity search on a URN the catalog has never ingested reported one
// match, and the reference it handed back then failed to fetch. The catalog
// reports that URN as not-found now (#1610), and this arm must skip it rather
// than fail the whole search: the entity set is probed across many URNs, most
// of which legitimately have no catalog entry.
func TestCatalogProvider_EntityLookupSkipsACatalogMiss(t *testing.T) {
	s := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{}}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("a URN the catalog has no entry for produced %d hit(s): %+v", len(hits), hits)
	}
}

// TestCatalogProvider_EntityLookupResolvesAnUndocumentedEntity is the other half
// of #1610: an entity the catalog holds but nobody has documented carries
// nothing beyond what its own URN supplies, and is a hit all the same. Until
// the catalog could report a miss itself, this arm had to reject that record to
// keep a made-up URN from matching.
func TestCatalogProvider_EntityLookupResolvesAnUndocumentedEntity(t *testing.T) {
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
	if len(hits) != 1 || hits[0].Ref != testDatasetURN {
		t.Fatalf("an undocumented entity the catalog holds must resolve, got %+v", hits)
	}
}

func TestCatalogProvider_EntityLookupMissReturnsNothing(t *testing.T) {
	// A valid URN the catalog answers with a read failure yields no hit, so a
	// URN it cannot resolve never produces a false match.
	s := &fakeTableSearcher{ctxErr: errors.New("catalog unreachable")}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits for an unresolved URN, got %+v", hits)
	}
}

func TestCatalogProvider_EntityLookupEmptyURNContextSkipped(t *testing.T) {
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

func TestCatalogProvider_EntityLookupSkipsUnparseableURN(t *testing.T) {
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

func TestCatalogProvider_EntityLookupToleratesCatalogError(t *testing.T) {
	// A catalog error on the entity path is skipped (the URN set is probed
	// across many lineage neighbors), not surfaced as a provider failure.
	s := &fakeTableSearcher{ctxErr: errors.New("catalog down")}
	p := NewCatalogProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{testDatasetURN}})
	if err != nil {
		t.Fatalf("entity-path catalog error must not fail the provider: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits, got %+v", hits)
	}
}

func TestCatalogProvider_EntityAndTextDedupByURN(t *testing.T) {
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
	mineURN     = "mcp:dataset:(trino,warehouse-a,db.public.orders)"
	theirsURN   = "mcp:dataset:(trino,warehouse-b,db.public.payroll)"
	unmappedURN = "mcp:dataset:(trino,mystery,db.public.notes)"
)

func TestCatalogProvider_TextArmHidesDatasetsOfDeniedConnections(t *testing.T) {
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

func TestCatalogProvider_EntityArmHidesDatasetsOfDeniedConnections(t *testing.T) {
	s := &fakeTableSearcher{byTable: map[string]*semantic.TableContext{
		"db.public.orders":  {URN: mineURN, Description: "orders"},
		"db.public.payroll": {URN: theirsURN, Description: "payroll"},
		"db.public.absent":  nil,
	}}
	p := NewCatalogProvider(s)
	caller, gate := scopedCaller(catalogScope())

	hits, err := p.Search(context.Background(), Query{
		EntityURNs: []string{mineURN, theirsURN, "mcp:dataset:(trino,warehouse-b,db.public.absent)"},
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

// craftedURN names a real table under a platform no connection claims. The
// catalog resolves a dataset from the TABLE identifier and re-derives the
// platform itself, so the boundary must judge the URN the catalog returned, not
// the one the caller wrote — otherwise this reference reads around it.
const craftedURN = "mcp:dataset:(trino,mystery,db.public.payroll)"

func TestCatalogProvider_CraftedURNCannotEvadeTheBoundary(t *testing.T) {
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
}

type fakeAvailability struct {
	answers map[string]*query.TableAvailability
	err     error
	got     []string
}

func (f *fakeAvailability) GetTableAvailability(_ context.Context, urn string) (*query.TableAvailability, error) {
	f.got = append(f.got, urn)
	return f.answers[urn], f.err
}
