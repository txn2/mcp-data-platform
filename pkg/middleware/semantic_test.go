package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

func TestSemanticEnrichmentMiddleware(t *testing.T) {
	t.Run("no platform context", func(t *testing.T) {
		middleware := SemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{})
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultText("success"), nil
		})

		result, err := handler(context.Background(), mcp.CallToolRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected success result")
		}
	})

	t.Run("error result not enriched", func(t *testing.T) {
		middleware := SemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{
			EnrichTrinoResults: true,
		})
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultError("error"), nil
		})

		pc := &PlatformContext{ToolkitKind: "trino"}
		ctx := WithPlatformContext(context.Background(), pc)
		result, err := handler(ctx, mcp.CallToolRequest{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result")
		}
	})

	t.Run("nil result not enriched", func(t *testing.T) {
		middleware := SemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{})
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, nil
		})

		pc := &PlatformContext{ToolkitKind: "trino"}
		ctx := WithPlatformContext(context.Background(), pc)
		result, err := handler(ctx, mcp.CallToolRequest{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Error("expected nil result")
		}
	})
}

func TestEnrichmentConfig(t *testing.T) {
	cfg := EnrichmentConfig{
		EnrichTrinoResults:          true,
		EnrichDataHubResults:        true,
		EnrichS3Results:             true,
		EnrichDataHubStorageResults: true,
	}

	if !cfg.EnrichTrinoResults {
		t.Error("expected EnrichTrinoResults to be true")
	}
	if !cfg.EnrichDataHubResults {
		t.Error("expected EnrichDataHubResults to be true")
	}
	if !cfg.EnrichS3Results {
		t.Error("expected EnrichS3Results to be true")
	}
	if !cfg.EnrichDataHubStorageResults {
		t.Error("expected EnrichDataHubStorageResults to be true")
	}
}

func TestExtractTableFromRequest(t *testing.T) {
	t.Run("empty arguments", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}
		table := extractTableFromRequest(request)
		if table != "" {
			t.Errorf("expected empty string, got %q", table)
		}
	})

	t.Run("table field", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"table": "schema.table"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		table := extractTableFromRequest(request)
		if table != "schema.table" {
			t.Errorf("expected 'schema.table', got %q", table)
		}
	})

	t.Run("table_name field", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{"table_name": "schema.other_table"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		table := extractTableFromRequest(request)
		if table != "schema.other_table" {
			t.Errorf("expected 'schema.other_table', got %q", table)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: []byte("invalid")},
		}
		table := extractTableFromRequest(request)
		if table != "" {
			t.Errorf("expected empty string, got %q", table)
		}
	})
}

func TestParseTableIdentifier(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCatalog string
		wantSchema  string
		wantTable   string
	}{
		{
			name:        "three parts",
			input:       "catalog.schema.table",
			wantCatalog: "catalog",
			wantSchema:  "schema",
			wantTable:   "table",
		},
		{
			name:       "two parts",
			input:      "schema.table",
			wantSchema: "schema",
			wantTable:  "table",
		},
		{
			name:      "one part",
			input:     "table",
			wantTable: "table",
		},
		{
			name:      "empty",
			input:     "",
			wantTable: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTableIdentifier(tt.input)
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

func TestSplitTableName(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"catalog.schema.table", []string{"catalog", "schema", "table"}},
		{"schema.table", []string{"schema", "table"}},
		{"table", []string{"table"}},
		{"", nil},
		{"...", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := splitTableName(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d parts, got %d: %v", len(tt.expected), len(result), result)
			}
		})
	}
}

func TestExtractURNsFromResult(t *testing.T) {
	t.Run("text content with URN", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn":  "urn:li:dataset:1",
			"name": "test",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		urns := extractURNsFromResult(result)
		if len(urns) != 1 {
			t.Errorf("expected 1 URN, got %d", len(urns))
		}
		if urns[0] != "urn:li:dataset:1" {
			t.Errorf("expected 'urn:li:dataset:1', got %q", urns[0])
		}
	})

	t.Run("nested URN", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"results": []any{
				map[string]any{"urn": "urn:li:dataset:1"},
				map[string]any{"urn": "urn:li:dataset:2"},
			},
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		urns := extractURNsFromResult(result)
		if len(urns) != 2 {
			t.Errorf("expected 2 URNs, got %d", len(urns))
		}
	})
}

func TestExtractURNsFromMap(t *testing.T) {
	data := map[string]any{
		"urn":   "urn:li:dataset:1",
		"URN":   "urn:li:dataset:2",
		"other": "value",
		"nested": map[string]any{
			"urn": "urn:li:dataset:3",
		},
		"array": []any{
			map[string]any{"urn": "urn:li:dataset:4"},
		},
	}

	urns := extractURNsFromMap(data)
	if len(urns) != 4 {
		t.Errorf("expected 4 URNs, got %d: %v", len(urns), urns)
	}
}

func TestAppendSemanticContext(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendSemanticContext(result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with context", func(t *testing.T) {
		result := NewToolResultText("original")
		ctx := &semantic.TableContext{
			Description: "Test table",
			Tags:        []string{"important"},
		}
		enriched, err := appendSemanticContext(result, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})
}

func TestAppendQueryContext(t *testing.T) {
	t.Run("empty contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendQueryContext(result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		contexts := map[string]*query.TableAvailability{
			"urn:li:dataset:1": {Available: true, QueryTable: "schema.table"},
		}
		enriched, err := appendQueryContext(result, contexts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})
}

func TestExtractS3PathFromRequest(t *testing.T) {
	t.Run("empty arguments", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != "" || prefix != "" {
			t.Errorf("expected empty strings, got bucket=%q prefix=%q", bucket, prefix)
		}
	})

	t.Run("bucket and prefix", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"bucket": "my-bucket",
			"prefix": "data/",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != "my-bucket" {
			t.Errorf("expected bucket 'my-bucket', got %q", bucket)
		}
		if prefix != "data/" {
			t.Errorf("expected prefix 'data/', got %q", prefix)
		}
	})

	t.Run("key without prefix", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"bucket": "my-bucket",
			"key":    "data/subdir/file.parquet",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != "my-bucket" {
			t.Errorf("expected bucket 'my-bucket', got %q", bucket)
		}
		if prefix != "data/subdir" {
			t.Errorf("expected prefix 'data/subdir', got %q", prefix)
		}
	})

	t.Run("key at root", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"bucket": "my-bucket",
			"key":    "file.parquet",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != "my-bucket" {
			t.Errorf("expected bucket 'my-bucket', got %q", bucket)
		}
		if prefix != "" {
			t.Errorf("expected empty prefix, got %q", prefix)
		}
	})
}

func TestExtractS3URNsFromResult(t *testing.T) {
	t.Run("with S3 URN", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/key,PROD)",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		urns := extractS3URNsFromResult(result)
		if len(urns) != 1 {
			t.Errorf("expected 1 S3 URN, got %d", len(urns))
		}
	})

	t.Run("non-S3 URN filtered", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,table,PROD)",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		urns := extractS3URNsFromResult(result)
		if len(urns) != 0 {
			t.Errorf("expected 0 S3 URNs, got %d", len(urns))
		}
	})
}

func TestAppendStorageContext(t *testing.T) {
	t.Run("empty contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendStorageContext(result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		contexts := map[string]*storage.DatasetAvailability{
			"urn:li:dataset:1": {Available: true, Bucket: "bucket"},
		}
		enriched, err := appendStorageContext(result, contexts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})
}

func TestAppendS3SemanticContext(t *testing.T) {
	t.Run("empty contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendS3SemanticContext(result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		contexts := []map[string]any{
			{"urn": "urn:1", "name": "dataset1"},
		}
		enriched, err := appendS3SemanticContext(result, contexts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})
}

func TestBuildTableSemanticContext(t *testing.T) {
	sr := semantic.TableSearchResult{
		URN:  "urn:li:dataset:1",
		Name: "test_table",
	}
	tableCtx := &semantic.TableContext{
		Description: "Test description",
		Owners:      []semantic.Owner{{URN: "urn:owner", Name: "Owner"}},
		Tags:        []string{"tag1"},
		Domain:      &semantic.Domain{Name: "Finance"},
		Deprecation: &semantic.Deprecation{Deprecated: true, Note: "deprecated"},
	}

	score := 95.0
	tableCtx.QualityScore = &score

	result := buildTableSemanticContext(sr, tableCtx)

	if result["urn"] != "urn:li:dataset:1" {
		t.Errorf("unexpected urn: %v", result["urn"])
	}
	if result["name"] != "test_table" {
		t.Errorf("unexpected name: %v", result["name"])
	}
	if result["description"] != "Test description" {
		t.Errorf("unexpected description: %v", result["description"])
	}
	if result["domain"] != "Finance" {
		t.Errorf("unexpected domain: %v", result["domain"])
	}
	if result["quality_score"] != 95.0 {
		t.Errorf("unexpected quality_score: %v", result["quality_score"])
	}
}

// mockSemanticProvider implements semantic.Provider for testing.
type mockSemanticProvider struct {
	getTableContextFunc func(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
	searchTablesFunc    func(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
}

func (m *mockSemanticProvider) Name() string { return "mock" }
func (m *mockSemanticProvider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	if m.getTableContextFunc != nil {
		return m.getTableContextFunc(ctx, table)
	}
	return nil, nil
}
func (m *mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil
}
func (m *mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return nil, nil
}
func (m *mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil
}
func (m *mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil
}
func (m *mockSemanticProvider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	if m.searchTablesFunc != nil {
		return m.searchTablesFunc(ctx, filter)
	}
	return nil, nil
}
func (m *mockSemanticProvider) Close() error { return nil }

// mockQueryProvider implements query.Provider for testing.
type mockQueryProvider struct {
	getTableAvailabilityFunc func(ctx context.Context, urn string) (*query.TableAvailability, error)
}

func (m *mockQueryProvider) Name() string { return "mock" }
func (m *mockQueryProvider) ResolveTable(_ context.Context, _ string) (*query.TableIdentifier, error) {
	return nil, nil
}
func (m *mockQueryProvider) GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error) {
	if m.getTableAvailabilityFunc != nil {
		return m.getTableAvailabilityFunc(ctx, urn)
	}
	return nil, nil
}
func (m *mockQueryProvider) GetQueryExamples(_ context.Context, _ string) ([]query.QueryExample, error) {
	return nil, nil
}
func (m *mockQueryProvider) GetExecutionContext(_ context.Context, _ []string) (*query.ExecutionContext, error) {
	return nil, nil
}
func (m *mockQueryProvider) GetTableSchema(_ context.Context, _ query.TableIdentifier) (*query.TableSchema, error) {
	return nil, nil
}
func (m *mockQueryProvider) Close() error { return nil }

// mockStorageProvider implements storage.Provider for testing.
type mockStorageProvider struct {
	getDatasetAvailabilityFunc func(ctx context.Context, urn string) (*storage.DatasetAvailability, error)
}

func (m *mockStorageProvider) Name() string { return "mock" }
func (m *mockStorageProvider) ResolveDataset(_ context.Context, _ string) (*storage.DatasetIdentifier, error) {
	return nil, nil
}
func (m *mockStorageProvider) GetDatasetAvailability(ctx context.Context, urn string) (*storage.DatasetAvailability, error) {
	if m.getDatasetAvailabilityFunc != nil {
		return m.getDatasetAvailabilityFunc(ctx, urn)
	}
	return nil, nil
}
func (m *mockStorageProvider) GetAccessExamples(_ context.Context, _ string) ([]storage.AccessExample, error) {
	return nil, nil
}
func (m *mockStorageProvider) ListObjects(_ context.Context, _ storage.DatasetIdentifier, _ int) ([]storage.ObjectInfo, error) {
	return nil, nil
}
func (m *mockStorageProvider) Close() error { return nil }

func TestEnrichTrinoResult(t *testing.T) {
	t.Run("no table in request", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{}
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with table and semantic context", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: "Test table",
					Tags:        []string{"tag1"},
				}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{"table": "schema.my_table"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})

	t.Run("semantic provider error", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return nil, context.Canceled
			},
		}
		args, _ := json.Marshal(map[string]any{"table": "schema.my_table"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should return original result without error
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item (original), got %d", len(enriched.Content))
		}
	})
}

func TestEnrichDataHubResult(t *testing.T) {
	t.Run("no URNs in result", func(t *testing.T) {
		result := NewToolResultText("no urns here")
		provider := &mockQueryProvider{}
		request := mcp.CallToolRequest{}

		enriched, err := enrichDataHubResult(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with URNs and query context", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:1",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		provider := &mockQueryProvider{
			getTableAvailabilityFunc: func(_ context.Context, _ string) (*query.TableAvailability, error) {
				return &query.TableAvailability{
					Available:  true,
					QueryTable: "schema.table",
				}, nil
			},
		}
		request := mcp.CallToolRequest{}

		enriched, err := enrichDataHubResult(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})

	t.Run("query provider error continues", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:1",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		provider := &mockQueryProvider{
			getTableAvailabilityFunc: func(_ context.Context, _ string) (*query.TableAvailability, error) {
				return nil, context.Canceled
			},
		}
		request := mcp.CallToolRequest{}

		enriched, err := enrichDataHubResult(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should return original result (error on GetTableAvailability is skipped)
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item (original), got %d", len(enriched.Content))
		}
	})
}

func TestEnrichS3Result(t *testing.T) {
	t.Run("no bucket in request", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{}
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}

		enriched, err := enrichS3Result(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("no matching datasets", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
				return nil, nil
			},
		}
		args, _ := json.Marshal(map[string]any{"bucket": "my-bucket"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichS3Result(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with matching datasets", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
				return []semantic.TableSearchResult{
					{URN: "urn:li:dataset:1", Name: "dataset1"},
				}, nil
			},
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: "Test dataset",
				}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{"bucket": "my-bucket", "prefix": "data/"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichS3Result(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})
}

func TestEnrichDataHubStorageResult(t *testing.T) {
	t.Run("no S3 URNs in result", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,table,PROD)",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		provider := &mockStorageProvider{}

		enriched, err := enrichDataHubStorageResult(context.Background(), result, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("with S3 URNs and storage context", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/key,PROD)",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		provider := &mockStorageProvider{
			getDatasetAvailabilityFunc: func(_ context.Context, _ string) (*storage.DatasetAvailability, error) {
				return &storage.DatasetAvailability{
					Available: true,
					Bucket:    "bucket",
				}, nil
			},
		}

		enriched, err := enrichDataHubStorageResult(context.Background(), result, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})
}

func TestSemanticEnricherEnrich(t *testing.T) {
	t.Run("trino toolkit with enrichment enabled", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: &mockSemanticProvider{
				getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
					return &semantic.TableContext{Description: "Test"}, nil
				},
			},
			cfg: EnrichmentConfig{EnrichTrinoResults: true},
		}

		args, _ := json.Marshal(map[string]any{"table": "schema.table"})
		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: "trino"}
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 2 {
			t.Errorf("expected 2 content items, got %d", len(enriched.Content))
		}
	})

	t.Run("trino toolkit with enrichment disabled", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: &mockSemanticProvider{},
			cfg:              EnrichmentConfig{EnrichTrinoResults: false},
		}

		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: "trino"}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})

	t.Run("unknown toolkit", func(t *testing.T) {
		enricher := &semanticEnricher{}

		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: "unknown"}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(enriched.Content) != 1 {
			t.Errorf("expected 1 content item, got %d", len(enriched.Content))
		}
	})
}

func TestSearchS3Datasets(t *testing.T) {
	t.Run("search error returns nil", func(t *testing.T) {
		provider := &mockSemanticProvider{
			searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
				return nil, context.Canceled
			},
		}

		results := searchS3Datasets(context.Background(), provider, "bucket", "prefix")
		if results != nil {
			t.Errorf("expected nil, got %v", results)
		}
	})

	t.Run("successful search", func(t *testing.T) {
		provider := &mockSemanticProvider{
			searchTablesFunc: func(_ context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
				if filter.Platform != "s3" {
					t.Error("expected platform s3")
				}
				return []semantic.TableSearchResult{
					{URN: "urn:1", Name: "dataset1"},
				}, nil
			},
		}

		results := searchS3Datasets(context.Background(), provider, "bucket", "prefix")
		if len(results) != 1 {
			t.Errorf("expected 1 result, got %d", len(results))
		}
	})
}

func TestExtractS3URNsFromMap(t *testing.T) {
	t.Run("S3 URN extracted", func(t *testing.T) {
		data := map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket,PROD)",
		}
		urns := extractS3URNsFromMap(data)
		if len(urns) != 1 {
			t.Errorf("expected 1 URN, got %d", len(urns))
		}
	})

	t.Run("non-S3 URN not extracted", func(t *testing.T) {
		data := map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,table,PROD)",
		}
		urns := extractS3URNsFromMap(data)
		if len(urns) != 0 {
			t.Errorf("expected 0 URNs, got %d", len(urns))
		}
	})

	t.Run("nested S3 URNs extracted", func(t *testing.T) {
		data := map[string]any{
			"results": []any{
				map[string]any{"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket1,PROD)"},
				map[string]any{"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket2,PROD)"},
			},
		}
		urns := extractS3URNsFromMap(data)
		if len(urns) != 2 {
			t.Errorf("expected 2 URNs, got %d", len(urns))
		}
	})
}
