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
		request := mcp.CallToolRequest{}
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
