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
