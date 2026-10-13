package tableregister

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var errCatalog = errors.New("catalog down")

type fakeCatalog struct {
	datasets []catalog.DatasetInput
	edges    []catalog.Edge
	removed  []string
	putErr   error
	edgeErr  error
	markErr  error
}

func (f *fakeCatalog) MarkDatasetRemoved(_ context.Context, ref catalog.Ref) error {
	f.removed = append(f.removed, ref.String())
	return f.markErr
}

func (f *fakeCatalog) PutDataset(_ context.Context, in catalog.DatasetInput) (*catalog.Dataset, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	f.datasets = append(f.datasets, in)
	return &catalog.Dataset{Reference: in.Ref.String()}, nil
}

func (f *fakeCatalog) AddEdges(_ context.Context, e []catalog.Edge) error {
	f.edges = append(f.edges, e...)
	return f.edgeErr
}

func catalogReg(kind, id string) Registration {
	return Registration{
		ID: "reg-" + id, SourceKind: kind, SourceID: id, Connection: "Warehouse",
		Catalog: "scratch", Schema: "uploads", Table: "t_" + id, RegisteredBy: "ana@example.com",
		Columns: []Column{{Name: "a", Type: "varchar"}},
	}
}

func TestCatalogingStoreCatalogsARegisteredTable(t *testing.T) {
	ctx := context.Background()
	cat := &fakeCatalog{}
	s := NewCatalogingStore(newMemStore(), cat, func(string) string { return "warehouse" })

	require.NoError(t, s.Insert(ctx, catalogReg(KindResource, "r1")))
	require.Len(t, cat.datasets, 1)
	d := cat.datasets[0]
	assert.Equal(t, "mcp:dataset:(trino,warehouse,scratch.uploads.t_r1)", d.Ref.String())
	assert.Equal(t, catalog.DatasetRegisteredTable, d.Type)
	assert.Equal(t, "r1", d.SchemaResourceID)
	assert.Equal(t, []catalog.ColumnInput{{Name: "a", DataType: "varchar", Nullable: true}}, d.Columns)
	require.Len(t, cat.edges, 1)
	assert.Equal(t, catalog.Edge{
		Source: "mcp:resource:r1", Target: d.Ref.String(), WriterKind: catalog.WriterRegistration,
		WriterRef: "reg-r1", WrittenBy: "ana@example.com",
	}, cat.edges[0])

	require.NoError(t, s.Insert(ctx, catalogReg(KindAsset, "a1")))
	assert.Equal(t, "mcp:asset:a1", cat.edges[1].Source)

	require.NoError(t, s.Insert(ctx, catalogReg(KindWebhook, "hook")))
	assert.Equal(t, catalog.DatasetWebhookTable, cat.datasets[2].Type)
	assert.Len(t, cat.edges, 2, "a webhook table reads many windows and names no one file")

	require.NoError(t, s.Relocate(ctx, "reg-r1", "s3://b/new/", "csv", []Column{{Name: "b", Type: "varchar"}}))
	require.Len(t, cat.datasets, 4)
	assert.Equal(t, "b", cat.datasets[3].Columns[0].Name, "a relocated table is cataloged with its new columns")
}

func TestCatalogingStoreFailures(t *testing.T) {
	ctx := context.Background()
	inner := newMemStore()
	inner.insertErr = errCatalog
	cat := &fakeCatalog{}
	s := NewCatalogingStore(inner, cat, nil)
	assert.ErrorIs(t, s.Insert(ctx, catalogReg(KindResource, "r1")), errCatalog)
	assert.Empty(t, cat.datasets, "a registration that was not recorded is not cataloged")

	inner.insertErr = nil
	cat.putErr = errCatalog
	require.NoError(t, s.Insert(ctx, catalogReg(KindResource, "r2")), "a catalog failure does not fail the registration")
	assert.Empty(t, cat.edges)

	cat.putErr, cat.edgeErr = nil, errCatalog
	require.NoError(t, s.Insert(ctx, catalogReg(KindResource, "r3")))
	assert.Equal(t, "mcp:dataset:(trino,Warehouse,scratch.uploads.t_r3)", cat.datasets[0].Ref.String(),
		"with no lookup the connection name is used as it stands")

	inner.relocateErr = errCatalog
	assert.ErrorIs(t, s.Relocate(ctx, "reg-r3", "x", "csv", nil), errCatalog)
	inner.relocateErr = nil
	lost := NewCatalogingStore(getFails{inner}, cat, nil)
	require.NoError(t, lost.Relocate(ctx, "reg-r3", "x", "csv", nil),
		"a relocation whose row cannot be read back is logged, not failed")
}

// getFails is a store whose reads fail after its writes succeed.
type getFails struct{ *memStore }

func (getFails) Get(context.Context, string) (*Registration, error) { return nil, errCatalog }

func TestCatalogingStoreMarksAnUnregisteredTableRemoved(t *testing.T) {
	ctx := context.Background()
	inner := newMemStore()
	cat := &fakeCatalog{}
	s := NewCatalogingStore(inner, cat, func(string) string { return "warehouse" })
	require.NoError(t, s.Insert(ctx, catalogReg(KindResource, "r1")))

	require.NoError(t, s.Delete(ctx, "reg-r1"))
	assert.Equal(t, []string{"mcp:dataset:(trino,warehouse,scratch.uploads.t_r1)"}, cat.removed)

	assert.Error(t, s.Delete(ctx, "reg-missing"), "the decorated store's refusal is returned")
	assert.Len(t, cat.removed, 1)

	require.NoError(t, s.Insert(ctx, catalogReg(KindResource, "r2")))
	cat.markErr = errCatalog
	require.NoError(t, s.Delete(ctx, "reg-r2"), "a catalog failure does not fail the unregistration")

	require.NoError(t, s.Insert(ctx, catalogReg(KindResource, "r3")))
	inner.deleteErr = errCatalog
	assert.ErrorIs(t, s.Delete(ctx, "reg-r3"), errCatalog)
}
