package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// fakeGovernanceReader is a governanceReader stub: each vocabulary returns a
// fixed page (or an error), and every SearchTables filter it is handed is
// recorded so a test can assert the catalog query a fetch actually issued.
type fakeGovernanceReader struct {
	terms   []semantic.EntityRef
	tags    []semantic.EntityRef
	domains []semantic.EntityRef

	termsErr   error
	tagsErr    error
	domainsErr error

	// term is what GetGlossaryTerm returns; termErr makes the by-URN read fail
	// (which DataHub also does for a term that does not exist).
	term    *semantic.GlossaryTerm
	termErr error

	carriers    []semantic.TableSearchResult
	carriersErr error

	// nameMatchOnly makes the two upstream searches behave as DataHub's do: they
	// match a name, not a sentence that mentions one.
	nameMatchOnly bool

	// termsListErr fails only the ENUMERATION of glossary terms (the empty-query
	// call), leaving the name search healthy, so the fallback's own failure mode
	// is reachable.
	termsListErr error

	gotTermQuery   string
	gotTermQueries []string
	gotTermLimits  []int
	gotTagQuery    string
	gotTagQueries  []string
	gotTagLimit    int
	gotFilters     []semantic.SearchFilter
}

func (f *fakeGovernanceReader) SearchGlossaryTerms(_ context.Context, query string, limit int) ([]semantic.EntityRef, error) {
	f.gotTermQueries = append(f.gotTermQueries, query)
	f.gotTermLimits = append(f.gotTermLimits, limit)
	f.gotTermQuery = query
	if query == "" && f.termsListErr != nil {
		return nil, f.termsListErr
	}
	if f.nameMatchOnly && query != "" {
		return nameMatches(f.terms, query), f.termsErr
	}
	return f.terms, f.termsErr
}

// nameMatches models what DataHub's name search does and does not do: it matches
// a NAME, so a natural-language question that merely mentions a term does not
// find it. Used to exercise the local-ranking fallback.
func nameMatches(refs []semantic.EntityRef, query string) []semantic.EntityRef {
	var out []semantic.EntityRef
	for _, ref := range refs {
		if strings.EqualFold(ref.Name, query) {
			out = append(out, ref)
		}
	}
	return out
}

func (f *fakeGovernanceReader) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return f.term, f.termErr
}

func (f *fakeGovernanceReader) SearchTags(_ context.Context, query string, limit int) ([]semantic.EntityRef, error) {
	f.gotTagQueries = append(f.gotTagQueries, query)
	f.gotTagQuery, f.gotTagLimit = query, limit
	if f.nameMatchOnly && query != "" {
		return nameMatches(f.tags, query), f.tagsErr
	}
	return f.tags, f.tagsErr
}

func (f *fakeGovernanceReader) ListDomains(context.Context) ([]semantic.EntityRef, error) {
	return f.domains, f.domainsErr
}

func (f *fakeGovernanceReader) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	results, _, err := f.SearchTablesCounted(ctx, filter)
	return results, err
}

// SearchTablesCounted models the catalog contract the provider relies on: the
// page is bounded by the requested limit, and the total is the number of matches
// behind it. A caller cannot recover the total from the page, which is the whole
// reason the count is returned separately (#1238).
func (f *fakeGovernanceReader) SearchTablesCounted(
	_ context.Context, filter semantic.SearchFilter,
) ([]semantic.TableSearchResult, int, error) {
	f.gotFilters = append(f.gotFilters, filter)
	if f.carriersErr != nil {
		return nil, semantic.TotalUnknown, f.carriersErr
	}
	page := f.carriers
	if filter.Limit > 0 && filter.Limit < len(page) {
		page = page[:filter.Limit]
	}
	return page, len(f.carriers), nil
}

// populatedReader is a reader with one entry in each vocabulary, all matching
// the word "revenue", plus two carrying datasets.
func populatedReader() *fakeGovernanceReader {
	return &fakeGovernanceReader{
		terms: []semantic.EntityRef{{
			URN: "urn:li:glossaryTerm:8f3c", Name: "Net Revenue",
			Description: "Recognized revenue less refunds and credits.",
		}},
		tags: []semantic.EntityRef{{
			URN: "urn:li:tag:revenue-critical", Name: "revenue-critical",
			Description: "Feeds the revenue report.",
		}},
		domains: []semantic.EntityRef{
			{URN: "urn:li:domain:finance", Name: "Finance", Description: "Revenue and billing."},
			{URN: "urn:li:domain:hr", Name: "People", Description: "Headcount."},
		},
		carriers: []semantic.TableSearchResult{
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.sales.invoices,PROD)", Name: "warehouse.sales.invoices"},
			{URN: "urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.sales.credits,PROD)", Name: "warehouse.sales.credits"},
		},
	}
}

func TestGovernanceProvider_SearchSurfacesEachVocabularyWithItsDefinition(t *testing.T) {
	f := populatedReader()
	p := NewGovernanceProvider(f)

	assert.Equal(t, SourceGovernance, p.Name())
	assert.Equal(t, ScopeShared, p.Scope())

	hits, err := p.Search(context.Background(), Query{Intent: "revenue", Limit: 10})
	require.NoError(t, err)
	require.Len(t, hits, 3, "one hit per vocabulary")

	// The intent reaches the two vocabularies that have an upstream search.
	assert.Equal(t, "revenue", f.gotTermQuery)
	assert.Equal(t, "revenue", f.gotTagQuery)

	// The term leads the interleave, and its definition rides in the hit text so
	// "what does Net Revenue mean" is answered without a follow-up fetch.
	assert.Equal(t, "urn:li:glossaryTerm:8f3c", hits[0].Ref)
	assert.Equal(t, "urn:li:glossaryTerm:8f3c", hits[0].Reference)
	assert.Equal(t, SourceGovernance, hits[0].Source)
	assert.Contains(t, hits[0].Text, "Net Revenue (glossary term)")
	assert.Contains(t, hits[0].Text, "Recognized revenue less refunds and credits.")
	assert.Equal(t, []string{"urn:li:glossaryTerm:8f3c"}, hits[0].EntityURNs)

	assert.Contains(t, hits[1].Text, "revenue-critical (tag)")
	assert.Contains(t, hits[2].Text, "Finance (domain)")
	assert.Contains(t, hits[2].Text, "Revenue and billing.")

	// Scores descend over the merged order, so the allocator can normalize them.
	assert.Greater(t, hits[0].Score, hits[1].Score)
	assert.Greater(t, hits[1].Score, hits[2].Score)
}

func TestGovernanceProvider_DomainsRankedLocallyBecauseUpstreamCannot(t *testing.T) {
	f := populatedReader()
	// Only the domain vocabulary is consulted, so the ranking under test is the
	// local one rather than an upstream page order.
	f.terms, f.tags = nil, nil
	f.domains = []semantic.EntityRef{
		{URN: "urn:li:domain:hr", Name: "People", Description: "Headcount."},
		{URN: "urn:li:domain:finance", Name: "Finance", Description: "Revenue and billing."},
		{URN: "urn:li:domain:billing", Name: "Billing revenue", Description: "Invoices."},
	}
	p := NewGovernanceProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "billing revenue", Limit: 10})
	require.NoError(t, err)
	require.Len(t, hits, 2, "the domain nothing matched is dropped, not ranked last")

	// "Billing revenue" matches both query tokens; Finance matches one.
	assert.Equal(t, "urn:li:domain:billing", hits[0].Ref)
	assert.Equal(t, "urn:li:domain:finance", hits[1].Ref)
}

func TestGovernanceProvider_SearchWithoutIntentYieldsNothing(t *testing.T) {
	f := populatedReader()
	p := NewGovernanceProvider(f)

	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:li:glossaryTerm:8f3c"}})
	require.NoError(t, err)
	assert.Empty(t, hits, "a vocabulary entry is found by what it is called, not by an entity key")
	assert.Empty(t, f.gotTermQuery, "no intent must cost no upstream read")
}

func TestGovernanceProvider_OneFailedVocabularyDegradesRatherThanBlanks(t *testing.T) {
	f := populatedReader()
	f.tagsErr = errors.New("tag index down")
	p := NewGovernanceProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "revenue", Limit: 10})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	for _, h := range hits {
		assert.NotContains(t, h.Ref, "urn:li:tag:")
	}
}

func TestGovernanceProvider_EveryVocabularyFailedBlanksTheSource(t *testing.T) {
	f := &fakeGovernanceReader{
		termsErr:   errors.New("terms down"),
		tagsErr:    errors.New("tags down"),
		domainsErr: errors.New("domains down"),
	}
	p := NewGovernanceProvider(f)

	_, err := p.Search(context.Background(), Query{Intent: "revenue", Limit: 10})
	require.Error(t, err, "an unhealthy catalog must not read as an empty vocabulary")
	assert.Contains(t, err.Error(), "terms down")
	assert.Contains(t, err.Error(), "domains down")
}

func TestGovernanceProvider_FetchGlossaryTermReturnsDefinitionAndCarriers(t *testing.T) {
	f := populatedReader()
	f.term = &semantic.GlossaryTerm{
		URN: "urn:li:glossaryTerm:8f3c", Name: "Net Revenue",
		Description: "Recognized revenue less refunds and credits.",
	}
	p := NewGovernanceProvider(f)

	doc, owned, err := p.Fetch(context.Background(), "urn:li:glossaryTerm:8f3c", Caller{})
	require.True(t, owned)
	require.NoError(t, err)
	require.NotNil(t, doc)

	assert.Equal(t, "urn:li:glossaryTerm:8f3c", doc.Reference)
	assert.Equal(t, SourceGovernance, doc.Source)
	assert.Equal(t, "Net Revenue", doc.Title)
	assert.Equal(t, "Recognized revenue less refunds and credits.", doc.Body)

	entity, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok)
	assert.Equal(t, "glossary_term", entity.Kind)
	require.Len(t, entity.Datasets, 2)
	assert.Equal(t, "warehouse.sales.invoices", entity.Datasets[0].Name)
	assert.False(t, entity.MoreDatasets)
	assert.Zero(t, entity.DatasetsWithheld)
	assert.Len(t, doc.EntityURNs, 2)

	// The carrier query uses the term filter that matches table AND column
	// assignments; there is no table-level-only field upstream.
	require.Len(t, f.gotFilters, 1)
	require.Len(t, f.gotFilters[0].Filters, 1)
	assert.Equal(t, semantic.FilterFieldGlossaryTerms, f.gotFilters[0].Filters[0].Field)
	assert.Equal(t, []string{"urn:li:glossaryTerm:8f3c"}, f.gotFilters[0].Filters[0].Values)
	// A listing query is "*": the query reaches DataHub verbatim, where an empty
	// one matches nothing and would report every entity as carried by no dataset.
	assert.Equal(t, "*", f.gotFilters[0].Query)
}

func TestGovernanceProvider_FetchTagAndDomainResolveByListing(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		wantKind   string
		wantName   string
		wantFilter func(t *testing.T, filter semantic.SearchFilter)
	}{
		{
			name: "tag", ref: "urn:li:tag:revenue-critical", wantKind: "tag", wantName: "revenue-critical",
			wantFilter: func(t *testing.T, filter semantic.SearchFilter) {
				t.Helper()
				assert.Equal(t, []string{"urn:li:tag:revenue-critical"}, filter.Tags)
				assert.Equal(t, "*", filter.Query)
			},
		},
		{
			name: "domain", ref: "urn:li:domain:finance", wantKind: "domain", wantName: "Finance",
			wantFilter: func(t *testing.T, filter semantic.SearchFilter) {
				t.Helper()
				assert.Equal(t, "urn:li:domain:finance", filter.Domain)
				assert.Equal(t, "*", filter.Query)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := populatedReader()
			p := NewGovernanceProvider(f)

			doc, owned, err := p.Fetch(context.Background(), tc.ref, Caller{})
			require.True(t, owned)
			require.NoError(t, err)
			require.NotNil(t, doc)

			entity, ok := doc.Content.(GovernanceEntity)
			require.True(t, ok)
			assert.Equal(t, tc.wantKind, entity.Kind)
			assert.Equal(t, tc.wantName, entity.Name)
			assert.Equal(t, tc.ref, entity.URN)
			assert.NotEmpty(t, entity.Description)
			require.Len(t, entity.Datasets, 2)

			require.Len(t, f.gotFilters, 1)
			tc.wantFilter(t, f.gotFilters[0])
		})
	}
}

func TestGovernanceProvider_FetchDeclinesReferencesItDoesNotOwn(t *testing.T) {
	p := NewGovernanceProvider(populatedReader())
	for _, ref := range []string{
		"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)",
		"urn:li:document:doc-1",
		"mcp:knowledge_page:abc",
		"",
	} {
		doc, owned, err := p.Fetch(context.Background(), ref, Caller{})
		assert.False(t, owned, "ref %q must fall through to the next provider", ref)
		assert.NoError(t, err)
		assert.Nil(t, doc)
	}
}

func TestGovernanceProvider_FetchStaleReferenceIsCleanNotFound(t *testing.T) {
	// A term DataHub does not hold: the by-URN read errors, which is how DataHub
	// reports a missing entity, so it must read as not-found rather than failure.
	term := &fakeGovernanceReader{termErr: errors.New("entity not found")}
	_, owned, err := NewGovernanceProvider(term).Fetch(context.Background(), "urn:li:glossaryTerm:gone", Caller{})
	assert.True(t, owned)
	assert.ErrorIs(t, err, ErrNotFound)

	// A tag URN absent from the vocabulary listing.
	empty := &fakeGovernanceReader{}
	_, owned, err = NewGovernanceProvider(empty).Fetch(context.Background(), "urn:li:tag:gone", Caller{})
	assert.True(t, owned)
	assert.ErrorIs(t, err, ErrNotFound)

	// A domain URN absent from the enumeration.
	_, owned, err = NewGovernanceProvider(empty).Fetch(context.Background(), "urn:li:domain:gone", Caller{})
	assert.True(t, owned)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGovernanceProvider_FetchVocabularyReadFailureIsNotAFalseNotFound(t *testing.T) {
	// Unlike a term (whose by-URN read conflates the two), a failing vocabulary
	// LISTING is unambiguously a failure: reporting it as not-found would tell a
	// caller a tag was retired when DataHub was merely unreachable.
	f := &fakeGovernanceReader{tagsErr: errors.New("tag index down")}
	_, owned, err := NewGovernanceProvider(f).Fetch(context.Background(), "urn:li:tag:x", Caller{})
	assert.True(t, owned)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestGovernanceProvider_CarrierSearchFailureStillReturnsTheDefinition(t *testing.T) {
	f := populatedReader()
	f.carriersErr = errors.New("catalog search down")
	p := NewGovernanceProvider(f)

	doc, owned, err := p.Fetch(context.Background(), "urn:li:tag:revenue-critical", Caller{})
	require.True(t, owned)
	require.NoError(t, err, "the definition is the answer; losing the membership costs context, not the read")
	entity, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok)
	assert.Empty(t, entity.Datasets)
	assert.Equal(t, "revenue-critical", entity.Name)
}

// TestGovernanceProvider_CarrierListReportsWhatItDoesNotShow pins MoreDatasets to
// the catalog's match count rather than to the size of the page the provider
// asked for. A page that came back exactly full is not evidence of anything: the
// catalog is free to return fewer rows than requested, so the count is the only
// signal that distinguishes a bounded membership from an exhausted one (#1238).
func TestGovernanceProvider_CarrierListReportsWhatItDoesNotShow(t *testing.T) {
	carriers := func(n int) []semantic.TableSearchResult {
		out := make([]semantic.TableSearchResult, n)
		for i := range out {
			out[i] = semantic.TableSearchResult{
				URN: fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,a.b.t%d,PROD)", i),
			}
		}
		return out
	}
	tests := []struct {
		name     string
		matches  int
		wantMore bool
		wantLen  int
	}{
		{name: "more carriers than the list holds", matches: carrierLimit + 15, wantMore: true, wantLen: carrierLimit},
		{name: "exactly as many carriers as the limit", matches: carrierLimit, wantMore: false, wantLen: carrierLimit},
		{name: "fewer carriers than the limit", matches: 2, wantMore: false, wantLen: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := populatedReader()
			f.carriers = carriers(tc.matches)
			p := NewGovernanceProvider(f)

			doc, _, err := p.Fetch(context.Background(), "urn:li:domain:finance", Caller{})
			require.NoError(t, err)
			entity, ok := doc.Content.(GovernanceEntity)
			require.True(t, ok)
			assert.Equal(t, carrierLimit, f.gotFilters[0].Limit)
			assert.Len(t, entity.Datasets, tc.wantLen)
			assert.Equal(t, tc.wantMore, entity.MoreDatasets)
		})
	}
}

// TestGovernanceProvider_UncountedReaderDoesNotClaimTheWholeMembership covers the
// fallback: a reader with no match count cannot show that the list holds every
// carrier, so the fetch points the reader at the catalog instead of presenting a
// bounded list as the entity's whole membership.
func TestGovernanceProvider_UncountedReaderDoesNotClaimTheWholeMembership(t *testing.T) {
	f := populatedReader()
	p := NewGovernanceProvider(uncountedGovernanceReader{f})

	doc, _, err := p.Fetch(context.Background(), "urn:li:domain:finance", Caller{})
	require.NoError(t, err)
	entity, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok)
	assert.Len(t, entity.Datasets, 2)
	assert.True(t, entity.MoreDatasets)
}

// uncountedGovernanceReader hides the fake's counting capability, standing in for
// a catalog backend that implements only the plain search.
type uncountedGovernanceReader struct{ governanceReader }

// governanceScope denies every connection and attributes any DATASET URN to one,
// which is the shape that separates the two visibility rules under test: a
// governance URN carries no platform segment and so is attributable to nothing.
type governanceScope struct{}

func (governanceScope) ConnectionsForURN(urn string) []string {
	if strings.HasPrefix(urn, datasetPrefix) {
		return []string{"restricted"}
	}
	return nil
}
func (governanceScope) AllowConnection(string, string) bool { return false }

func TestGovernanceProvider_EntityStaysVisibleButItsCarriersAreScoped(t *testing.T) {
	f := populatedReader()
	p := NewGovernanceProvider(f)
	caller, _ := scopedCaller(governanceScope{})

	// The entity itself: unattributable to any connection, so the documented rule
	// leaves it visible rather than hiding it on a guess.
	doc, owned, err := p.Fetch(context.Background(), "urn:li:tag:revenue-critical", caller)
	require.True(t, owned)
	require.NoError(t, err)
	require.NotNil(t, doc)

	// Its carrying datasets ARE attributable, and the caller is granted none of
	// them, so they are removed and the removal is explained.
	entity, ok := doc.Content.(GovernanceEntity)
	require.True(t, ok)
	assert.Empty(t, entity.Datasets)
	assert.Empty(t, doc.EntityURNs)
	assert.Equal(t, 2, entity.DatasetsWithheld)
	assert.Contains(t, entity.Notice, "your persona ("+testPersona+")")
	assert.Contains(t, entity.Notice, "Ask an administrator")
}

func TestGovernanceProvider_SearchHitsRunThroughTheBoundary(t *testing.T) {
	f := populatedReader()
	p := NewGovernanceProvider(f)
	caller, gate := scopedCaller(denyAllScope{})

	hits, err := p.Search(context.Background(), Query{Intent: "revenue", Limit: 10, Caller: caller})
	require.NoError(t, err)
	assert.Empty(t, hits, "a scope that attributes every URN to a denied connection hides the hits")
	assert.Equal(t, 3, gate.withheld, "and reports them as present-but-not-yours rather than absent")
}

func TestGovernanceProvider_UnnamedEntryFallsBackToItsFullURN(t *testing.T) {
	// DataHub generates a UUID key for an entity created without an explicit id,
	// so the last URN segment is not a name; the whole URN is the honest fallback.
	f := &fakeGovernanceReader{domains: []semantic.EntityRef{
		{URN: "urn:li:domain:0f1e2d3c", Description: "revenue reporting"},
	}}
	p := NewGovernanceProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "revenue", Limit: 10})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Contains(t, hits[0].Text, "urn:li:domain:0f1e2d3c (domain)")
}

func TestGovernanceProvider_TagResolveListsTheWholeVocabulary(t *testing.T) {
	// A tag has no by-URN read upstream, so resolving one means listing and
	// matching; the listing must be unfiltered or the URN would never be found.
	f := populatedReader()
	_, _, err := NewGovernanceProvider(f).Fetch(context.Background(), "urn:li:tag:revenue-critical", Caller{})
	require.NoError(t, err)
	assert.Empty(t, f.gotTagQuery, "a name query cannot find a UUID-keyed tag")
	assert.Equal(t, vocabularyPageLimit, f.gotTagLimit)
}

// TestGovernanceRoundTripThroughRouter is the assembled-system proof for #1160:
// the governance source is queried through the real Router alongside the two
// other urn:li: sources, and the reference each hit carries is dereferenced back
// through the real Router.Fetch. A unit test on the provider alone would pass
// even if the source were never registered or if another provider claimed its
// reference form first, which is exactly the wiring this asserts.
func TestGovernanceRoundTripThroughRouter(t *testing.T) {
	gov := populatedReader()
	gov.term = &semantic.GlossaryTerm{
		URN: "urn:li:glossaryTerm:8f3c", Name: "Net Revenue",
		Description: "Recognized revenue less refunds and credits.",
	}
	// The two sibling urn:li: sources are registered ahead of governance, so the
	// round trip also proves the reference partition: neither claims a governance
	// URN, and governance claims neither of theirs.
	catalog := NewCatalogProvider(&fakeTableSearcher{
		results: []semantic.TableSearchResult{{URN: testDatasetURN, Name: testDatasetTable}},
		byTable: map[string]*semantic.TableContext{testDatasetTable: {URN: testDatasetURN, Description: "revenue by day"}},
	})
	docs := NewContextDocumentsProvider(&fakeDocumentSearcher{
		docs: []semantic.DocumentResult{{URN: "urn:li:document:doc-1", Title: "Revenue runbook", Status: "PUBLISHED", ShowInGlobalContext: true}},
		doc:  &semantic.DocumentResult{URN: "urn:li:document:doc-1", Title: "Revenue runbook", Body: "full body"},
	})
	router := NewRouter(nil, nil, catalog, docs, NewGovernanceProvider(gov))

	res, err := router.Search(context.Background(), Query{Intent: "revenue", Limit: 50})
	require.NoError(t, err)

	// Every governance vocabulary is represented in the grouped result, under its
	// own source label rather than folded into the catalog's.
	byRef := make(map[string]Hit)
	for _, g := range res.Groups {
		for _, h := range g.Hits {
			assert.Equal(t, g.Source, h.Source)
			byRef[h.Reference] = h
		}
	}
	for _, ref := range []string{
		"urn:li:glossaryTerm:8f3c", "urn:li:tag:revenue-critical", "urn:li:domain:finance",
	} {
		hit, ok := byRef[ref]
		require.True(t, ok, "search must surface %s", ref)
		require.Equal(t, SourceGovernance, hit.Source)

		// The reference search emitted dereferences through the router, to the same
		// entity, with the definition the hit previewed.
		doc, fErr := router.Fetch(context.Background(), hit.Reference, Caller{})
		require.NoError(t, fErr, "the reference %s must fetch", ref)
		require.NotNil(t, doc)
		assert.Equal(t, SourceGovernance, doc.Source)
		assert.Equal(t, ref, doc.Reference)
		entity, isEntity := doc.Content.(GovernanceEntity)
		require.True(t, isEntity)
		assert.Contains(t, hit.Text, entity.Name)
		assert.Equal(t, entity.Description, doc.Body)
		assert.NotEmpty(t, entity.Datasets, "a governance entity carries the datasets that use it")
	}

	// The sibling sources still own their own forms through the same walk.
	dataset, err := router.Fetch(context.Background(), testDatasetURN, Caller{})
	require.NoError(t, err)
	assert.Equal(t, SourceCatalog, dataset.Source)
	document, err := router.Fetch(context.Background(), "urn:li:document:doc-1", Caller{})
	require.NoError(t, err)
	assert.Equal(t, SourceContextDocuments, document.Source)
}

func TestGovernanceProvider_NaturalLanguageIntentFallsBackToLocalRanking(t *testing.T) {
	// The acceptance case: search receives a QUESTION, while DataHub's glossary
	// and tag searches match a NAME. Without the fallback, "what does net revenue
	// mean here" would find nothing and the source would answer no question a
	// caller actually asks.
	f := populatedReader()
	f.nameMatchOnly = true
	p := NewGovernanceProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "what does net revenue mean here", Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "urn:li:glossaryTerm:8f3c", hits[0].Ref)
	assert.Contains(t, hits[0].Text, "Recognized revenue less refunds and credits.")

	// Upstream's own search still leads: the enumeration ran only after it came
	// back empty, so a deployment whose glossary DataHub can rank pays one read.
	assert.Equal(t, []string{"what does net revenue mean here", ""}, f.gotTermQueries)
}

func TestGovernanceProvider_UpstreamSearchLeadsWhenItAnswers(t *testing.T) {
	f := populatedReader()
	p := NewGovernanceProvider(f)

	_, err := p.Search(context.Background(), Query{Intent: "revenue", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"revenue"}, f.gotTermQueries, "a ranked upstream answer needs no enumeration")
	assert.Equal(t, []string{"revenue"}, f.gotTagQueries)
}

func TestGovernanceProvider_FallbackFailureAfterAnEmptyAnswerIsNotAFailure(t *testing.T) {
	// The vocabulary answered "nothing" before the fallback was attempted, so the
	// fallback failing must not be reported as the vocabulary being down — that
	// would blank a healthy source whenever a listing hiccupped.
	f := populatedReader()
	f.nameMatchOnly = true
	// The term name search answers (with nothing, for this question); only the
	// enumeration behind the fallback fails.
	f.termsListErr = errors.New("listing timed out")
	p := NewGovernanceProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "which tables are revenue critical", Limit: 10})
	require.NoError(t, err, "a best-effort fallback failing must not blank a source that answered")
	require.NotEmpty(t, hits, "the healthy vocabularies still answer")
	for _, h := range hits {
		assert.NotContains(t, h.Ref, "urn:li:glossaryTerm:")
	}
}

func TestGovernanceProvider_FallbackEnumeratesTheVocabularyNotTheDisplayPage(t *testing.T) {
	// Ranking happens locally, so the fallback must read the vocabulary page. Asking
	// only for the display budget would rank an arbitrary slice of the vocabulary
	// and drop the one entry the caller was asking about.
	f := populatedReader()
	f.nameMatchOnly = true
	f.tags = nil
	f.domains = nil
	p := NewGovernanceProvider(f)

	_, err := p.Search(context.Background(), Query{Intent: "what does net revenue mean", Limit: 3})
	require.NoError(t, err)
	assert.Equal(t, vocabularyPageLimit, f.gotTermLimits[len(f.gotTermLimits)-1],
		"the enumeration behind the fallback reads the vocabulary page")
}
