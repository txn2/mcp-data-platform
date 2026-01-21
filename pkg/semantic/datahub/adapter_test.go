package datahub

import (
	"context"
	"errors"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// mockDataHubClient implements the Client interface for testing.
type mockDataHubClient struct {
	searchFunc          func(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error)
	getEntityFunc       func(ctx context.Context, urn string) (*types.Entity, error)
	getSchemaFunc       func(ctx context.Context, urn string) (*types.SchemaMetadata, error)
	getLineageFunc      func(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error)
	getGlossaryTermFunc func(ctx context.Context, urn string) (*types.GlossaryTerm, error)
	pingFunc            func(ctx context.Context) error
	closeFunc           func() error
}

func (m *mockDataHubClient) Search(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, query, opts...)
	}
	return &types.SearchResult{}, nil
}

func (m *mockDataHubClient) GetEntity(ctx context.Context, urn string) (*types.Entity, error) {
	if m.getEntityFunc != nil {
		return m.getEntityFunc(ctx, urn)
	}
	return &types.Entity{}, nil
}

func (m *mockDataHubClient) GetSchema(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
	if m.getSchemaFunc != nil {
		return m.getSchemaFunc(ctx, urn)
	}
	return &types.SchemaMetadata{}, nil
}

func (m *mockDataHubClient) GetLineage(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error) {
	if m.getLineageFunc != nil {
		return m.getLineageFunc(ctx, urn, opts...)
	}
	return &types.LineageResult{}, nil
}

func (m *mockDataHubClient) GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error) {
	if m.getGlossaryTermFunc != nil {
		return m.getGlossaryTermFunc(ctx, urn)
	}
	return &types.GlossaryTerm{}, nil
}

func (m *mockDataHubClient) Ping(ctx context.Context) error {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return nil
}

func (m *mockDataHubClient) Close() error {
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
		mock := &mockDataHubClient{}
		adapter, err := NewWithClient(Config{}, mock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if adapter.Name() != "datahub" {
			t.Errorf("expected name 'datahub', got %q", adapter.Name())
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		mock := &mockDataHubClient{}
		adapter, err := NewWithClient(Config{}, mock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if adapter.cfg.Platform != "trino" {
			t.Errorf("expected default platform 'trino', got %q", adapter.cfg.Platform)
		}
	})
}

func TestAdapterName(t *testing.T) {
	mock := &mockDataHubClient{}
	adapter, _ := NewWithClient(Config{}, mock)
	if adapter.Name() != "datahub" {
		t.Errorf("expected 'datahub', got %q", adapter.Name())
	}
}

func TestGetTableContext(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		getEntityFunc: func(_ context.Context, _ string) (*types.Entity, error) {
			return &types.Entity{
				URN:          "urn:li:dataset:(urn:li:dataPlatform:trino,schema.table,PROD)",
				Description:  "Test table",
				LastModified: 1700000000000,
				Owners: []types.Owner{
					{URN: "urn:li:corpuser:owner1", Name: "Owner One", Email: "owner1@example.com", Type: types.OwnershipTypeTechnicalOwner},
				},
				Tags: []types.Tag{
					{Name: "important"},
				},
				GlossaryTerms: []types.GlossaryTerm{
					{URN: "urn:li:glossaryTerm:revenue", Name: "Revenue"},
				},
				Domain: &types.Domain{
					URN:  "urn:li:domain:finance",
					Name: "Finance",
				},
				Deprecation: &types.Deprecation{
					Deprecated: true,
					Note:       "Use new_table instead",
				},
				Properties: map[string]any{
					"owner_team": "analytics",
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Platform: "trino"}, mock)

	result, err := adapter.GetTableContext(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Description != "Test table" {
		t.Errorf("expected description 'Test table', got %q", result.Description)
	}
	if len(result.Owners) != 1 {
		t.Errorf("expected 1 owner, got %d", len(result.Owners))
	}
	if len(result.Tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(result.Tags))
	}
	if result.Domain == nil || result.Domain.Name != "Finance" {
		t.Error("expected domain Finance")
	}
	if result.Deprecation == nil || !result.Deprecation.Deprecated {
		t.Error("expected deprecation to be set")
	}
}

func TestGetColumnContext(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		getSchemaFunc: func(_ context.Context, _ string) (*types.SchemaMetadata, error) {
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{
						FieldPath:   "user_id",
						Description: "User identifier",
						Tags:        []types.Tag{{Name: "pii"}},
					},
					{
						FieldPath:   "email",
						Description: "User email",
						Tags:        []types.Tag{{Name: "sensitive"}},
						GlossaryTerms: []types.GlossaryTerm{
							{URN: "urn:li:glossaryTerm:email", Name: "Email Address"},
						},
					},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Platform: "trino"}, mock)

	t.Run("found column", func(t *testing.T) {
		result, err := adapter.GetColumnContext(ctx, semantic.ColumnIdentifier{
			TableIdentifier: semantic.TableIdentifier{Schema: "schema", Table: "table"},
			Column:          "email",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "email" {
			t.Errorf("expected name 'email', got %q", result.Name)
		}
		if !result.IsSensitive {
			t.Error("expected IsSensitive to be true")
		}
		if len(result.GlossaryTerms) != 1 {
			t.Errorf("expected 1 glossary term, got %d", len(result.GlossaryTerms))
		}
	})

	t.Run("column not found", func(t *testing.T) {
		_, err := adapter.GetColumnContext(ctx, semantic.ColumnIdentifier{
			TableIdentifier: semantic.TableIdentifier{Schema: "schema", Table: "table"},
			Column:          "nonexistent",
		})
		if err == nil {
			t.Error("expected error for missing column")
		}
	})
}

func TestGetColumnsContext(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		getSchemaFunc: func(_ context.Context, _ string) (*types.SchemaMetadata, error) {
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{FieldPath: "id", Description: "ID"},
					{FieldPath: "name", Description: "Name"},
					{FieldPath: "nested.field", Description: "Nested field"},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Platform: "trino"}, mock)

	result, err := adapter.GetColumnsContext(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 columns, got %d", len(result))
	}
	// Nested field should be extracted as just "field"
	if _, ok := result["field"]; !ok {
		t.Error("expected 'field' key for nested.field")
	}
}

func TestGetLineage(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		getLineageFunc: func(_ context.Context, _ string, _ ...dhclient.LineageOption) (*types.LineageResult, error) {
			return &types.LineageResult{
				Nodes: []types.LineageNode{
					{URN: "urn:li:dataset:1", Name: "source_table", Type: "DATASET", Platform: "trino", Level: 1},
					{URN: "urn:li:dataset:2", Name: "target_table", Type: "DATASET", Platform: "trino", Level: 0},
				},
				Edges: []types.LineageEdge{
					{Source: "urn:li:dataset:1", Target: "urn:li:dataset:2"},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Platform: "trino"}, mock)

	t.Run("upstream lineage", func(t *testing.T) {
		result, err := adapter.GetLineage(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"}, semantic.LineageUpstream, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Direction != semantic.LineageUpstream {
			t.Errorf("expected upstream direction")
		}
		if len(result.Entities) != 2 {
			t.Errorf("expected 2 entities, got %d", len(result.Entities))
		}
	})

	t.Run("downstream lineage", func(t *testing.T) {
		result, err := adapter.GetLineage(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"}, semantic.LineageDownstream, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Direction != semantic.LineageDownstream {
			t.Errorf("expected downstream direction")
		}
	})
}

func TestGetGlossaryTerm(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		getGlossaryTermFunc: func(_ context.Context, urn string) (*types.GlossaryTerm, error) {
			return &types.GlossaryTerm{
				URN:         urn,
				Name:        "Revenue",
				Description: "Total revenue from sales",
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	result, err := adapter.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:revenue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Name != "Revenue" {
		t.Errorf("expected name 'Revenue', got %q", result.Name)
	}
	if result.Description != "Total revenue from sales" {
		t.Errorf("expected description, got %q", result.Description)
	}
}

func TestSearchTables(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		searchFunc: func(_ context.Context, _ string, _ ...dhclient.SearchOption) (*types.SearchResult, error) {
			return &types.SearchResult{
				Entities: []types.SearchEntity{
					{
						URN:         "urn:li:dataset:1",
						Name:        "users",
						Platform:    "trino",
						Description: "User table",
						Tags:        []types.Tag{{Name: "important"}},
						Domain:      &types.Domain{Name: "Core"},
						MatchedFields: []types.MatchedField{
							{Name: "name"},
						},
					},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	results, err := adapter.SearchTables(ctx, semantic.SearchFilter{
		Query:  "users",
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "users" {
		t.Errorf("expected name 'users', got %q", results[0].Name)
	}
	if results[0].Domain != "Core" {
		t.Errorf("expected domain 'Core', got %q", results[0].Domain)
	}
	if results[0].MatchedField != "name" {
		t.Errorf("expected matched field 'name', got %q", results[0].MatchedField)
	}
}

func TestResolveURN(t *testing.T) {
	mock := &mockDataHubClient{}
	adapter, _ := NewWithClient(Config{}, mock)
	ctx := context.Background()

	tests := []struct {
		name        string
		urn         string
		wantCatalog string
		wantSchema  string
		wantTable   string
		wantErr     bool
	}{
		{
			name:        "valid URN with 3 parts",
			urn:         "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			wantCatalog: "catalog",
			wantSchema:  "schema",
			wantTable:   "table",
			wantErr:     false,
		},
		{
			name:       "valid URN with 2 parts",
			urn:        "urn:li:dataset:(urn:li:dataPlatform:trino,schema.table,PROD)",
			wantSchema: "schema",
			wantTable:  "table",
			wantErr:    false,
		},
		{
			name:    "invalid URN prefix",
			urn:     "urn:wrong:dataset:(urn:li:dataPlatform:trino,table,PROD)",
			wantErr: true,
		},
		{
			name:    "invalid URN format",
			urn:     "urn:li:dataset:invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapter.ResolveURN(ctx, tt.urn)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Catalog != tt.wantCatalog {
				t.Errorf("expected catalog %q, got %q", tt.wantCatalog, result.Catalog)
			}
			if result.Schema != tt.wantSchema {
				t.Errorf("expected schema %q, got %q", tt.wantSchema, result.Schema)
			}
			if result.Table != tt.wantTable {
				t.Errorf("expected table %q, got %q", tt.wantTable, result.Table)
			}
		})
	}
}

func TestBuildURN(t *testing.T) {
	mock := &mockDataHubClient{}
	adapter, _ := NewWithClient(Config{Platform: "trino"}, mock)
	ctx := context.Background()

	urn, err := adapter.BuildURN(ctx, semantic.TableIdentifier{Catalog: "hive", Schema: "analytics", Table: "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "urn:li:dataset:(urn:li:dataPlatform:trino,hive.analytics.users,PROD)"
	if urn != expected {
		t.Errorf("expected %q, got %q", expected, urn)
	}
}

func TestAdapterClose(t *testing.T) {
	closed := false
	mock := &mockDataHubClient{
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
}

func TestConvertFunctions(t *testing.T) {
	t.Run("convertOwners", func(t *testing.T) {
		owners := []types.Owner{
			{URN: "urn:1", Name: "Owner1", Email: "o1@test.com", Type: types.OwnershipTypeTechnicalOwner},
			{URN: "urn:2", Name: "Owner2", Email: "o2@test.com", Type: types.OwnershipTypeBusinessOwner},
		}
		result := convertOwners(owners)
		if len(result) != 2 {
			t.Errorf("expected 2 owners, got %d", len(result))
		}
	})

	t.Run("convertTags", func(t *testing.T) {
		tags := []types.Tag{{Name: "tag1"}, {Name: "tag2"}}
		result := convertTags(tags)
		if len(result) != 2 || result[0] != "tag1" {
			t.Error("tags not converted correctly")
		}
	})

	t.Run("convertDomain nil", func(t *testing.T) {
		result := convertDomain(nil)
		if result != nil {
			t.Error("expected nil for nil domain")
		}
	})

	t.Run("convertDeprecation nil", func(t *testing.T) {
		result := convertDeprecation(nil)
		if result != nil {
			t.Error("expected nil for nil deprecation")
		}
	})

	t.Run("convertDeprecation with decommission time", func(t *testing.T) {
		dep := &types.Deprecation{
			Deprecated:       true,
			Note:             "test",
			DecommissionTime: 1700000000000,
		}
		result := convertDeprecation(dep)
		if result.DecommDate == nil {
			t.Error("expected decommission date to be set")
		}
	})

	t.Run("convertProperties empty", func(t *testing.T) {
		result := convertProperties(nil)
		if result != nil {
			t.Error("expected nil for nil properties")
		}
	})

	t.Run("convertProperties with values", func(t *testing.T) {
		props := map[string]any{
			"key1": "value1",
			"key2": 123, // non-string should be skipped
			"key3": "value3",
		}
		result := convertProperties(props)
		if len(result) != 2 {
			t.Errorf("expected 2 properties, got %d", len(result))
		}
	})

	t.Run("convertTimestamp zero", func(t *testing.T) {
		result := convertTimestamp(0)
		if result != nil {
			t.Error("expected nil for zero timestamp")
		}
	})

	t.Run("convertTimestamp valid", func(t *testing.T) {
		result := convertTimestamp(1700000000000)
		if result == nil {
			t.Error("expected non-nil timestamp")
		}
	})

	t.Run("extractFieldName", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"simple", "simple"},
			{"nested.field", "field"},
			{"deeply.nested.field", "field"},
			{"", ""},
		}
		for _, tt := range tests {
			result := extractFieldName(tt.input)
			if result != tt.expected {
				t.Errorf("extractFieldName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		}
	})

	t.Run("ownerTypeToSemantic", func(t *testing.T) {
		if ownerTypeToSemantic(types.OwnershipTypeTechnicalOwner) != semantic.OwnerTypeUser {
			t.Error("unexpected owner type conversion")
		}
		if ownerTypeToSemantic(types.OwnershipTypeBusinessOwner) != semantic.OwnerTypeUser {
			t.Error("unexpected owner type conversion")
		}
		// Test default case with an unknown type
		if ownerTypeToSemantic(types.OwnershipType("unknown")) != semantic.OwnerTypeUser {
			t.Error("unexpected owner type conversion for unknown type")
		}
	})
}

func TestGetTableContextError(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		getEntityFunc: func(_ context.Context, _ string) (*types.Entity, error) {
			return nil, errors.New("entity not found")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.GetTableContext(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetColumnContextError(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		getSchemaFunc: func(_ context.Context, _ string) (*types.SchemaMetadata, error) {
			return nil, errors.New("schema not found")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.GetColumnContext(ctx, semantic.ColumnIdentifier{
		TableIdentifier: semantic.TableIdentifier{Schema: "schema", Table: "table"},
		Column:          "col",
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetColumnsContextError(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		getSchemaFunc: func(_ context.Context, _ string) (*types.SchemaMetadata, error) {
			return nil, errors.New("schema not found")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.GetColumnsContext(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetLineageError(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		getLineageFunc: func(_ context.Context, _ string, _ ...dhclient.LineageOption) (*types.LineageResult, error) {
			return nil, errors.New("lineage not found")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.GetLineage(ctx, semantic.TableIdentifier{Schema: "schema", Table: "table"}, semantic.LineageUpstream, 3)
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetGlossaryTermNotFound(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		getGlossaryTermFunc: func(_ context.Context, _ string) (*types.GlossaryTerm, error) {
			return nil, nil // returns nil without error (not found case)
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:notfound")
	if err == nil {
		t.Error("expected error for nil glossary term")
	}
}

func TestGetGlossaryTermError(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		getGlossaryTermFunc: func(_ context.Context, _ string) (*types.GlossaryTerm, error) {
			return nil, errors.New("term not found")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:notfound")
	if err == nil {
		t.Error("expected error")
	}
}

func TestSearchTablesError(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		searchFunc: func(_ context.Context, _ string, _ ...dhclient.SearchOption) (*types.SearchResult, error) {
			return nil, errors.New("search failed")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	_, err := adapter.SearchTables(ctx, semantic.SearchFilter{Query: "test"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestSearchTablesWithFilters(t *testing.T) {
	ctx := context.Background()
	mock := &mockDataHubClient{
		searchFunc: func(_ context.Context, _ string, _ ...dhclient.SearchOption) (*types.SearchResult, error) {
			return &types.SearchResult{
				Entities: []types.SearchEntity{
					{URN: "urn:1", Name: "table1"},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	// Test with domain and tag filters
	results, err := adapter.SearchTables(ctx, semantic.SearchFilter{
		Query:  "test",
		Domain: "finance",
		Tags:   []string{"pii", "sensitive"},
		Limit:  50,
		Offset: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestAdapterCloseError(t *testing.T) {
	mock := &mockDataHubClient{
		closeFunc: func() error {
			return errors.New("close failed")
		},
	}
	adapter, _ := NewWithClient(Config{}, mock)

	err := adapter.Close()
	if err == nil {
		t.Error("expected error")
	}
}

func TestResolveURNEdgeCases(t *testing.T) {
	mock := &mockDataHubClient{}
	adapter, _ := NewWithClient(Config{}, mock)
	ctx := context.Background()

	t.Run("single part table name is invalid", func(t *testing.T) {
		_, err := adapter.ResolveURN(ctx, "urn:li:dataset:(urn:li:dataPlatform:trino,table,PROD)")
		if err == nil {
			t.Error("expected error for single part table name")
		}
	})

	t.Run("empty path in URN", func(t *testing.T) {
		_, err := adapter.ResolveURN(ctx, "urn:li:dataset:(urn:li:dataPlatform:trino,,PROD)")
		if err == nil {
			t.Error("expected error for empty path")
		}
	})
}

func TestFieldToColumnContextEdgeCases(t *testing.T) {
	ctx := context.Background()

	mock := &mockDataHubClient{
		getSchemaFunc: func(_ context.Context, _ string) (*types.SchemaMetadata, error) {
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{
						FieldPath:   "pii_field",
						Description: "Sensitive data",
						Tags:        []types.Tag{{Name: "pii"}},
						NativeType:  "VARCHAR",
					},
				},
			}, nil
		},
	}
	adapter, _ := NewWithClient(Config{Platform: "trino"}, mock)

	result, err := adapter.GetColumnContext(ctx, semantic.ColumnIdentifier{
		TableIdentifier: semantic.TableIdentifier{Schema: "schema", Table: "table"},
		Column:          "pii_field",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsPII {
		t.Error("expected IsPII to be true for pii tag")
	}
}

// Verify Adapter implements interfaces.
var (
	_ semantic.Provider    = (*Adapter)(nil)
	_ semantic.URNResolver = (*Adapter)(nil)
)
