package knowledge

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

type fakeCatalog struct {
	datasets    map[string]*catalog.Dataset
	definitions map[string]*catalog.Definition
	err         error
}

func (f *fakeCatalog) GetDataset(_ context.Context, ref catalog.Ref) (*catalog.Dataset, error) {
	if f.err != nil {
		return nil, f.err
	}
	d, ok := f.datasets[ref.String()]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	return d, nil
}

func (f *fakeCatalog) GetDefinition(_ context.Context, ref catalog.Ref) (*catalog.Definition, error) {
	if f.err != nil {
		return nil, f.err
	}
	d, ok := f.definitions[ref.String()]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	return d, nil
}

var nativeOrders = catalog.DatasetRef("trino", "warehouse", "hive", "sales", "orders")

func nativeFixture() *fakeCatalog {
	return &fakeCatalog{
		datasets: map[string]*catalog.Dataset{
			nativeOrders.String(): {
				Reference: nativeOrders.String(), Name: "orders",
				Aspects: catalog.Aspects{Description: "Storefront orders."},
				Columns: []catalog.Column{{Name: "amount", Aspects: catalog.Aspects{Description: "USD total."}}},
			},
		},
		definitions: map[string]*catalog.Definition{
			"mcp:glossary_term:revenue": {
				Reference: "mcp:glossary_term:t1", Name: "Revenue",
				Aspects: catalog.Aspects{Description: "Money in."},
			},
		},
	}
}

func TestNativeCatalogFetchDataset(t *testing.T) {
	p := NewNativeCatalogProvider(nativeFixture(), nil)
	doc, owned, err := p.Fetch(context.Background(), nativeOrders.String(), Caller{})
	require.True(t, owned)
	require.NoError(t, err)
	assert.Equal(t, SourceCatalog, doc.Source)
	assert.Equal(t, "orders", doc.Title)
	assert.Equal(t, "Storefront orders.", doc.Body)
	assert.Equal(t, []string{nativeOrders.String()}, doc.EntityURNs)
	d, ok := doc.Content.(datasetDocument)
	require.True(t, ok)
	assert.Equal(t, "amount", d.Columns[0].Name)
	assert.NotNil(t, d.Upstream, "a store that cannot walk lineage reports none, as []")
	assert.Empty(t, doc.References, "a dataset with no schema document links none")
}

// A dataset links the schema document a scan read it from, and the resource a
// registered table was created from, as references the caller can fetch in
// turn (#1918).
func TestNativeCatalogFetchLinksTheSourceDocuments(t *testing.T) {
	f := nativeFixture()
	f.datasets[nativeOrders.String()].SchemaDocument = billingSpec.String()
	f.datasets[nativeOrders.String()].SchemaResourceID = "res-1"
	doc, _, err := NewNativeCatalogProvider(f, nil).Fetch(context.Background(), nativeOrders.String(), Caller{})
	require.NoError(t, err)
	assert.Equal(t, []DocumentRef{
		{Reference: billingSpec.String(), Type: "catalog"},
		{Reference: "mcp:resource:res-1", Type: "resource"},
	}, doc.References)
}

var billingSpec = catalog.SchemaDocumentRef("api", "billing", "openapi.json")

// documentCatalog adds schema documents to the fake catalog.
type documentCatalog struct {
	*fakeCatalog
	docs map[string]*catalog.SchemaDocument
	err  error
}

func (d documentCatalog) GetDocument(_ context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error) {
	if d.err != nil {
		return nil, d.err
	}
	doc, ok := d.docs[ref.String()]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	return doc, nil
}

func specCatalog(content []byte) documentCatalog {
	spec := &catalog.SchemaDocument{
		Reference: billingSpec.String(), ConnectionKind: "api", Connection: "billing",
		Filename: "openapi.json", MIMEType: "application/json", Content: content, SizeBytes: len(content),
	}
	return documentCatalog{fakeCatalog: nativeFixture(), docs: map[string]*catalog.SchemaDocument{billingSpec.String(): spec}}
}

// A schema document is read only by a caller whose persona may reach its
// connection, the boundary its datasets are read under (#1918).
func TestNativeCatalogFetchSchemaDocument(t *testing.T) {
	p := NewNativeCatalogProvider(specCatalog([]byte(`{"openapi":"3.0.0"}`)), nil)
	allowed, _ := scopedCaller(stubScope{allowed: map[string]bool{"billing": true}})
	doc, owned, err := p.Fetch(context.Background(), billingSpec.String(), allowed)
	require.True(t, owned)
	require.NoError(t, err)
	assert.Equal(t, `{"openapi":"3.0.0"}`, doc.Body)
	assert.Equal(t, "billing openapi.json", doc.Title)
	assert.Equal(t, SourceCatalog, doc.Source)
	assert.Empty(t, doc.Note, "a whole document carries no note")

	denied, _ := scopedCaller(stubScope{allowed: map[string]bool{"warehouse": true}})
	_, owned, err = p.Fetch(context.Background(), billingSpec.String(), denied)
	assert.True(t, owned)
	require.ErrorIs(t, err, ErrNotFound, "a persona without the connection does not learn the document exists")

	_, _, err = p.Fetch(context.Background(), catalog.SchemaDocumentRef("api", "billing", "absent.json").String(), allowed)
	require.ErrorIs(t, err, ErrNotFound)

	broken := specCatalog(nil)
	broken.err = errors.New("db down")
	_, _, err = NewNativeCatalogProvider(broken, nil).Fetch(context.Background(), billingSpec.String(), allowed)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound)

	_, owned, err = NewNativeCatalogProvider(nativeFixture(), nil).Fetch(context.Background(), billingSpec.String(), allowed)
	assert.True(t, owned)
	require.ErrorIs(t, err, ErrNotFound, "a store that keeps no documents has none to read")
}

// A document past the inline limit is cut at a character boundary and says so.
func TestNativeCatalogFetchCutsALongSchemaDocument(t *testing.T) {
	// A three-byte character straddles the limit, so the cut backs up to it.
	content := append(bytes.Repeat([]byte("a"), resource.MaxInlineContentBytes), "ab"...)
	copy(content[resource.MaxInlineContentBytes-1:], "€")
	doc, _, err := NewNativeCatalogProvider(specCatalog(content), nil).Fetch(context.Background(), billingSpec.String(), Caller{})
	require.NoError(t, err)
	assert.Len(t, doc.Body, resource.MaxInlineContentBytes-1)
	assert.True(t, utf8.ValidString(doc.Body))
	assert.Contains(t, doc.Note, "Only the first 1048575 of this document's 1048578 bytes")
}

func TestRuneCut(t *testing.T) {
	assert.Equal(t, 3, runeCut([]byte("abcd"), 3))
	assert.Equal(t, 1, runeCut([]byte("a€b"), 2), "backs up to the start of the character")
	invalid := bytes.Repeat([]byte{0x80}, 10)
	assert.Equal(t, 8, runeCut(invalid, 8), "bytes with no character start are cut where asked")
}

type lineageCatalog struct {
	*fakeCatalog
	hops map[catalog.Direction][]catalog.Hop
	err  error
}

func (l lineageCatalog) Lineage(_ context.Context, _ string, dir catalog.Direction, depth int) ([]catalog.Hop, error) {
	if depth != lineageDepth {
		return nil, errors.New("unexpected depth")
	}
	return l.hops[dir], l.err
}

func TestNativeCatalogFetchCarriesLineage(t *testing.T) {
	up := []catalog.Hop{{Edge: catalog.Edge{Source: "mcp:resource:r1", Target: nativeOrders.String()}, Depth: 1}}
	store := lineageCatalog{fakeCatalog: nativeFixture(), hops: map[catalog.Direction][]catalog.Hop{catalog.Upstream: up, catalog.Downstream: {}}}
	doc, _, err := NewNativeCatalogProvider(store, nil).Fetch(context.Background(), nativeOrders.String(), Caller{})
	require.NoError(t, err)
	d, ok := doc.Content.(datasetDocument)
	require.True(t, ok)
	assert.Equal(t, up, d.Upstream)
	assert.Empty(t, d.Downstream)

	store.err = errors.New("boom")
	_, _, err = NewNativeCatalogProvider(store, nil).Fetch(context.Background(), nativeOrders.String(), Caller{})
	assert.Error(t, err)
}

func TestNativeCatalogFetchColumn(t *testing.T) {
	p := NewNativeCatalogProvider(nativeFixture(), nil)
	ref := catalog.ColumnRef(nativeOrders, "amount").String()
	doc, owned, err := p.Fetch(context.Background(), ref, Caller{})
	require.True(t, owned)
	require.NoError(t, err)
	assert.Equal(t, ref, doc.Reference)
	assert.Equal(t, "orders.amount", doc.Title)
	assert.Equal(t, "USD total.", doc.Body)
	cd, ok := doc.Content.(columnDocument)
	require.True(t, ok)
	assert.Equal(t, nativeOrders.String(), cd.Dataset)

	_, owned, err = p.Fetch(context.Background(), catalog.ColumnRef(nativeOrders, "absent").String(), Caller{})
	assert.True(t, owned)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestNativeCatalogFetchDefinition(t *testing.T) {
	p := NewNativeCatalogProvider(nativeFixture(), nil)
	doc, owned, err := p.Fetch(context.Background(), "mcp:glossary_term:revenue", Caller{})
	require.True(t, owned)
	require.NoError(t, err)
	assert.Equal(t, SourceGovernance, doc.Source)
	assert.Equal(t, "mcp:glossary_term:t1", doc.Reference)
	assert.Equal(t, "Money in.", doc.Body)

	// A definition is organization-wide: the connection boundary does not apply.
	denied, _ := scopedCaller(stubScope{allowed: map[string]bool{}})
	_, _, err = p.Fetch(context.Background(), "mcp:glossary_term:revenue", denied)
	assert.NoError(t, err)
}

func TestNativeCatalogDeclinesOtherReferences(t *testing.T) {
	p := NewNativeCatalogProvider(nativeFixture(), nil)
	for _, ref := range []string{
		"mcp:asset:1", "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)", "mcp:dataset:(broken", "",
	} {
		_, owned, err := p.Fetch(context.Background(), ref, Caller{})
		assert.False(t, owned, ref)
		assert.NoError(t, err, ref)
	}
}

func TestNativeCatalogAppliesTheConnectionBoundary(t *testing.T) {
	var asked [2]string
	bound := func(kind, instance string) string {
		asked = [2]string{kind, instance}
		return "warehouse-bound"
	}
	p := NewNativeCatalogProvider(nativeFixture(), bound)

	allowed, _ := scopedCaller(stubScope{allowed: map[string]bool{"warehouse-bound": true}})
	_, _, err := p.Fetch(context.Background(), nativeOrders.String(), allowed)
	require.NoError(t, err)
	assert.Equal(t, [2]string{"trino", "warehouse"}, asked)

	// The instance name alone is not what persona rules match.
	instanceOnly, _ := scopedCaller(stubScope{allowed: map[string]bool{"warehouse": true}})
	for _, ref := range []string{nativeOrders.String(), catalog.ColumnRef(nativeOrders, "amount").String()} {
		_, owned, err := p.Fetch(context.Background(), ref, instanceOnly)
		assert.True(t, owned)
		assert.ErrorIs(t, err, ErrNotFound, ref)
	}
}

func TestNativeCatalogNilBoundUsesTheInstanceName(t *testing.T) {
	p := NewNativeCatalogProvider(nativeFixture(), nil)
	caller, _ := scopedCaller(stubScope{allowed: map[string]bool{"warehouse": true}})
	_, _, err := p.Fetch(context.Background(), nativeOrders.String(), caller)
	assert.NoError(t, err)
}

func TestNativeCatalogStoreErrors(t *testing.T) {
	p := NewNativeCatalogProvider(&fakeCatalog{}, nil)
	_, owned, err := p.Fetch(context.Background(), nativeOrders.String(), Caller{})
	assert.True(t, owned)
	assert.ErrorIs(t, err, ErrNotFound)
	_, _, err = p.Fetch(context.Background(), "mcp:domain:absent", Caller{})
	assert.ErrorIs(t, err, ErrNotFound)

	boom := errors.New("boom")
	p = NewNativeCatalogProvider(&fakeCatalog{err: boom}, nil)
	_, _, err = p.Fetch(context.Background(), nativeOrders.String(), Caller{})
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrNotFound)
	_, _, err = p.Fetch(context.Background(), "mcp:tag:pii", Caller{})
	assert.ErrorIs(t, err, boom)
}

func TestNativeCatalogThroughTheRouter(t *testing.T) {
	r := NewRouter(nil, nil, NewNativeCatalogProvider(nativeFixture(), nil))
	doc, err := r.Fetch(context.Background(), nativeOrders.String(), Caller{})
	require.NoError(t, err)
	assert.Equal(t, "orders", doc.Title)

	hits, err := NewNativeCatalogProvider(nativeFixture(), nil).Search(context.Background(), Query{Intent: "orders"})
	assert.NoError(t, err)
	assert.Empty(t, hits)
	assert.Equal(t, ScopeShared, NewNativeCatalogProvider(nil, nil).Scope())
	assert.Equal(t, SourceCatalog, NewNativeCatalogProvider(nil, nil).Name())
}
