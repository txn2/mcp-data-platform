package semantic

import (
	"context"
	"testing"
)

// recordProvider embeds the noop provider and adds the two by-URN record reads
// (#1590), the shape the DataHub adapter has and the noop provider lacks.
type recordProvider struct {
	Provider
}

func (recordProvider) GetDataset(_ context.Context, table TableIdentifier) (*Dataset, error) {
	return &Dataset{Name: table.Table}, nil
}

func (recordProvider) GetDataProduct(_ context.Context, urn string) (*DataProduct, error) {
	return &DataProduct{URN: urn, Name: "Orders 360"}, nil
}

func TestDatasetReaderFrom(t *testing.T) {
	t.Run("bare provider", func(t *testing.T) {
		r, ok := DatasetReaderFrom(recordProvider{Provider: NewNoopProvider()})
		if !ok {
			t.Fatal("expected the dataset read capability")
		}
		ds, _ := r.GetDataset(context.Background(), TableIdentifier{Table: "orders"})
		if ds.Name != "orders" {
			t.Fatalf("unexpected record %+v", ds)
		}
	})
	t.Run("through caching decorator", func(t *testing.T) {
		cached := NewCachedProvider(recordProvider{Provider: NewNoopProvider()}, CacheConfig{})
		if _, ok := DatasetReaderFrom(cached); !ok {
			t.Fatal("expected the capability through the cache decorator")
		}
	})
	t.Run("provider without the read", func(t *testing.T) {
		if _, ok := DatasetReaderFrom(NewNoopProvider()); ok {
			t.Fatal("noop provider must not report the dataset read")
		}
	})
}

func TestDataProductReaderFrom(t *testing.T) {
	t.Run("bare provider", func(t *testing.T) {
		r, ok := DataProductReaderFrom(recordProvider{Provider: NewNoopProvider()})
		if !ok {
			t.Fatal("expected the data product read capability")
		}
		p, _ := r.GetDataProduct(context.Background(), "urn:li:dataProduct:x")
		if p.Name != "Orders 360" {
			t.Fatalf("unexpected product %+v", p)
		}
	})
	t.Run("through caching decorator", func(t *testing.T) {
		cached := NewCachedProvider(recordProvider{Provider: NewNoopProvider()}, CacheConfig{})
		if _, ok := DataProductReaderFrom(cached); !ok {
			t.Fatal("expected the capability through the cache decorator")
		}
	})
	t.Run("provider without the read", func(t *testing.T) {
		if _, ok := DataProductReaderFrom(NewNoopProvider()); ok {
			t.Fatal("noop provider must not report the data product read")
		}
	})
}
