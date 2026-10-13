package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
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

// A dataset whose schema document is kept links it as a resource reference
// the caller can fetch in turn (#1918).
func TestNativeCatalogFetchLinksTheSchemaDocument(t *testing.T) {
	f := nativeFixture()
	f.datasets[nativeOrders.String()].SchemaResourceID = "res-1"
	doc, _, err := NewNativeCatalogProvider(f, nil).Fetch(context.Background(), nativeOrders.String(), Caller{})
	require.NoError(t, err)
	assert.Equal(t, []DocumentRef{{Reference: "mcp:resource:res-1", Type: "resource"}}, doc.References)
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
