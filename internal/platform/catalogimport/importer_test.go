package catalogimport

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// fakeSource is a DataHub held in memory.
type fakeSource struct {
	heads   []DatasetHead
	records map[string]*DatasetRecord
	defs    []Definition
	err     error
	reads   map[string]int
}

func (f *fakeSource) DatasetHeads(context.Context) ([]DatasetHead, error) { return f.heads, f.err }
func (f *fakeSource) Definitions(context.Context) ([]Definition, error)   { return f.defs, nil }
func (f *fakeSource) Dataset(_ context.Context, urn string) (*DatasetRecord, error) {
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[urn]++
	rec, ok := f.records[urn]
	if !ok {
		return nil, errors.New("not in DataHub")
	}
	return rec, nil
}

// memMapping is the import's mapping held in memory.
type memMapping map[string]string

func (m memMapping) Map(context.Context) (map[string]string, error) {
	return maps.Clone(map[string]string(m)), nil
}

func (m memMapping) Record(_ context.Context, urn, ref string) error {
	m[urn] = ref
	return nil
}

const (
	ordersURN        = "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"
	ordersRemovedURN = "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,DEV)"
	usersURN         = "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.users,PROD)"
	tableauURN       = "urn:li:dataset:(urn:li:dataPlatform:tableau,workbook.sheet,PROD)"
	missingURN       = "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.gone,PROD)"
	termURN          = "urn:li:glossaryTerm:net-revenue"
	nodeURN          = "urn:li:glossaryNode:finance"
	domainURN        = "urn:li:domain:sales"
)

// importFixture is a catalog with two datasets and a DataHub holding their
// curation.
type importFixture struct {
	cat           *catalogfake.Store
	orders, users catalog.Ref
	src           *fakeSource
}

func fixture() importFixture {
	cat := catalogfake.New()
	orders := cat.AddDataset("warehouse", "hive.sales.orders", "id", "amount")
	users := cat.AddDataset("warehouse", "hive.sales.users", "id")
	src := &fakeSource{
		heads: []DatasetHead{
			{URN: ordersRemovedURN, Removed: true},
			{URN: ordersURN},
			{URN: usersURN},
			{URN: tableauURN},
			{URN: missingURN},
		},
		records: map[string]*DatasetRecord{
			ordersURN: {URN: ordersURN, Aspects: catalog.Aspects{
				Description:   "Orders placed online.",
				Tags:          []string{"pii"},
				GlossaryTerms: []string{termURN, "urn:li:glossaryTerm:unknown"},
				Domain:        domainURN,
				Owners:        []catalog.Owner{{Owner: "ana", Type: "dataowner"}},
			}, Columns: map[string]catalog.Aspects{
				"AMOUNT":  {Description: "Total in USD.", Tags: []string{"money"}},
				"missing": {Description: "No such column."},
			}, Upstreams: []string{usersURN, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket.file,PROD)"}},
			ordersRemovedURN: {URN: ordersRemovedURN, Removed: true, Aspects: catalog.Aspects{
				Description:      "An old description the live record replaces.",
				CustomProperties: map[string]string{"only_on_removed": "yes"},
			}, Columns: map[string]catalog.Aspects{"id": {Description: "Order key."}}},
			usersURN: {URN: usersURN, Aspects: catalog.Aspects{Description: "Users."}},
		},
		defs: []Definition{
			{
				URN: termURN, Kind: catalog.KindGlossaryTerm, Name: "Net revenue", Parent: nodeURN,
				Aspects: catalog.Aspects{Description: "Gross sales minus returns."},
			},
			{URN: nodeURN, Kind: catalog.KindGlossaryNode, Name: "Finance"},
			{URN: domainURN, Kind: catalog.KindDomain, Name: "Sales"},
			{URN: "urn:li:tag:pii", Kind: catalog.KindTag, Name: "pii", Aspects: catalog.Aspects{Description: "Personal data."}},
		},
	}
	return importFixture{cat: cat, orders: orders, users: users, src: src}
}

// The import carries a live record's curation and fills what it lacks from a
// removed record of the same path, maps glossary terms and the domain to the
// definitions it imported, and records lineage between the datasets it holds.
func TestImportCarriesCurationAndFillsFromRemovedRecords(t *testing.T) {
	fx := fixture()
	cat, orders, users, src := fx.cat, fx.orders, fx.users, fx.src
	mapping := memMapping{}
	report, err := New(cat, mapping, Options{}).Run(context.Background(), src, nil)
	require.NoError(t, err)

	a := cat.Aspects(orders)
	assert.Equal(t, "Orders placed online.", a.Description, "the live record's value wins")
	assert.Equal(t, map[string]string{"only_on_removed": "yes"}, a.CustomProperties, "a removed record fills what the live one lacks")
	require.Len(t, a.GlossaryTerms, 1, "a term the import has no definition for is left out")
	assert.Equal(t, mapping[termURN], a.GlossaryTerms[0])
	assert.Equal(t, mapping[domainURN], a.Domain)
	assert.Equal(t, []string{"pii"}, a.Tags)

	amount := cat.Aspects(catalog.ColumnRef(orders, "amount"))
	assert.Equal(t, "Total in USD.", amount.Description, "a field matches its column ignoring case")
	assert.Equal(t, "Order key.", cat.Aspects(catalog.ColumnRef(orders, "id")).Description)
	assert.Equal(t, "Users.", cat.Aspects(users).Description)

	term, err := cat.GetDefinition(context.Background(), catalog.KeyRef(catalog.KindGlossaryTerm, "net-revenue"))
	require.NoError(t, err)
	assert.Equal(t, "Gross sales minus returns.", term.Aspects.Description)
	assert.Equal(t, mapping[nodeURN], term.Parent, "a term sits under the node imported before it")

	assert.Equal(t, 5, report.Datasets.Read)
	assert.Equal(t, 2, report.Datasets.Imported)
	assert.Equal(t, 1, report.Datasets.FromRemoved)
	assert.Equal(t, 2, report.Datasets.Skipped)
	assert.Equal(t, 1, report.LineageEdges)
	assert.Equal(t, map[string]int{"glossary_term": 1, "glossary_node": 1, "domain": 1, "tag": 1}, report.Definitions)
	assert.Equal(t, 1, report.SkippedByReason[ReasonTableau])
	assert.Equal(t, 1, report.SkippedByReason[ReasonNotCataloged])
	assert.Equal(t, 1, report.SkippedByReason[ReasonNoColumn])
	assert.Equal(t, 2, report.SkippedByReason[ReasonUnmapped], "the unknown term and the S3 upstream")
	assert.Equal(t, orders.String(), mapping[ordersURN])
	assert.Equal(t, orders.String(), mapping[ordersRemovedURN], "a removed record maps to the live dataset of its path")
	assert.Equal(t, 1, src.reads[ordersURN], "each record is read once")
}

// A second run writes nothing that is already there, and an aspect written
// in the catalog by anyone else is kept.
func TestImportAgainKeepsPlatformCuration(t *testing.T) {
	fx := fixture()
	cat, orders, src := fx.cat, fx.orders, fx.src
	mapping := memMapping{}
	im := New(cat, mapping, Options{})
	_, err := im.Run(context.Background(), src, nil)
	require.NoError(t, err)

	_, err = cat.WriteAspect(context.Background(), orders, catalog.AspectDescription,
		[]byte(`"Written in the platform."`), "ana@example.com")
	require.NoError(t, err)
	src.records[ordersURN].Aspects.Description = "Changed in DataHub."
	src.records[ordersURN].Aspects.Tags = []string{"pii", "finance"}

	report, err := im.Run(context.Background(), src, nil)
	require.NoError(t, err)
	a := cat.Aspects(orders)
	assert.Equal(t, "Written in the platform.", a.Description)
	assert.Equal(t, []string{"pii", "finance"}, a.Tags, "an aspect only the import wrote follows DataHub")
	assert.Equal(t, 1, report.AspectsKept)
	assert.Equal(t, 1, report.AspectsWritten)
	assert.Positive(t, report.AspectsUnchanged)
	assert.Equal(t, 1, report.Definitions["glossary_term"], "the term is kept under the key it was first stored under")
}

// A definition whose name another DataHub definition already took is stored
// under its name and its DataHub key.
func TestImportSeparatesDefinitionsThatShareAName(t *testing.T) {
	fx := fixture()
	cat, src := fx.cat, fx.src
	src.defs = []Definition{
		{URN: "urn:li:glossaryTerm:a", Kind: catalog.KindGlossaryTerm, Name: "Revenue"},
		{URN: "urn:li:glossaryTerm:b", Kind: catalog.KindGlossaryTerm, Name: "Revenue"},
	}
	mapping := memMapping{}
	_, err := New(cat, mapping, Options{}).Run(context.Background(), src, nil)
	require.NoError(t, err)
	assert.NotEqual(t, mapping["urn:li:glossaryTerm:a"], mapping["urn:li:glossaryTerm:b"])
	_, err = cat.GetDefinition(context.Background(), catalog.KeyRef(catalog.KindGlossaryTerm, "revenue-b"))
	assert.NoError(t, err)
}

// A dataset name with a platform instance in front, one under a catalog name
// DataHub knows differently, and one under the deployment's own platform name
// all land on their catalog dataset.
func TestResolveReadsDataHubNamesBack(t *testing.T) {
	fx := fixture()
	cat, orders := fx.cat, fx.orders
	im := New(cat, memMapping{}, Options{Platform: "postgres", CatalogMapping: map[string]string{"hive": "lake"}})
	r := &run{im: im, ctx: context.Background(), mapped: map[string]string{}, report: newReport()}
	for _, urn := range []string{
		"urn:li:dataset:(urn:li:dataPlatform:trino,prod.hive.sales.orders,PROD)",
		"urn:li:dataset:(urn:li:dataPlatform:trino,lake.sales.orders,PROD)",
		"urn:li:dataset:(urn:li:dataPlatform:postgres,hive.sales.orders,PROD)",
	} {
		refs, reason := r.resolve(urn)
		assert.Equal(t, []string{orders.String()}, refs, urn)
		assert.Empty(t, reason)
	}
	_, reason := r.resolve("urn:li:dataset:(urn:li:dataPlatform:s3,x,PROD)")
	assert.Equal(t, ReasonPlatform, reason)
	_, reason = r.resolve("urn:li:dataset:broken")
	assert.Equal(t, ReasonUnsupported, reason)
}

// The rewrite resolves what the import recorded, a tag by its name, a
// dataset the import did not list by its path, and a field to its column.
func TestResolveRef(t *testing.T) {
	fx := fixture()
	cat, orders := fx.cat, fx.orders
	r := &run{
		im: New(cat, memMapping{}, Options{}), ctx: context.Background(),
		mapped: map[string]string{termURN: "mcp:glossary_term:t1"}, report: newReport(),
	}
	assert.Equal(t, "mcp:glossary_term:t1", r.resolveRef(termURN))
	assert.Equal(t, "mcp:tag:pii", r.resolveRef("urn:li:tag:pii"))
	assert.Equal(t, orders.String(), r.resolveRef(ordersURN))
	assert.Equal(t, catalog.ColumnRef(orders, "amount").String(),
		r.resolveRef("urn:li:schemaField:("+ordersURN+",amount)"))
	assert.Empty(t, r.resolveRef("urn:li:schemaField:("+ordersURN+",a.b)"), "a nested field has no column")
	assert.Empty(t, r.resolveRef("urn:li:schemaField:nocomma"))
	assert.Empty(t, r.resolveRef("urn:li:corpuser:ana"))
	assert.Empty(t, r.resolveRef(missingURN))
}

func TestImportFailures(t *testing.T) {
	fx := fixture()
	cat, src := fx.cat, fx.src
	src.err = errors.New("DataHub down")
	_, err := New(cat, memMapping{}, Options{}).Run(context.Background(), src, nil)
	require.ErrorContains(t, err, "DataHub down")

	fx = fixture()
	cat, src = fx.cat, fx.src
	delete(src.records, usersURN)
	report, err := New(cat, memMapping{}, Options{}).Run(context.Background(), src, nil)
	require.NoError(t, err, "an unreadable record is reported, not fatal")
	assert.Equal(t, 1, countPrefix(report, ReasonUnreadable))
}

// countPrefix counts the skips whose reason starts with prefix.
func countPrefix(r *Report, prefix string) int {
	n := 0
	for reason, c := range r.SkippedByReason {
		if len(reason) >= len(prefix) && reason[:len(prefix)] == prefix {
			n += c
		}
	}
	return n
}

// Run hands the rewrite the resolver once the import is done.
func TestRunRewritesAfterImporting(t *testing.T) {
	fx := fixture()
	cat, orders, src := fx.cat, fx.orders, fx.src
	rw := &spyRewrite{}
	_, err := New(cat, memMapping{}, Options{}).Run(context.Background(), src, rw)
	require.NoError(t, err)
	require.NotNil(t, rw.resolve)
	assert.Equal(t, orders.String(), rw.resolve(ordersURN))

	rw.err = errors.New("rewrite failed")
	report, err := New(cat, memMapping{}, Options{}).Run(context.Background(), src, rw)
	require.ErrorContains(t, err, "rewrite failed")
	assert.NotNil(t, report, "the import's report is kept when the rewrite fails")
}

type spyRewrite struct {
	resolve Resolver
	err     error
}

func (s *spyRewrite) Rewrite(_ context.Context, resolve Resolver, _ *Report) error {
	s.resolve = resolve
	return s.err
}

func TestReportBoundsItsList(t *testing.T) {
	r := newReport()
	for range maxListed + 10 {
		r.skip("u", "why", "")
	}
	assert.Len(t, r.Skipped, maxListed)
	assert.Equal(t, maxListed+10, r.SkippedByReason["why"])
}

// A dataset only a removed record names takes that record's curation.
func TestImportFromOnlyARemovedRecord(t *testing.T) {
	fx := fixture()
	cat, orders, src := fx.cat, fx.orders, fx.src
	src.heads = []DatasetHead{{URN: ordersRemovedURN, Removed: true}}
	report, err := New(cat, memMapping{}, Options{}).Run(context.Background(), src, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Datasets.FromRemoved)
	assert.Equal(t, "An old description the live record replaces.", cat.Aspects(orders).Description)
}

// Glossary nodes whose parents form a cycle are imported without a parent
// rather than not at all.
func TestImportBreaksAParentCycle(t *testing.T) {
	fx := fixture()
	cat, src := fx.cat, fx.src
	src.heads = nil
	src.defs = []Definition{
		{URN: "urn:li:glossaryNode:a", Kind: catalog.KindGlossaryNode, Name: "A", Parent: "urn:li:glossaryNode:b"},
		{URN: "urn:li:glossaryNode:b", Kind: catalog.KindGlossaryNode, Name: "B", Parent: "urn:li:glossaryNode:a"},
	}
	report, err := New(cat, memMapping{}, Options{}).Run(context.Background(), src, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Definitions["glossary_node"])
}

// A mapping that cannot be written stops the import.
func TestImportStopsWhenTheMappingCannotBeWritten(t *testing.T) {
	fx := fixture()
	cat, src := fx.cat, fx.src
	_, err := New(cat, recordFails{memMapping{}}, Options{}).Run(context.Background(), src, nil)
	assert.ErrorIs(t, err, errBoom)
}

type recordFails struct{ memMapping }

func (recordFails) Record(context.Context, string, string) error { return errBoom }

func TestUnion(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, union([]string{"a", "b"}, []string{"b", "c", "a"}))
	assert.Nil(t, union(nil, nil))
}

func TestIsZeroAndFill(t *testing.T) {
	assert.True(t, isZero(catalog.Aspects{}))
	assert.False(t, hasCuration(&DatasetRecord{Columns: map[string]catalog.Aspects{"a": {}}}))
	assert.True(t, hasCuration(&DatasetRecord{Columns: map[string]catalog.Aspects{"a": {Description: "x"}}}))
	dst := catalog.Aspects{Description: "kept"}
	assert.True(t, fill(&dst, catalog.Aspects{Description: "not taken", Tags: []string{"t"}}))
	assert.Equal(t, catalog.Aspects{Description: "kept", Tags: []string{"t"}}, dst)
	assert.False(t, fill(&dst, catalog.Aspects{Description: "x"}))
}
