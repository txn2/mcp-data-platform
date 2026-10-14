package trino

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

const (
	adapterTestTrino         = "trino"
	adapterTestUnexpectedErr = "unexpected error: %v"
	adapterTestRowCount100   = 100
	adapterTestRowCount200   = 200
	adapterTestRowCount50    = 50
	adapterTestWarehouse     = "warehouse"
	adapterTestCatalogRdbms  = "rdbms"
)

// mockTrinoClient implements the Client interface for testing.
type mockTrinoClient struct {
	queryFunc         func(ctx context.Context, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error)
	listCatalogsFunc  func(ctx context.Context) ([]string, error)
	listSchemasFunc   func(ctx context.Context, catalog string) ([]string, error)
	listTablesFunc    func(ctx context.Context, catalog, schema string) ([]trinoclient.TableInfo, error)
	describeTableFunc func(ctx context.Context, catalog, schema, table string) (*trinoclient.TableInfo, error)
	pingFunc          func(ctx context.Context) error
	closeFunc         func() error
}

func (m *mockTrinoClient) Query(ctx context.Context, sql string, opts trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, sql, opts)
	}
	return &trinoclient.QueryResult{}, nil
}

func (m *mockTrinoClient) ListCatalogs(ctx context.Context) ([]string, error) {
	if m.listCatalogsFunc != nil {
		return m.listCatalogsFunc(ctx)
	}
	return nil, nil
}

func (m *mockTrinoClient) ListSchemas(ctx context.Context, catalog string) ([]string, error) {
	if m.listSchemasFunc != nil {
		return m.listSchemasFunc(ctx, catalog)
	}
	return nil, nil
}

func (m *mockTrinoClient) ListTables(ctx context.Context, catalog, schema string) ([]trinoclient.TableInfo, error) {
	if m.listTablesFunc != nil {
		return m.listTablesFunc(ctx, catalog, schema)
	}
	return nil, nil
}

func (m *mockTrinoClient) DescribeTable(ctx context.Context, catalog, schema, table string) (*trinoclient.TableInfo, error) {
	if m.describeTableFunc != nil {
		return m.describeTableFunc(ctx, catalog, schema, table)
	}
	return &trinoclient.TableInfo{}, nil
}

func (m *mockTrinoClient) Ping(ctx context.Context) error {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return nil
}

func (m *mockTrinoClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestNewWithClient(t *testing.T) {
	t.Run("nil client returns error", func(t *testing.T) {
		_, err := NewWithClient(Config{}, nil)
		if err == nil {
			t.Error("expected error for nil client")
		}
	})

	t.Run("valid client", func(t *testing.T) {
		mock := &mockTrinoClient{}
		adapter, err := NewWithClient(Config{ConnectionName: "test"}, mock)
		if err != nil {
			t.Fatalf(adapterTestUnexpectedErr, err)
		}
		if adapter.Name() != adapterTestTrino {
			t.Errorf("expected name 'trino', got %q", adapter.Name())
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		mock := &mockTrinoClient{}
		adapter, err := NewWithClient(Config{}, mock)
		if err != nil {
			t.Fatalf(adapterTestUnexpectedErr, err)
		}
		if adapter.cfg.DefaultLimit != 1000 {
			t.Errorf("expected default limit 1000, got %d", adapter.cfg.DefaultLimit)
		}
		if adapter.cfg.MaxLimit != 10000 {
			t.Errorf("expected max limit 10000, got %d", adapter.cfg.MaxLimit)
		}
	})
}

func TestAdapterName(t *testing.T) {
	mock := &mockTrinoClient{}
	adapter, _ := NewWithClient(Config{}, mock)
	if adapter.Name() != adapterTestTrino {
		t.Errorf("expected 'trino', got %q", adapter.Name())
	}
}

func assertTableIdentifier(t *testing.T, result *query.TableIdentifier, wantCatalog, wantSchema, wantTable string) {
	t.Helper()
	if result.Catalog != wantCatalog {
		t.Errorf("expected catalog %q, got %q", wantCatalog, result.Catalog)
	}
	if result.Schema != wantSchema {
		t.Errorf("expected schema %q, got %q", wantSchema, result.Schema)
	}
	if result.Table != wantTable {
		t.Errorf("expected table %q, got %q", wantTable, result.Table)
	}
}

func TestGetTableAvailability_TableExists(t *testing.T) {
	ctx := context.Background()
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return &trinoclient.TableInfo{Name: "test_table"}, nil
		},
		queryFunc: func(_ context.Context, _ string, _ trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
			return &trinoclient.QueryResult{
				Rows: []map[string]any{{"_col0": int64(adapterTestRowCount100)}},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "test", EstimateRowCounts: true}, mock)

	result, err := adapter.GetTableAvailability(ctx, "mcp:dataset:(trino,test,default.schema.table)")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if !result.Available {
		t.Error("expected Available to be true")
	}
	if result.EstimatedRows == nil || *result.EstimatedRows != adapterTestRowCount100 {
		t.Error("expected EstimatedRows to be 100")
	}
}

func TestGetTableAvailability_FloatCount(t *testing.T) {
	ctx := context.Background()
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return &trinoclient.TableInfo{Name: "test_table"}, nil
		},
		queryFunc: func(_ context.Context, _ string, _ trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
			return &trinoclient.QueryResult{
				Rows: []map[string]any{{"_col0": float64(adapterTestRowCount200)}},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "test", EstimateRowCounts: true}, mock)

	result, err := adapter.GetTableAvailability(ctx, "mcp:dataset:(trino,test,default.schema.table)")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if result.EstimatedRows == nil || *result.EstimatedRows != adapterTestRowCount200 {
		t.Error("expected EstimatedRows to be 200")
	}
}

func TestGetTableAvailability_RowCountsDisabled(t *testing.T) {
	ctx := context.Background()
	queryCalled := false
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return &trinoclient.TableInfo{Name: "test_table"}, nil
		},
		queryFunc: func(_ context.Context, _ string, _ trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
			queryCalled = true
			return &trinoclient.QueryResult{
				Rows: []map[string]any{{"_col0": int64(adapterTestRowCount100)}},
			}, nil
		},
	}
	// EstimateRowCounts defaults to false (zero value)
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "test"}, mock)

	result, err := adapter.GetTableAvailability(ctx, "mcp:dataset:(trino,test,default.schema.table)")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if !result.Available {
		t.Error("expected Available to be true")
	}
	if result.EstimatedRows != nil {
		t.Errorf("expected EstimatedRows to be nil when row counts disabled, got %d", *result.EstimatedRows)
	}
	if queryCalled {
		t.Error("expected Query to NOT be called when EstimateRowCounts is false")
	}
}

// ResolveLocation answers where an entity is queryable without ever measuring
// it, even on an adapter configured to estimate row counts. A request-path
// caller that reads only the table and connection would otherwise pay for a
// COUNT(*) full scan per entity and throw the number away (#1220).
func TestResolveLocation_NeverCounts(t *testing.T) {
	ctx := context.Background()
	queryCalled := false
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return &trinoclient.TableInfo{Name: "test_table"}, nil
		},
		queryFunc: func(_ context.Context, _ string, _ trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
			queryCalled = true
			return &trinoclient.QueryResult{
				Rows: []map[string]any{{"_col0": int64(adapterTestRowCount100)}},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "test", EstimateRowCounts: true}, mock)

	result, err := adapter.ResolveLocation(ctx, "mcp:dataset:(trino,test,default.schema.table)")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if !result.Available || result.QueryTable == "" || result.Connection != "test" {
		t.Errorf("expected a located table, got %+v", result)
	}
	if result.EstimatedRows != nil {
		t.Errorf("expected no row estimate, got %d", *result.EstimatedRows)
	}
	if queryCalled {
		t.Error("ResolveLocation must not run COUNT(*), even with EstimateRowCounts enabled")
	}
}

// A table the adapter cannot resolve is an unavailable answer on this path too,
// not an error — the contract tableavail relies on to tell "not available" from
// "no answer".
func TestResolveLocation_NotExists(t *testing.T) {
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return nil, errors.New("table not found")
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "test"}, mock)

	result, err := adapter.ResolveLocation(context.Background(),
		"mcp:dataset:(trino,test,default.schema.gone)")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if result.Available || result.Error == "" {
		t.Errorf("expected an unavailable answer carrying the reason, got %+v", result)
	}
}

func TestGetTableAvailability_NotExists(t *testing.T) {
	ctx := context.Background()
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return nil, errors.New("table not found")
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "hive"}, mock)

	result, err := adapter.GetTableAvailability(ctx, "mcp:dataset:(trino,test,default.schema.table)")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if result.Available {
		t.Error("expected Available to be false")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
}

func TestGetTableAvailability_InvalidURN(t *testing.T) {
	ctx := context.Background()
	mock := &mockTrinoClient{}
	adapter, _ := NewWithClient(Config{}, mock)

	result, err := adapter.GetTableAvailability(ctx, "invalid-urn")
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if result.Available {
		t.Error("expected Available to be false")
	}
}

func TestGetQueryExamples(t *testing.T) {
	mock := &mockTrinoClient{}
	adapter, _ := NewWithClient(Config{Catalog: "hive"}, mock)
	ctx := context.Background()

	t.Run("valid URN", func(t *testing.T) {
		examples, err := adapter.GetQueryExamples(ctx, "mcp:dataset:(trino,test,default.myschema.mytable)")
		if err != nil {
			t.Fatalf(adapterTestUnexpectedErr, err)
		}
		if len(examples) != 3 {
			t.Errorf("expected 3 examples, got %d", len(examples))
		}
	})

	t.Run("invalid URN", func(t *testing.T) {
		_, err := adapter.GetQueryExamples(ctx, "invalid-urn")
		if err == nil {
			t.Error("expected error for invalid URN")
		}
	})
}

func TestGetExecutionContext(t *testing.T) {
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return &trinoclient.TableInfo{Name: "test"}, nil
		},
		queryFunc: func(_ context.Context, _ string, _ trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
			return &trinoclient.QueryResult{
				Rows: []map[string]any{{"_col0": int64(adapterTestRowCount50)}},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "main", EstimateRowCounts: true}, mock)
	ctx := context.Background()

	urns := []string{
		"mcp:dataset:(trino,test,default.schema1.table1)",
		"mcp:dataset:(trino,test,default.schema2.table2)",
		"invalid-urn", // Should be skipped
	}

	result, err := adapter.GetExecutionContext(ctx, urns)
	if err != nil {
		t.Fatalf(adapterTestUnexpectedErr, err)
	}
	if len(result.Tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(result.Tables))
	}
	if len(result.Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(result.Connections))
	}
}

func TestGetTableSchema(t *testing.T) {
	mock := &mockTrinoClient{
		describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
			return &trinoclient.TableInfo{
				Name: "test_table",
				Columns: []trinoclient.ColumnDef{
					{Name: "id", Type: "bigint", Nullable: "NOT NULL", Comment: "Primary key"},
					{Name: "name", Type: "varchar", Nullable: "", Comment: ""},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Catalog: "default_catalog", Schema: "default_schema"}, mock)
	ctx := context.Background()

	t.Run("with explicit catalog and schema", func(t *testing.T) {
		schema, err := adapter.GetTableSchema(ctx, query.TableIdentifier{
			Catalog: "hive",
			Schema:  "analytics",
			Table:   "users",
		})
		if err != nil {
			t.Fatalf(adapterTestUnexpectedErr, err)
		}
		if len(schema.Columns) != 2 {
			t.Errorf("expected 2 columns, got %d", len(schema.Columns))
		}
		if schema.Columns[0].Nullable {
			t.Error("expected first column to be NOT NULL")
		}
		if !schema.Columns[1].Nullable {
			t.Error("expected second column to be nullable")
		}
	})

	t.Run("using defaults", func(t *testing.T) {
		schema, err := adapter.GetTableSchema(ctx, query.TableIdentifier{
			Table: "users",
		})
		if err != nil {
			t.Fatalf(adapterTestUnexpectedErr, err)
		}
		if len(schema.Columns) != 2 {
			t.Errorf("expected 2 columns, got %d", len(schema.Columns))
		}
	})

	t.Run("describe error", func(t *testing.T) {
		errorMock := &mockTrinoClient{
			describeTableFunc: func(_ context.Context, _, _, _ string) (*trinoclient.TableInfo, error) {
				return nil, errors.New("table not found")
			},
		}
		errorAdapter, _ := NewWithClient(Config{Catalog: "hive"}, errorMock)

		_, err := errorAdapter.GetTableSchema(ctx, query.TableIdentifier{Table: "missing"})
		if err == nil {
			t.Error("expected error")
		}
	})
}

func TestAdapterClose(t *testing.T) {
	t.Run("close with client", func(t *testing.T) {
		closed := false
		mock := &mockTrinoClient{
			closeFunc: func() error {
				closed = true
				return nil
			},
		}
		adapter, _ := NewWithClient(Config{}, mock)
		err := adapter.Close()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !closed {
			t.Error("expected client to be closed")
		}
	})
}

func TestAdapterPing(t *testing.T) {
	t.Run("ping success", func(t *testing.T) {
		mock := &mockTrinoClient{
			pingFunc: func(_ context.Context) error {
				return nil
			},
		}
		adapter, _ := NewWithClient(Config{}, mock)
		err := adapter.Ping(context.Background())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ping failure", func(t *testing.T) {
		mock := &mockTrinoClient{
			pingFunc: func(_ context.Context) error {
				return errors.New("connection refused")
			},
		}
		adapter, _ := NewWithClient(Config{}, mock)
		err := adapter.Ping(context.Background())
		if err == nil {
			t.Error("expected error")
		}
	})
}

func TestNew(t *testing.T) {
	t.Run("missing host returns error", func(t *testing.T) {
		_, err := New(Config{User: "test"})
		if err == nil {
			t.Error("expected error for missing host")
		}
	})

	t.Run("missing user returns error", func(t *testing.T) {
		_, err := New(Config{Host: "localhost"})
		if err == nil {
			t.Error("expected error for missing user")
		}
	})
}

func TestAdapterCloseNilClient(t *testing.T) {
	adapter := &Adapter{
		client: nil,
	}
	err := adapter.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Verify Adapter implements Provider interface.
var _ query.Provider = (*Adapter)(nil)

// TestResolveTableCatalogReference resolves a platform catalog reference
// (#1919) to the table and connection it names, with no mapping applied.
func TestResolveTableCatalogReference(t *testing.T) {
	adapter, _ := NewWithClient(Config{Catalog: "hive", ConnectionName: "test-conn"}, &mockTrinoClient{})
	got, err := adapter.ResolveTable(context.Background(), "mcp:dataset:(trino,warehouse,x.sales.orders)")
	require.NoError(t, err)
	assert.Equal(t, query.TableIdentifier{Catalog: "x", Schema: "sales", Table: "orders", Connection: "warehouse"}, *got)

	for _, ref := range []string{
		"mcp:dataset:(api,stripe,listCharges)", "mcp:dataset:(trino,w,a.b)",
		"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)", "mcp:glossary_term:revenue",
	} {
		_, err = adapter.ResolveTable(context.Background(), ref)
		assert.Error(t, err, ref)
	}
}
