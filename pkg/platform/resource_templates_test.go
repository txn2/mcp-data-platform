package platform

import (
	"context"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestParseTemplateVars(t *testing.T) {
	tests := []struct {
		name     string
		template string
		uri      string
		want     map[string]string
		wantErr  bool
	}{
		{
			name:     "schema URI",
			template: schemaTemplateURI,
			uri:      "schema://rdbms.public/transactions",
			want:     map[string]string{"catalog": "rdbms", "schema_name": "public", "table": "transactions"},
		},
		{
			name:     "glossary URI",
			template: glossaryTemplateURI,
			uri:      "glossary://revenue",
			want:     map[string]string{"term": "revenue"},
		},
		{
			name:     "availability URI",
			template: availabilityTemplateURI,
			uri:      "availability://warehouse.analytics/orders",
			want:     map[string]string{"catalog": "warehouse", "schema_name": "analytics", "table": "orders"},
		},
		{
			name:     "mismatch URI",
			template: schemaTemplateURI,
			uri:      "glossary://revenue",
			wantErr:  true,
		},
		{
			name:     "empty URI",
			template: schemaTemplateURI,
			uri:      "",
			wantErr:  true,
		},
		{
			name:     "invalid template",
			template: "{{{bad",
			uri:      "anything",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTemplateVars(tt.template, tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseTemplateVars() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("parseTemplateVars()[%q] = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

// mockSemanticProvider implements semantic.Provider for testing.
type mockSemanticProvider struct {
	tableCtx    *semantic.TableContext
	tableErr    error
	colsCtx     map[string]*semantic.ColumnContext
	colsErr     error
	glossary    *semantic.GlossaryTerm
	glossaryErr error
}

func (*mockSemanticProvider) Name() string { return "mock" }

func (m *mockSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return m.tableCtx, m.tableErr
}

func (*mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return m.colsCtx, m.colsErr
}

func (*mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return m.glossary, m.glossaryErr
}

func (*mockSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (*mockSemanticProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (*mockSemanticProvider) Close() error { return nil }

// mockQueryProvider implements query.Provider for testing.
type mockQueryProvider struct {
	schema    *query.TableSchema
	schemaErr error
	avail     *query.TableAvailability
	availErr  error
}

func (*mockQueryProvider) Name() string { return "mock" }

func (*mockQueryProvider) ResolveTable(_ context.Context, _ string) (*query.TableIdentifier, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockQueryProvider) GetTableAvailability(_ context.Context, _ string) (*query.TableAvailability, error) {
	return m.avail, m.availErr
}

func (*mockQueryProvider) GetQueryExamples(_ context.Context, _ string) ([]query.Example, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (*mockQueryProvider) GetExecutionContext(_ context.Context, _ []string) (*query.ExecutionContext, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockQueryProvider) GetTableSchema(_ context.Context, _ query.TableIdentifier) (*query.TableSchema, error) {
	return m.schema, m.schemaErr
}

func (*mockQueryProvider) Close() error { return nil }

func TestHandleSchemaResource(t *testing.T) {
	t.Run("both providers", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			queryProvider: &mockQueryProvider{
				schema: &query.TableSchema{
					Columns: []query.Column{
						{Name: "id", Type: "INTEGER"},
						{Name: "name", Type: "VARCHAR"},
					},
				},
			},
			semanticProvider: &mockSemanticProvider{
				tableCtx: &semantic.TableContext{
					Description: "Test table",
				},
				colsCtx: map[string]*semantic.ColumnContext{
					"id": {Name: "id", Description: "Primary key"},
				},
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/transactions"},
		}
		result, err := p.handleSchemaResource(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
		if result.Contents[0].Text == "" {
			t.Error("expected non-empty content")
		}
	})

	t.Run("query only", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			queryProvider: &mockQueryProvider{
				schema: &query.TableSchema{
					Columns: []query.Column{{Name: "id", Type: "INTEGER"}},
				},
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/transactions"},
		}
		result, err := p.handleSchemaResource(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("semantic only", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			semanticProvider: &mockSemanticProvider{
				tableCtx: &semantic.TableContext{Description: "Test table"},
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/transactions"},
		}
		result, err := p.handleSchemaResource(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("not found", func(t *testing.T) {
		p := &Platform{
			config:           &Config{},
			queryProvider:    &mockQueryProvider{schemaErr: fmt.Errorf("not found")},
			semanticProvider: &mockSemanticProvider{tableErr: fmt.Errorf("not found")},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/missing"},
		}
		_, err := p.handleSchemaResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for not found table")
		}
	})

	t.Run("invalid URI", func(t *testing.T) {
		p := &Platform{config: &Config{}}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://something"},
		}
		_, err := p.handleSchemaResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for invalid URI")
		}
	})
}

func TestHandleGlossaryResource(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			semanticProvider: &mockSemanticProvider{
				glossary: &semantic.GlossaryTerm{
					URN:         "urn:li:glossaryTerm:revenue",
					Name:        "revenue",
					Description: "Total income",
				},
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://revenue"},
		}
		result, err := p.handleGlossaryResource(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("not found", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			semanticProvider: &mockSemanticProvider{
				glossaryErr: fmt.Errorf("not found"),
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://missing"},
		}
		_, err := p.handleGlossaryResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing term")
		}
	})

	t.Run("no semantic provider", func(t *testing.T) {
		p := &Platform{config: &Config{}}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://revenue"},
		}
		_, err := p.handleGlossaryResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error when no semantic provider")
		}
	})

	t.Run("invalid URI", func(t *testing.T) {
		p := &Platform{config: &Config{}}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://bad"},
		}
		_, err := p.handleGlossaryResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for invalid URI")
		}
	})
}

func TestHandleAvailabilityResource(t *testing.T) {
	estRows := int64(1000)

	t.Run("available", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			queryProvider: &mockQueryProvider{
				avail: &query.TableAvailability{
					Available:     true,
					QueryTable:    "rdbms.public.transactions",
					Connection:    "default",
					EstimatedRows: &estRows,
				},
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/transactions"},
		}
		result, err := p.handleAvailabilityResource(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("not available", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			queryProvider: &mockQueryProvider{
				avail: &query.TableAvailability{
					Available: false,
					Error:     "table does not exist",
				},
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/missing"},
		}
		result, err := p.handleAvailabilityResource(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("provider error", func(t *testing.T) {
		p := &Platform{
			config: &Config{},
			queryProvider: &mockQueryProvider{
				availErr: fmt.Errorf("connection failed"),
			},
		}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/transactions"},
		}
		_, err := p.handleAvailabilityResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on provider failure")
		}
	})

	t.Run("no query provider", func(t *testing.T) {
		p := &Platform{config: &Config{}}

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/transactions"},
		}
		_, err := p.handleAvailabilityResource(context.Background(), req)
		if err == nil {
			t.Fatal("expected error when no query provider")
		}
	})
}

func TestBuildDataHubURN(t *testing.T) {
	tests := []struct {
		name    string
		mapping URNMappingConfig
		catalog string
		schema  string
		table   string
		want    string
	}{
		{
			name:    "default platform",
			mapping: URNMappingConfig{},
			catalog: "rdbms",
			schema:  "public",
			table:   "users",
			want:    "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.users,PROD)",
		},
		{
			name:    "custom platform",
			mapping: URNMappingConfig{Platform: "postgres"},
			catalog: "rdbms",
			schema:  "public",
			table:   "users",
			want:    "urn:li:dataset:(urn:li:dataPlatform:postgres,rdbms.public.users,PROD)",
		},
		{
			name: "catalog mapping",
			mapping: URNMappingConfig{
				Platform:       "postgres",
				CatalogMapping: map[string]string{"rdbms": "warehouse"},
			},
			catalog: "rdbms",
			schema:  "public",
			table:   "users",
			want:    "urn:li:dataset:(urn:li:dataPlatform:postgres,warehouse.public.users,PROD)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDataHubURN(tt.mapping, tt.catalog, tt.schema, tt.table)
			if got != tt.want {
				t.Errorf("buildDataHubURN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterResourceTemplates(t *testing.T) {
	t.Run("disabled", func(_ *testing.T) {
		s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
		p := &Platform{
			config:    &Config{Resources: ResourcesConfig{Enabled: false}},
			mcpServer: s,
		}
		p.registerResourceTemplates()
		// No panic or error means success — templates not registered.
	})

	t.Run("enabled", func(_ *testing.T) {
		s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
		p := &Platform{
			config:    &Config{Resources: ResourcesConfig{Enabled: true}},
			mcpServer: s,
		}
		p.registerResourceTemplates()
		// Verify templates registered by checking no panic.
	})
}

func TestMarshalResourceResult(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		result, err := marshalResourceResult("test://uri", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
		if result.Contents[0].URI != "test://uri" {
			t.Errorf("URI = %q, want %q", result.Contents[0].URI, "test://uri")
		}
		if result.Contents[0].MIMEType != "application/json" {
			t.Errorf("MIMEType = %q, want %q", result.Contents[0].MIMEType, "application/json")
		}
	})
}
