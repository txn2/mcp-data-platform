package trino

import (
	"context"
	"errors"
	"testing"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

func TestAdapterCatalogBrowser(t *testing.T) {
	mock := &mockTrinoClient{
		listCatalogsFunc: func(context.Context) ([]string, error) {
			return []string{"hive", "iceberg"}, nil
		},
		listSchemasFunc: func(_ context.Context, catalog string) ([]string, error) {
			if catalog != "hive" {
				t.Fatalf("unexpected catalog %q", catalog)
			}
			return []string{"sales", "ops"}, nil
		},
		listTablesFunc: func(_ context.Context, catalog, schema string) ([]trinoclient.TableInfo, error) {
			if catalog != "hive" || schema != "sales" {
				t.Fatalf("unexpected catalog/schema %q/%q", catalog, schema)
			}
			return []trinoclient.TableInfo{
				{Name: "orders", Type: "TABLE"},
				{Name: "customers", Type: "VIEW"},
			}, nil
		},
	}
	adapter, err := NewWithClient(Config{ConnectionName: "test"}, mock)
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}

	// The adapter must satisfy the query.CatalogBrowser capability, reachable
	// through the unwrap helper.
	browser, ok := query.CatalogBrowserFrom(adapter)
	if !ok {
		t.Fatal("CatalogBrowserFrom did not recognize the trino adapter")
	}

	ctx := context.Background()
	cats, err := browser.ListCatalogs(ctx)
	if err != nil || len(cats) != 2 || cats[0] != "hive" {
		t.Fatalf("ListCatalogs = %v, %v", cats, err)
	}
	schemas, err := browser.ListSchemas(ctx, "hive")
	if err != nil || len(schemas) != 2 || schemas[1] != "ops" {
		t.Fatalf("ListSchemas = %v, %v", schemas, err)
	}
	tables, err := browser.ListTables(ctx, "hive", "sales")
	if err != nil {
		t.Fatalf("ListTables error: %v", err)
	}
	if len(tables) != 2 || tables[0] != "orders" || tables[1] != "customers" {
		t.Fatalf("ListTables mapped names = %v, want [orders customers]", tables)
	}
}

func TestAdapterCatalogBrowserErrors(t *testing.T) {
	wantErr := errors.New("boom")
	mock := &mockTrinoClient{
		listCatalogsFunc: func(context.Context) ([]string, error) { return nil, wantErr },
		listSchemasFunc:  func(context.Context, string) ([]string, error) { return nil, wantErr },
		listTablesFunc:   func(context.Context, string, string) ([]trinoclient.TableInfo, error) { return nil, wantErr },
	}
	adapter, err := NewWithClient(Config{ConnectionName: "test"}, mock)
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	ctx := context.Background()
	if _, err := adapter.ListCatalogs(ctx); err == nil {
		t.Error("expected ListCatalogs error")
	}
	if _, err := adapter.ListSchemas(ctx, "hive"); err == nil {
		t.Error("expected ListSchemas error")
	}
	if _, err := adapter.ListTables(ctx, "hive", "sales"); err == nil {
		t.Error("expected ListTables error")
	}
}
