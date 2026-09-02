package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/query"
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
	// The entity is documented by a tag rather than a description, so it is a
	// real entry (#1605) whose hit text falls back to the dotted table name.
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

// TestDatahubProvider_EntityLookupRejectsTheURNOnlyStub is #1605 on the search
// side. A catalog answers a URN it has never ingested with a context carrying
// that URN and nothing else, so an entity search on a made-up URN reported one
// match and the reference it handed back then failed to fetch.
func TestDatahubProvider_EntityLookupRejectsTheURNOnlyStub(t *testing.T) {
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
	if len(hits) != 0 {
		t.Errorf("a URN the catalog has no entry for produced %d hit(s): %+v", len(hits), hits)
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
		ds, ok := doc.Content.(CatalogDataset)
		if !ok || ds.Description != "ACME transactions" {
			t.Errorf("Content = %+v, want the CatalogDataset built from the TableContext", doc.Content)
		}
		if ds.Schema != nil || ds.QueryAvailability != nil || doc.Verifiable != nil {
			t.Errorf("without a dataset reader or availability resolver the record is the context alone: %+v", ds)
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

// fakeDatasetReader is the full-record read a real catalog offers (#1590).
type fakeDatasetReader struct {
	ds  *semantic.Dataset
	err error
	got []semantic.TableIdentifier
}

func (f *fakeDatasetReader) GetDataset(_ context.Context, table semantic.TableIdentifier) (*semantic.Dataset, error) {
	f.got = append(f.got, table)
	return f.ds, f.err
}

type fakeProductReader struct {
	product *semantic.DataProduct
	err     error
	got     []string
}

func (f *fakeProductReader) GetDataProduct(_ context.Context, urn string) (*semantic.DataProduct, error) {
	f.got = append(f.got, urn)
	return f.product, f.err
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

func TestCatalogProvider_FetchDatasetRecord(t *testing.T) {
	record := &semantic.Dataset{
		TableContext: semantic.TableContext{URN: testDatasetURN, Description: "ACME transactions"},
		Name:         "transactions",
		Schema: &semantic.DatasetSchema{Fields: []semantic.SchemaField{
			{FieldPath: "id", Type: "NUMBER"}, {FieldPath: "amount", Type: "NUMBER", Nullable: true},
		}},
		Queries: []semantic.SavedQuery{{Statement: "SELECT count(*) FROM transactions"}},
	}

	t.Run("a wired dataset reader supplies the full record, the searcher is not consulted", func(t *testing.T) {
		searcher := &fakeTableSearcher{}
		reader := &fakeDatasetReader{ds: record}
		p := NewCatalogProvider(searcher)
		p.SetDatasetReader(reader)
		doc, owned, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v", owned, err)
		}
		ds, ok := doc.Content.(CatalogDataset)
		if !ok {
			t.Fatalf("Content = %T, want CatalogDataset", doc.Content)
		}
		if ds.Name != "transactions" || ds.Description != "ACME transactions" {
			t.Errorf("record = %+v", ds.Dataset)
		}
		if ds.Schema == nil || len(ds.Schema.Fields) != 2 || len(ds.Queries) != 1 {
			t.Errorf("schema and queries must ride on the record: %+v", ds.Dataset)
		}
		if len(searcher.gotTables) != 0 {
			t.Errorf("GetTableContext must not be called when the reader is wired")
		}
		if len(reader.got) != 1 || reader.got[0].String() != testDatasetTable {
			t.Errorf("reader asked for %v, want %s", reader.got, testDatasetTable)
		}
	})

	t.Run("a reader failure is a clean not-found, as the context read's is", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{err: errors.New("GetEntity: not found")})
		_, owned, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("a record with no URN is not-found", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: &semantic.Dataset{}})
		_, owned, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("query availability rides on the record and marks the document verifiable", func(t *testing.T) {
		rows := int64(4200)
		avail := &fakeAvailability{answers: map[string]*query.TableAvailability{
			testDatasetURN: {Available: true, QueryTable: "iceberg.acme.transactions", Connection: "primary", EstimatedRows: &rows},
		}}
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: record})
		p.SetAvailabilityResolver(avail)
		doc, _, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if err != nil {
			t.Fatal(err)
		}
		ds, ok := doc.Content.(CatalogDataset)
		if !ok {
			t.Fatalf("Content = %T", doc.Content)
		}
		if ds.QueryAvailability == nil || ds.QueryAvailability.QueryTable != "iceberg.acme.transactions" || *ds.QueryAvailability.EstimatedRows != 4200 {
			t.Errorf("query_availability = %+v", ds.QueryAvailability)
		}
		if doc.Verifiable == nil || doc.Verifiable.QueryTable != "iceberg.acme.transactions" || doc.Verifiable.Connection != "primary" || doc.Verifiable.URN != testDatasetURN {
			t.Errorf("Verifiable = %+v", doc.Verifiable)
		}
		if len(avail.got) != 1 || avail.got[0] != testDatasetURN {
			t.Errorf("resolver asked for %v", avail.got)
		}
	})

	t.Run("an unavailable dataset carries the answer but is not verifiable", func(t *testing.T) {
		avail := &fakeAvailability{answers: map[string]*query.TableAvailability{
			testDatasetURN: {Available: false, Error: "no connection serves platform trino"},
		}}
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: record})
		p.SetAvailabilityResolver(avail)
		doc, _, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if err != nil {
			t.Fatal(err)
		}
		ds, ok := doc.Content.(CatalogDataset)
		if !ok {
			t.Fatalf("Content = %T", doc.Content)
		}
		if ds.QueryAvailability == nil || ds.QueryAvailability.Available {
			t.Errorf("query_availability = %+v", ds.QueryAvailability)
		}
		if doc.Verifiable != nil {
			t.Errorf("an unavailable dataset must not be marked verifiable: %+v", doc.Verifiable)
		}
	})

	t.Run("a resolver that fails leaves the record without availability, not the fetch", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: record})
		p.SetAvailabilityResolver(&fakeAvailability{err: errors.New("warehouse unreachable")})
		doc, _, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if err != nil {
			t.Fatal(err)
		}
		if ds, ok := doc.Content.(CatalogDataset); !ok || ds.QueryAvailability != nil || doc.Verifiable != nil {
			t.Errorf("unexpected availability: %+v %+v", doc.Content, doc.Verifiable)
		}
		p.SetAvailabilityResolver(&fakeAvailability{})
		doc, _, err = p.Fetch(context.Background(), testDatasetURN, Caller{})
		if err != nil {
			t.Fatal(err)
		}
		if ds, ok := doc.Content.(CatalogDataset); !ok || ds.QueryAvailability != nil || doc.Verifiable != nil {
			t.Errorf("a resolver with no answer: unexpected availability: %+v %+v", doc.Content, doc.Verifiable)
		}
	})

	t.Run("the boundary judges the URN the reader resolved", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: &semantic.Dataset{TableContext: semantic.TableContext{URN: theirsURN}}})
		caller, _ := scopedCaller(catalogScope())
		_, owned, err := p.Fetch(context.Background(), craftedURN, caller)
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})
}

func TestCatalogProvider_FetchDataProduct(t *testing.T) {
	const productURN = "urn:li:dataProduct:orders-360"
	product := &semantic.DataProduct{
		URN: productURN, Name: "Orders 360", Description: "Everything about an order.",
		Domain: &semantic.Domain{URN: "urn:li:domain:sales", Name: "Sales"},
		Owners: []semantic.Owner{{URN: "urn:li:corpuser:ana", Type: semantic.OwnerTypeUser, Name: "ana"}},
		Assets: []semantic.EntityRef{
			{URN: mineURN, Name: "db.public.orders"},
			{URN: theirsURN, Name: "db.public.payroll"},
			{URN: unmappedURN, Name: "db.public.notes"},
		},
		CustomProperties: map[string]string{"tier": "gold"},
	}

	t.Run("without a product reader the reference is owned and not-found", func(t *testing.T) {
		_, owned, err := NewCatalogProvider(&fakeTableSearcher{}).Fetch(context.Background(), productURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("returns the product with its member datasets", func(t *testing.T) {
		reader := &fakeProductReader{product: product}
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDataProductReader(reader)
		doc, owned, err := p.Fetch(context.Background(), productURN, Caller{})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v", owned, err)
		}
		if doc.Source != SourceCatalog || doc.Title != "Orders 360" || doc.Body != "Everything about an order." {
			t.Errorf("doc = %+v", doc)
		}
		entity, ok := doc.Content.(DataProductEntity)
		if !ok {
			t.Fatalf("Content = %T", doc.Content)
		}
		if entity.Kind != "data_product" || entity.Domain.Name != "Sales" || len(entity.Owners) != 1 || entity.CustomProperties["tier"] != "gold" {
			t.Errorf("entity = %+v", entity)
		}
		if len(entity.Datasets) != 3 || entity.DatasetsWithheld != 0 || entity.Notice != "" {
			t.Errorf("unscoped caller sees every member: %+v", entity)
		}
		if len(doc.EntityURNs) != 3 {
			t.Errorf("EntityURNs = %v", doc.EntityURNs)
		}
		if reader.got[0] != productURN {
			t.Errorf("reader asked for %v", reader.got)
		}
	})

	t.Run("member datasets outside the caller's boundary are withheld and counted", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDataProductReader(&fakeProductReader{product: product})
		caller, _ := scopedCaller(catalogScope())
		doc, _, err := p.Fetch(context.Background(), productURN, caller)
		if err != nil {
			t.Fatal(err)
		}
		entity, ok := doc.Content.(DataProductEntity)
		if !ok {
			t.Fatalf("Content = %T", doc.Content)
		}
		if len(entity.Datasets) != 2 || entity.DatasetsWithheld != 1 || entity.Notice == "" {
			t.Errorf("entity = %+v", entity)
		}
		for _, d := range entity.Datasets {
			if d.URN == theirsURN {
				t.Errorf("the denied member leaked: %+v", entity.Datasets)
			}
		}
	})

	t.Run("a product the catalog cannot read is a clean not-found", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDataProductReader(&fakeProductReader{err: errors.New("GetDataProduct: not found")})
		_, owned, err := p.Fetch(context.Background(), productURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
		p.SetDataProductReader(&fakeProductReader{product: &semantic.DataProduct{}})
		_, owned, err = p.Fetch(context.Background(), productURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("empty product: owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})
}

// TestCatalogProvider_FetchRejectsTheURNOnlyStub is #1605 at the arm rather
// than at the rule: a catalog that answers a reference it has never ingested
// with a record built out of that reference must produce a not-found, because
// the arm's only other not-found conditions (an error, an empty URN) are ones a
// real DataHub does not produce for a missing entity.
func TestCatalogProvider_FetchRejectsTheURNOnlyStub(t *testing.T) {
	t.Run("dataset", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: &semantic.Dataset{
			TableContext: semantic.TableContext{URN: testDatasetURN},
			Name:         testDatasetTable,
			Type:         "DATASET",
			Platform:     "trino",
			Schema:       &semantic.DatasetSchema{},
		}})
		doc, owned, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) || doc != nil {
			t.Errorf("doc=%+v owned=%v err=%v, want owned + ErrNotFound + no document", doc, owned, err)
		}
	})

	t.Run("data product", func(t *testing.T) {
		const productURN = "urn:li:dataProduct:never-ingested"
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDataProductReader(&fakeProductReader{product: &semantic.DataProduct{URN: productURN}})
		doc, owned, err := p.Fetch(context.Background(), productURN, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) || doc != nil {
			t.Errorf("doc=%+v owned=%v err=%v, want owned + ErrNotFound + no document", doc, owned, err)
		}
	})

	// A dataset whose record is partial is not a stub: the arm cannot tell an
	// absent aspect from one this read could not serve, so it resolves.
	t.Run("a partial read still resolves", func(t *testing.T) {
		p := NewCatalogProvider(&fakeTableSearcher{})
		p.SetDatasetReader(&fakeDatasetReader{ds: &semantic.Dataset{
			TableContext: semantic.TableContext{URN: testDatasetURN},
			Name:         testDatasetTable,
			Unavailable:  []string{"schema", "queries"},
		}})
		_, owned, err := p.Fetch(context.Background(), testDatasetURN, Caller{})
		if !owned || err != nil {
			t.Errorf("owned=%v err=%v, want the record to resolve", owned, err)
		}
	})
}
