package middleware

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/storage"
)

// Test constants for semantic tests.
const (
	semTestBucket          = "my-bucket"
	semTestMock            = "mock"
	semTestTable           = "table"
	semTestSchema          = "schema"
	semTestURNKey          = "urn"
	semTestQualityScore    = 95.0
	semTestURNCount        = 4
	semTestYear            = 2026
	semTestCatalogSchTable = "catalog.schema.table"
	semTestTrino           = "trino"
	semTestSemanticCtx     = "semantic_context"
	semTestSession1        = "session-1"
	semTestDescTest        = "Test"
	semTestDescTestTable   = "Test table"
	semTestMetadataRef     = "metadata_reference"
	semTestDay             = 15
	semTestHour            = 10
	semTestMinute          = 30
)

// requireTextContent extracts and returns the TextContent at the given index,
// failing the test if the type assertion fails.
func requireTextContent(t *testing.T, result *mcp.CallToolResult) *mcp.TextContent {
	t.Helper()
	tc, ok := result.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent at index 1, got %T", result.Content[1])
	}
	return tc
}

// requireUnmarshalJSON unmarshals JSON from a string into a map, failing the test on error.
func requireUnmarshalJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	return data
}

// requireEnrich calls enricher.enrich and fails the test on error.
func requireEnrich(t *testing.T, enricher *semanticEnricher, result *mcp.CallToolResult, request mcp.CallToolRequest, pc *PlatformContext) *mcp.CallToolResult {
	t.Helper()
	enriched, err := enricher.enrich(context.Background(), result, request, pc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return enriched
}

// requireNoErr fails the test if err is non-nil.
func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// requireContentLen fails if the enriched result does not have exactly n content items.
func requireContentLen(t *testing.T, enriched *mcp.CallToolResult, n int) {
	t.Helper()
	if len(enriched.Content) != n {
		t.Fatalf("expected %d content items, got %d", n, len(enriched.Content))
	}
}

// requireSemanticCtxMap extracts the semantic_context map from the enriched result's
// second content item (index 1), failing the test if extraction fails.
func requireSemanticCtxMap(t *testing.T, enriched *mcp.CallToolResult) map[string]any {
	t.Helper()
	tc := requireTextContent(t, enriched)
	data := requireUnmarshalJSON(t, tc.Text)
	semCtx, ok := data[semTestSemanticCtx].(map[string]any)
	if !ok {
		t.Fatal("expected semantic_context in enrichment")
	}
	return semCtx
}

// assertFieldsPresent checks that all named fields exist in the given map.
func assertFieldsPresent(t *testing.T, m map[string]any, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, exists := m[field]; !exists {
			t.Errorf("expected field %q to be present, but it was missing", field)
		}
	}
}

// assertFieldsAbsent checks that all named fields do NOT exist in the given map.
func assertFieldsAbsent(t *testing.T, m map[string]any, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, exists := m[field]; exists {
			t.Errorf("expected field %q to be absent, but it was present", field)
		}
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

	t.Run("separate catalog/schema/table params", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"catalog": "rdbms",
			"schema":  "public",
			"table":   "users",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		table := extractTableFromRequest(request)
		if table != "rdbms.public.users" {
			t.Errorf("expected 'rdbms.public.users', got %q", table)
		}
	})

	t.Run("separate schema/table params only", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"schema": "public",
			"table":  "users",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		table := extractTableFromRequest(request)
		if table != "public.users" {
			t.Errorf("expected 'public.users', got %q", table)
		}
	})

	t.Run("table only without catalog/schema", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"table": "users",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		table := extractTableFromRequest(request)
		if table != "users" {
			t.Errorf("expected 'users', got %q", table)
		}
	})
}

func TestExtractURNFromRequest(t *testing.T) {
	t.Run("urn present", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,test.public.users,PROD)",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		urn := extractURNFromRequest(request)
		if urn != "urn:li:dataset:(urn:li:dataPlatform:trino,test.public.users,PROD)" {
			t.Errorf("expected URN, got %q", urn)
		}
	})

	t.Run("no urn field", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"query": "test",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		urn := extractURNFromRequest(request)
		if urn != "" {
			t.Errorf("expected empty string, got %q", urn)
		}
	})

	t.Run("empty arguments", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}
		urn := extractURNFromRequest(request)
		if urn != "" {
			t.Errorf("expected empty string, got %q", urn)
		}
	})

	t.Run("nil params", func(t *testing.T) {
		request := mcp.CallToolRequest{}
		urn := extractURNFromRequest(request)
		if urn != "" {
			t.Errorf("expected empty string, got %q", urn)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: []byte("invalid")},
		}
		urn := extractURNFromRequest(request)
		if urn != "" {
			t.Errorf("expected empty string, got %q", urn)
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
			input:     semTestTable,
			wantTable: semTestTable,
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
		{"catalog.schema.table", []string{"catalog", semTestSchema, semTestTable}},
		{"schema.table", []string{semTestSchema, semTestTable}},
		{semTestTable, []string{semTestTable}},
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
	if len(urns) != semTestURNCount {
		t.Errorf("expected 4 URNs, got %d: %v", len(urns), urns)
	}
}

func TestAppendSemanticContext(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendSemanticContextWithColumns(result, nil, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
	})

	t.Run("with context", func(t *testing.T) {
		result := NewToolResultText("original")
		ctx := &semantic.TableContext{
			Description: semTestDescTestTable,
			Tags:        []string{"important"},
		}
		enriched, err := appendSemanticContextWithColumns(result, ctx, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
	})

	t.Run("includes all fields when populated", func(t *testing.T) {
		result := NewToolResultText("original")
		ctx := &semantic.TableContext{
			URN:         "urn:li:dataset:(urn:li:dataPlatform:postgres,warehouse.public.users,PROD)",
			Description: "User table",
			Owners:      []semantic.Owner{{URN: "urn:owner", Name: "Data Team"}},
			Tags:        []string{"pii", "important"},
			GlossaryTerms: []semantic.GlossaryTerm{
				{URN: "urn:li:glossaryTerm:user", Name: "User", Description: "A registered user"},
			},
			Domain: &semantic.Domain{URN: "urn:domain", Name: "Core", Description: "Core data"},
			CustomProperties: map[string]string{
				"owner_team": "platform",
				"sla":        "tier1",
			},
			LastModified: testTimePointer(),
		}
		enriched, err := appendSemanticContextWithColumns(result, ctx, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)

		semanticCtx := requireSemanticCtxMap(t, enriched)

		assertFieldsPresent(t, semanticCtx, []string{
			"urn", "description", "owners", "tags",
			"glossary_terms", "domain", "custom_properties", "last_modified",
		})

		if semanticCtx["urn"] != ctx.URN {
			t.Errorf("expected urn %q, got %v", ctx.URN, semanticCtx["urn"])
		}
		if semanticCtx["description"] != ctx.Description {
			t.Errorf("expected description %q, got %v", ctx.Description, semanticCtx["description"])
		}
	})

	t.Run("omits empty optional fields", func(t *testing.T) {
		result := NewToolResultText("original")
		ctx := &semantic.TableContext{
			Description: "Minimal table",
		}
		enriched, err := appendSemanticContextWithColumns(result, ctx, nil)
		requireNoErr(t, err)

		semanticCtx := requireSemanticCtxMap(t, enriched)
		assertFieldsAbsent(t, semanticCtx, []string{
			"urn", "glossary_terms", "custom_properties", "last_modified",
		})
	})
}

// testTimePointer returns a pointer to a fixed time.Time for testing.
func testTimePointer() *time.Time {
	t := time.Date(semTestYear, 1, semTestDay, semTestHour, semTestMinute, 0, 0, time.UTC)
	return &t
}

func TestAppendQueryContext(t *testing.T) {
	t.Run("empty contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendQueryContext(result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 1)
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
		requireContentLen(t, enriched, 2)
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
			"bucket": semTestBucket,
			"prefix": "data/",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != semTestBucket {
			t.Errorf("expected bucket 'my-bucket', got %q", bucket)
		}
		if prefix != "data/" {
			t.Errorf("expected prefix 'data/', got %q", prefix)
		}
	})

	t.Run("key without prefix", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"bucket": semTestBucket,
			"key":    "data/subdir/file.parquet",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != semTestBucket {
			t.Errorf("expected bucket 'my-bucket', got %q", bucket)
		}
		if prefix != "data/subdir" {
			t.Errorf("expected prefix 'data/subdir', got %q", prefix)
		}
	})

	t.Run("key at root", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"bucket": semTestBucket,
			"key":    "file.parquet",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		bucket, prefix := extractS3PathFromRequest(request)
		if bucket != semTestBucket {
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
		requireContentLen(t, enriched, 1)
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
		requireContentLen(t, enriched, 2)
	})
}

func TestAppendS3SemanticContext(t *testing.T) {
	t.Run("empty contexts", func(t *testing.T) {
		result := NewToolResultText("original")
		enriched, err := appendS3SemanticContext(result, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 1)
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
		requireContentLen(t, enriched, 2)
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

	score := semTestQualityScore
	tableCtx.QualityScore = &score

	result := buildTableSemanticContext(sr, tableCtx)

	if result[semTestURNKey] != "urn:li:dataset:1" {
		t.Errorf("unexpected urn: %v", result[semTestURNKey])
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
	if result["quality_score"] != semTestQualityScore {
		t.Errorf("unexpected quality_score: %v", result["quality_score"])
	}
}

// mockSemanticProvider implements semantic.Provider for testing.
type mockSemanticProvider struct {
	getTableContextFunc func(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
	searchTablesFunc    func(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
}

func (*mockSemanticProvider) Name() string { return semTestMock }
func (m *mockSemanticProvider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	if m.getTableContextFunc != nil {
		return m.getTableContextFunc(ctx, table)
	}
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (m *mockSemanticProvider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	if m.searchTablesFunc != nil {
		return m.searchTablesFunc(ctx, filter)
	}
	return nil, nil //nolint:nilnil // test mock returns zero values
}
func (*mockSemanticProvider) Close() error { return nil }

// mockQueryProvider implements query.Provider for testing.
type mockQueryProvider struct {
	getTableAvailabilityFunc func(ctx context.Context, urn string) (*query.TableAvailability, error)
}

func (*mockQueryProvider) Name() string { return semTestMock }
func (*mockQueryProvider) ResolveTable(_ context.Context, _ string) (*query.TableIdentifier, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (m *mockQueryProvider) GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error) {
	if m.getTableAvailabilityFunc != nil {
		return m.getTableAvailabilityFunc(ctx, urn)
	}
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockQueryProvider) GetQueryExamples(_ context.Context, _ string) ([]query.Example, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockQueryProvider) GetExecutionContext(_ context.Context, _ []string) (*query.ExecutionContext, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockQueryProvider) GetTableSchema(_ context.Context, _ query.TableIdentifier) (*query.TableSchema, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}
func (*mockQueryProvider) Close() error { return nil }

// mockStorageProvider implements storage.Provider for testing.
type mockStorageProvider struct {
	getDatasetAvailabilityFunc func(ctx context.Context, urn string) (*storage.DatasetAvailability, error)
}

func (*mockStorageProvider) Name() string { return semTestMock }
func (*mockStorageProvider) ResolveDataset(_ context.Context, _ string) (*storage.DatasetIdentifier, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (m *mockStorageProvider) GetDatasetAvailability(ctx context.Context, urn string) (*storage.DatasetAvailability, error) {
	if m.getDatasetAvailabilityFunc != nil {
		return m.getDatasetAvailabilityFunc(ctx, urn)
	}
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockStorageProvider) GetAccessExamples(_ context.Context, _ string) ([]storage.AccessExample, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}

func (*mockStorageProvider) ListObjects(_ context.Context, _ storage.DatasetIdentifier, _ int) ([]storage.ObjectInfo, error) {
	return nil, nil //nolint:nilnil // test mock returns zero values
}
func (*mockStorageProvider) Close() error { return nil }

func TestEnrichTrinoResult(t *testing.T) {
	t.Run("no table in request", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{}
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
	})

	t.Run("with table and semantic context", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: semTestDescTestTable,
					Tags:        []string{"tag1"},
				}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{"table": "schema.my_table"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
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
		requireNoErr(t, err)
		// Should return original result without error
		requireContentLen(t, enriched, 1)
	})

	t.Run("with SQL query parameter extracts tables", func(t *testing.T) {
		result := NewToolResultText("query results")
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: "Table: " + table.Table,
					Owners:      []semantic.Owner{{Name: "owner", Email: "owner@test.com"}},
				}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{
			"sql": "SELECT * FROM catalog.schema.users JOIN catalog.schema.orders ON users.id = orders.user_id",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		// Should have original + semantic context
		requireContentLen(t, enriched, 2)
	})

	t.Run("with SQL query no tables found falls back to empty", func(t *testing.T) {
		result := NewToolResultText("query results")
		provider := &mockSemanticProvider{}
		args, _ := json.Marshal(map[string]any{
			"sql": "SELECT 1",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		// No tables found, no enrichment
		requireContentLen(t, enriched, 1)
	})

	t.Run("with column context error continues", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: semTestDescTestTable,
				}, nil
			},
		}
		// Override GetColumnsContext to return error
		args, _ := json.Marshal(map[string]any{"table": "schema.my_table"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichTrinoResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		// Should still have enrichment even if columns fail
		requireContentLen(t, enriched, 2)
	})
}

func TestEnrichDataHubResult(t *testing.T) {
	t.Run("no URNs in result", func(t *testing.T) {
		result := NewToolResultText("no urns here")
		provider := &mockQueryProvider{}
		request := mcp.CallToolRequest{}

		enriched, err := enrichDataHubResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
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
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
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
		requireNoErr(t, err)
		// Should return original result (error on GetTableAvailability is skipped)
		requireContentLen(t, enriched, 1)
	})

	t.Run("URN from request added if not in result", func(t *testing.T) {
		result := NewToolResultText("no urns here")
		urnsCalled := make([]string, 0)
		provider := &mockQueryProvider{
			getTableAvailabilityFunc: func(_ context.Context, urn string) (*query.TableAvailability, error) {
				urnsCalled = append(urnsCalled, urn)
				return &query.TableAvailability{
					Available:  true,
					QueryTable: "schema.table",
				}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:from_request",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichDataHubResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
		if len(urnsCalled) != 1 || urnsCalled[0] != "urn:li:dataset:from_request" {
			t.Errorf("expected URN from request to be called, got %v", urnsCalled)
		}
	})

	t.Run("URN from request not duplicated if already in result", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:same",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		urnsCalled := make([]string, 0)
		provider := &mockQueryProvider{
			getTableAvailabilityFunc: func(_ context.Context, urn string) (*query.TableAvailability, error) {
				urnsCalled = append(urnsCalled, urn)
				return &query.TableAvailability{
					Available:  true,
					QueryTable: "schema.table",
				}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:same",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		_, err := enrichDataHubResult(context.Background(), result, request, provider)
		requireNoErr(t, err)
		// Should only be called once, not twice
		if len(urnsCalled) != 1 {
			t.Errorf("expected URN to be called once (not duplicated), got %d calls", len(urnsCalled))
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
		requireContentLen(t, enriched, 1)
	})

	t.Run("no matching datasets", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{
			searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
				return nil, nil
			},
		}
		args, _ := json.Marshal(map[string]any{"bucket": semTestBucket})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichS3Result(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 1)
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
		args, _ := json.Marshal(map[string]any{"bucket": semTestBucket, "prefix": "data/"})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enrichS3Result(context.Background(), result, request, provider)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 2)
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
		requireContentLen(t, enriched, 1)
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
		requireContentLen(t, enriched, 2)
	})
}

func TestSemanticEnricherEnrich(t *testing.T) {
	t.Run("trino toolkit with enrichment enabled", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: &mockSemanticProvider{
				getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
					return &semantic.TableContext{Description: semTestDescTest}, nil
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
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
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
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
	})

	t.Run("s3 toolkit with enrichment enabled", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: &mockSemanticProvider{
				searchTablesFunc: func(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
					return []semantic.TableSearchResult{
						{URN: "urn:li:dataset:1", Name: "dataset1"},
					}, nil
				},
				getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
					return &semantic.TableContext{Description: semTestDescTest}, nil
				},
			},
			cfg: EnrichmentConfig{EnrichS3Results: true},
		}

		args, _ := json.Marshal(map[string]any{"bucket": semTestBucket, "prefix": "data/"})
		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: "s3"}
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
	})

	t.Run("s3 toolkit with enrichment disabled", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: &mockSemanticProvider{},
			cfg:              EnrichmentConfig{EnrichS3Results: false},
		}

		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: "s3"}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
	})

	t.Run("trino toolkit with nil provider", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: nil,
			cfg:              EnrichmentConfig{EnrichTrinoResults: true},
		}

		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: semTestTrino}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
	})

	t.Run("unknown toolkit", func(t *testing.T) {
		enricher := &semanticEnricher{}

		result := NewToolResultText("original")
		pc := &PlatformContext{ToolkitKind: "unknown"}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
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

func TestEnrichDataHubResultWithAll(t *testing.T) {
	t.Run("enriches with query context", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:1",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		enricher := &semanticEnricher{
			queryProvider: &mockQueryProvider{
				getTableAvailabilityFunc: func(_ context.Context, _ string) (*query.TableAvailability, error) {
					return &query.TableAvailability{Available: true, QueryTable: "schema.table"}, nil
				},
			},
			cfg: EnrichmentConfig{
				EnrichDataHubResults: true,
			},
		}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrichDataHubResultWithAll(context.Background(), result, request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 2)
	})

	t.Run("enriches with storage context", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/key,PROD)",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		enricher := &semanticEnricher{
			storageProvider: &mockStorageProvider{
				getDatasetAvailabilityFunc: func(_ context.Context, _ string) (*storage.DatasetAvailability, error) {
					return &storage.DatasetAvailability{Available: true, Bucket: "bucket"}, nil
				},
			},
			cfg: EnrichmentConfig{
				EnrichDataHubStorageResults: true,
			},
		}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrichDataHubResultWithAll(context.Background(), result, request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 2)
	})

	t.Run("enriches with both query and storage context", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"results": []any{
				map[string]any{"urn": "urn:li:dataset:1"},
				map[string]any{"urn": "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/key,PROD)"},
			},
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		enricher := &semanticEnricher{
			queryProvider: &mockQueryProvider{
				getTableAvailabilityFunc: func(_ context.Context, _ string) (*query.TableAvailability, error) {
					return &query.TableAvailability{Available: true, QueryTable: "schema.table"}, nil
				},
			},
			storageProvider: &mockStorageProvider{
				getDatasetAvailabilityFunc: func(_ context.Context, _ string) (*storage.DatasetAvailability, error) {
					return &storage.DatasetAvailability{Available: true, Bucket: "bucket"}, nil
				},
			},
			cfg: EnrichmentConfig{
				EnrichDataHubResults:        true,
				EnrichDataHubStorageResults: true,
			},
		}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrichDataHubResultWithAll(context.Background(), result, request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Original + query context + storage context = 3
		if len(enriched.Content) < 2 {
			t.Errorf("expected at least 2 content items, got %d", len(enriched.Content))
		}
	})

	t.Run("no enrichment when disabled", func(t *testing.T) {
		result := NewToolResultText("original")
		enricher := &semanticEnricher{
			cfg: EnrichmentConfig{
				EnrichDataHubResults:        false,
				EnrichDataHubStorageResults: false,
			},
		}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrichDataHubResultWithAll(context.Background(), result, request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireContentLen(t, enriched, 1)
	})
}

func TestEnricherEnrichDataHubPath(t *testing.T) {
	t.Run("datahub toolkit triggers enrichDataHubResultWithAll", func(t *testing.T) {
		jsonContent, _ := json.Marshal(map[string]any{
			"urn": "urn:li:dataset:1",
		})
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: string(jsonContent)},
			},
		}
		enricher := &semanticEnricher{
			queryProvider: &mockQueryProvider{
				getTableAvailabilityFunc: func(_ context.Context, _ string) (*query.TableAvailability, error) {
					return &query.TableAvailability{Available: true, QueryTable: "schema.table"}, nil
				},
			},
			cfg: EnrichmentConfig{
				EnrichDataHubResults: true,
			},
		}

		pc := &PlatformContext{ToolkitKind: "datahub"}
		request := mcp.CallToolRequest{}

		enriched, err := enricher.enrich(context.Background(), result, request, pc)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
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

func TestBuildColumnInfo(t *testing.T) {
	t.Run("basic column", func(t *testing.T) {
		col := &semantic.ColumnContext{
			Description:   "Test description",
			GlossaryTerms: []semantic.GlossaryTerm{{URN: "urn:term", Name: "Term"}},
			Tags:          []string{"tag1", "tag2"},
			IsPII:         true,
			IsSensitive:   false,
		}

		info := buildColumnInfo(col)

		if info["description"] != "Test description" {
			t.Errorf("expected description 'Test description', got %v", info["description"])
		}
		piiVal, _ := info["is_pii"].(bool)
		if !piiVal {
			t.Error("expected is_pii=true")
		}
		sensitiveVal, _ := info["is_sensitive"].(bool)
		if sensitiveVal {
			t.Error("expected is_sensitive=false")
		}
		if _, exists := info["inherited_from"]; exists {
			t.Error("inherited_from should not be present without InheritedFrom")
		}
	})

	t.Run("column with inheritance", func(t *testing.T) {
		col := &semantic.ColumnContext{
			Description: "Inherited description",
			InheritedFrom: &semantic.InheritedMetadata{
				SourceURN:    "urn:li:dataset:source",
				SourceColumn: "source_col",
				Hops:         2,
				MatchMethod:  "name_transformed",
			},
		}

		info := buildColumnInfo(col)

		inherited, ok := info["inherited_from"].(map[string]any)
		if !ok {
			t.Fatal("expected inherited_from to be a map")
		}
		if inherited["source_dataset"] != "urn:li:dataset:source" {
			t.Errorf("expected source_dataset 'urn:li:dataset:source', got %v", inherited["source_dataset"])
		}
		if inherited["source_column"] != "source_col" {
			t.Errorf("expected source_column 'source_col', got %v", inherited["source_column"])
		}
		if inherited["hops"] != 2 {
			t.Errorf("expected hops=2, got %v", inherited["hops"])
		}
		if inherited["match_method"] != "name_transformed" {
			t.Errorf("expected match_method 'name_transformed', got %v", inherited["match_method"])
		}
	})
}

func TestBuildColumnContexts(t *testing.T) {
	t.Run("empty columns", func(t *testing.T) {
		columns := map[string]*semantic.ColumnContext{}
		ctx, sources := buildColumnContexts(columns)

		if len(ctx) != 0 {
			t.Errorf("expected empty context, got %d entries", len(ctx))
		}
		if len(sources) != 0 {
			t.Errorf("expected empty sources, got %d entries", len(sources))
		}
	})

	t.Run("columns without inheritance", func(t *testing.T) {
		columns := map[string]*semantic.ColumnContext{
			"col1": {Description: "Column 1"},
			"col2": {Description: "Column 2"},
		}

		ctx, sources := buildColumnContexts(columns)

		if len(ctx) != 2 {
			t.Errorf("expected 2 columns, got %d", len(ctx))
		}
		if len(sources) != 0 {
			t.Errorf("expected no inheritance sources, got %d", len(sources))
		}
	})

	t.Run("columns with inheritance", func(t *testing.T) {
		columns := map[string]*semantic.ColumnContext{
			"col1": {
				Description: "Column 1",
				InheritedFrom: &semantic.InheritedMetadata{
					SourceURN:    "urn:li:dataset:source1",
					SourceColumn: "src1",
					Hops:         1,
					MatchMethod:  "name_exact",
				},
			},
			"col2": {
				Description: "Column 2",
				InheritedFrom: &semantic.InheritedMetadata{
					SourceURN:    "urn:li:dataset:source1",
					SourceColumn: "src2",
					Hops:         1,
					MatchMethod:  "name_exact",
				},
			},
			"col3": {
				Description: "Column 3",
				InheritedFrom: &semantic.InheritedMetadata{
					SourceURN:    "urn:li:dataset:source2",
					SourceColumn: "src3",
					Hops:         2,
					MatchMethod:  "column_lineage",
				},
			},
		}

		ctx, sources := buildColumnContexts(columns)

		if len(ctx) != 3 {
			t.Errorf("expected 3 columns, got %d", len(ctx))
		}
		if len(sources) != 2 {
			t.Errorf("expected 2 unique inheritance sources, got %d", len(sources))
		}

		sourceSet := make(map[string]bool)
		for _, s := range sources {
			sourceSet[s] = true
		}
		if !sourceSet["urn:li:dataset:source1"] {
			t.Error("expected source1 in sources")
		}
		if !sourceSet["urn:li:dataset:source2"] {
			t.Error("expected source2 in sources")
		}
	})
}

func TestAppendSemanticContextWithColumns_WithColumnContext(t *testing.T) {
	t.Run("with column context and inheritance", func(t *testing.T) {
		result := NewToolResultText("original")
		tableCtx := &semantic.TableContext{
			Description: semTestDescTestTable,
			URN:         "urn:li:dataset:test",
		}
		columnsCtx := map[string]*semantic.ColumnContext{
			"user_id": {
				Description: "User identifier",
				IsPII:       true,
				InheritedFrom: &semantic.InheritedMetadata{
					SourceURN:    "urn:li:dataset:upstream",
					SourceColumn: "id",
					Hops:         1,
					MatchMethod:  "name_transformed",
				},
			},
			"amount": {
				Description:   "Transaction amount",
				GlossaryTerms: []semantic.GlossaryTerm{{URN: "urn:term", Name: "Amount"}},
			},
		}

		enriched, err := appendSemanticContextWithColumns(result, tableCtx, columnsCtx)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)

		tc := requireTextContent(t, enriched)
		data := requireUnmarshalJSON(t, tc.Text)

		colCtx, ok := data["column_context"].(map[string]any)
		if !ok {
			t.Fatal("expected column_context in enrichment")
		}
		if len(colCtx) != 2 {
			t.Errorf("expected 2 columns in context, got %d", len(colCtx))
		}

		sources, ok := data["inheritance_sources"].([]any)
		if !ok {
			t.Fatal("expected inheritance_sources in enrichment")
		}
		if len(sources) != 1 {
			t.Errorf("expected 1 inheritance source, got %d", len(sources))
		}

		userIDCol, ok := colCtx["user_id"].(map[string]any)
		if !ok {
			t.Fatal("expected user_id column")
		}
		if _, exists := userIDCol["inherited_from"]; !exists {
			t.Error("expected inherited_from in user_id column")
		}
	})

	t.Run("with column context no inheritance", func(t *testing.T) {
		result := NewToolResultText("original")
		tableCtx := &semantic.TableContext{Description: semTestDescTestTable}
		columnsCtx := map[string]*semantic.ColumnContext{
			"name": {Description: "Name field"},
		}

		enriched, err := appendSemanticContextWithColumns(result, tableCtx, columnsCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		textContent := requireTextContent(t, enriched)
		data := requireUnmarshalJSON(t, textContent.Text)

		if _, ok := data["column_context"]; !ok {
			t.Error("expected column_context")
		}
		if _, ok := data["inheritance_sources"]; ok {
			t.Error("inheritance_sources should not exist when no columns have inheritance")
		}
	})
}

func TestExtractSQLFromRequest(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "with sql parameter",
			args:     map[string]any{"sql": "SELECT * FROM users"},
			expected: "SELECT * FROM users",
		},
		{
			name:     "without sql parameter",
			args:     map[string]any{"table": "users"},
			expected: "",
		},
		{
			name:     "empty args",
			args:     map[string]any{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsJSON, _ := json.Marshal(tt.args)
			req := mcp.CallToolRequest{
				Params: &mcp.CallToolParamsRaw{
					Arguments: argsJSON,
				},
			}
			result := extractSQLFromRequest(req)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractTableFromSQL(t *testing.T) {
	t.Run("with sql containing table", func(t *testing.T) {
		args := map[string]any{"sql": "SELECT * FROM schema.table"}
		result := extractTableFromSQL(args)
		if result != "schema.table" {
			t.Errorf("expected schema.table, got %s", result)
		}
	})

	t.Run("no sql key", func(t *testing.T) {
		args := map[string]any{"other": "value"}
		result := extractTableFromSQL(args)
		if result != "" {
			t.Errorf("expected empty, got %s", result)
		}
	})

	t.Run("empty sql", func(t *testing.T) {
		args := map[string]any{"sql": ""}
		result := extractTableFromSQL(args)
		if result != "" {
			t.Errorf("expected empty, got %s", result)
		}
	})

	t.Run("sql with no tables", func(t *testing.T) {
		args := map[string]any{"sql": "SELECT 1"}
		result := extractTableFromSQL(args)
		if result != "" {
			t.Errorf("expected empty, got %s", result)
		}
	})

	t.Run("sql with three-part table name", func(t *testing.T) {
		args := map[string]any{"sql": "SELECT * FROM catalog.schema.table"}
		result := extractTableFromSQL(args)
		if result != "catalog.schema.table" {
			t.Errorf("expected catalog.schema.table, got %s", result)
		}
	})
}

func TestFormatTableRefs(t *testing.T) {
	refs := []TableRef{
		{FullPath: "catalog.schema.table1"},
		{FullPath: "catalog.schema.table2"},
	}
	result := formatTableRefs(refs)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0] != "catalog.schema.table1" {
		t.Errorf("expected catalog.schema.table1, got %s", result[0])
	}
	if result[1] != "catalog.schema.table2" {
		t.Errorf("expected catalog.schema.table2, got %s", result[1])
	}
}

func TestRefToTableIdentifier(t *testing.T) {
	ref := TableRef{
		Catalog: "my_catalog",
		Schema:  "my_schema",
		Table:   "my_table",
	}
	result := refToTableIdentifier(ref)
	if result.Catalog != "my_catalog" {
		t.Errorf("expected catalog my_catalog, got %s", result.Catalog)
	}
	if result.Schema != "my_schema" {
		t.Errorf("expected schema my_schema, got %s", result.Schema)
	}
	if result.Table != "my_table" {
		t.Errorf("expected table my_table, got %s", result.Table)
	}
}

func TestBuildAdditionalTableContext(t *testing.T) {
	ref := TableRef{FullPath: "catalog.schema.table"}
	ctx := &semantic.TableContext{
		URN:         "urn:li:dataset:test",
		Description: semTestDescTestTable,
		Tags:        []string{"tag1", "tag2"},
		Owners:      []semantic.Owner{{Name: "owner", Email: "owner@test.com"}},
		Deprecation: &semantic.Deprecation{Deprecated: true, Note: "Use new_table"},
	}

	result := buildAdditionalTableContext(ref, ctx)

	if result[semTestTable] != semTestCatalogSchTable {
		t.Errorf("expected table %s, got %v", semTestCatalogSchTable, result[semTestTable])
	}
	if result["description"] != semTestDescTestTable {
		t.Errorf("expected description Test table, got %v", result["description"])
	}
	if result[semTestURNKey] != "urn:li:dataset:test" {
		t.Errorf("expected urn, got %v", result[semTestURNKey])
	}
	if result["deprecation"] == nil {
		t.Error("expected deprecation to be set")
	}
	tags, ok := result["tags"].([]string)
	if !ok || len(tags) != 2 {
		t.Errorf("expected 2 tags, got %v", result["tags"])
	}
	owners, ok := result["owners"].([]semantic.Owner)
	if !ok || len(owners) != 1 {
		t.Errorf("expected 1 owner, got %v", result["owners"])
	}
}

func TestAppendSemanticContextWithAdditional(t *testing.T) {
	t.Run("nil context returns original", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{}}
		enriched, err := appendSemanticContextWithAdditional(result, nil, nil, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 0)
	})

	t.Run("adds semantic context with additional tables", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "original"},
		}}
		tableCtx := &semantic.TableContext{
			Description: "Primary table",
			Owners:      []semantic.Owner{{Name: "owner", Email: "owner@test.com"}},
		}
		additionalTables := []map[string]any{
			{"table": "second.table", "description": "Second table"},
		}

		enriched, err := appendSemanticContextWithAdditional(result, tableCtx, nil, additionalTables)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)

		data := requireUnmarshalJSON(t, requireTextContent(t, enriched).Text)
		assertFieldsPresent(t, data, []string{semTestSemanticCtx, "additional_tables"})
	})

	t.Run("adds column context with inheritance sources", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "original"},
		}}
		tableCtx := &semantic.TableContext{
			Description: "Primary table",
		}
		columnsCtx := map[string]*semantic.ColumnContext{
			"user_id": {
				Description: "User identifier",
				IsPII:       true,
				InheritedFrom: &semantic.InheritedMetadata{
					SourceURN:    "urn:li:dataset:upstream",
					SourceColumn: "id",
					Hops:         1,
					MatchMethod:  "name_transformed",
				},
			},
		}

		enriched, err := appendSemanticContextWithAdditional(result, tableCtx, columnsCtx, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)

		data := requireUnmarshalJSON(t, requireTextContent(t, enriched).Text)
		assertFieldsPresent(t, data, []string{"column_context", "inheritance_sources"})
	})

	t.Run("no additional tables omits additional_tables key", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "original"},
		}}
		tableCtx := &semantic.TableContext{
			Description: "Primary table",
		}

		enriched, err := appendSemanticContextWithAdditional(result, tableCtx, nil, nil)
		requireNoErr(t, err)

		data := requireUnmarshalJSON(t, requireTextContent(t, enriched).Text)
		assertFieldsAbsent(t, data, []string{"additional_tables"})
	})
}

func TestEnrichTrinoQueryResult(t *testing.T) {
	t.Run("empty tables returns original", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{}}
		provider := &mockSemanticProvider{}

		enriched, err := enrichTrinoQueryResult(context.Background(), result, []TableRef{}, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 0)
	})

	t.Run("enriches with semantic context", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "query results"},
		}}
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: "Table: " + table.Table,
					Owners:      []semantic.Owner{{Name: "owner", Email: "owner@test.com"}},
				}, nil
			},
		}

		tables := []TableRef{
			{Catalog: "cat1", Schema: "sch1", Table: "primary", FullPath: "cat1.sch1.primary"},
			{Catalog: "cat2", Schema: "sch2", Table: "secondary", FullPath: "cat2.sch2.secondary"},
		}

		enriched, err := enrichTrinoQueryResult(context.Background(), result, tables, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)

		data := requireUnmarshalJSON(t, requireTextContent(t, enriched).Text)
		assertFieldsPresent(t, data, []string{semTestSemanticCtx, "additional_tables"})
	})

	t.Run("primary table GetTableContext fails returns original", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "query results"},
		}}
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return nil, context.Canceled
			},
		}

		tables := []TableRef{
			{Catalog: "cat1", Schema: "sch1", Table: "primary", FullPath: "cat1.sch1.primary"},
		}

		enriched, err := enrichTrinoQueryResult(context.Background(), result, tables, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 1)
	})

	t.Run("additional table GetTableContext fails continues", func(t *testing.T) {
		result := &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "query results"},
		}}
		callCount := 0
		provider := &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				callCount++
				if callCount == 1 {
					// Primary table succeeds
					return &semantic.TableContext{
						Description: "Primary table",
						Owners:      []semantic.Owner{{Name: "owner", Email: "owner@test.com"}},
					}, nil
				}
				// Additional tables fail
				return nil, context.Canceled
			},
		}

		tables := []TableRef{
			{Catalog: "cat1", Schema: "sch1", Table: "primary", FullPath: "cat1.sch1.primary"},
			{Catalog: "cat2", Schema: "sch2", Table: "secondary", FullPath: "cat2.sch2.secondary"},
		}

		enriched, err := enrichTrinoQueryResult(context.Background(), result, tables, provider)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)
	})
}

// --- Session Dedup Tests ---

func TestSessionDedup_FirstAccess_FullEnrichment(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: "Full enrichment table",
					Tags:        []string{"test"},
				}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	result := NewToolResultText("original")
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: semTestSession1}

	enriched := requireEnrich(t, enricher, result, request, pc)

	// Should have full enrichment (original + semantic_context)
	if len(enriched.Content) != 2 {
		t.Fatalf("expected 2 content items, got %d", len(enriched.Content))
	}

	// Verify it's full semantic_context (not a reference)
	textContent := requireTextContent(t, enriched)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestSemanticCtx]; !ok {
		t.Error("expected semantic_context in enrichment")
	}
	if _, ok := data[semTestMetadataRef]; ok {
		t.Error("should NOT have metadata_reference on first access")
	}
}

func TestSessionDedup_SecondAccess_MinimalReference(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description: semTestDescTestTable,
					Tags:        []string{"test"},
				}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": "catalog.schema.table"})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: semTestSession1}

	// First access - full enrichment
	result1 := NewToolResultText("result1")
	enriched1 := requireEnrich(t, enricher, result1, request, pc)
	if len(enriched1.Content) != 2 {
		t.Fatalf("first access: expected 2 content items, got %d", len(enriched1.Content))
	}

	// Second access - should get reference
	result2 := NewToolResultText("result2")
	enriched2 := requireEnrich(t, enricher, result2, request, pc)
	if len(enriched2.Content) != 2 {
		t.Fatalf("second access: expected 2 content items, got %d", len(enriched2.Content))
	}

	textContent := requireTextContent(t, enriched2)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestMetadataRef]; !ok {
		t.Error("expected metadata_reference on second access")
	}
	if _, ok := data[semTestSemanticCtx]; ok {
		t.Error("should NOT have full semantic_context on second access with reference mode")
	}
}

func TestSessionDedup_TTLExpiry_FullEnrichmentAgain(t *testing.T) {
	cache := NewSessionEnrichmentCache(50*time.Millisecond, 30*time.Minute)
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{Description: semTestDescTest}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: semTestSession1}

	// First access
	result1 := NewToolResultText("result1")
	requireEnrich(t, enricher, result1, request, pc)

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Third access after expiry - should get full enrichment again
	result3 := NewToolResultText("result3")
	enriched3 := requireEnrich(t, enricher, result3, request, pc)

	textContent := requireTextContent(t, enriched3)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestSemanticCtx]; !ok {
		t.Error("expected full semantic_context after TTL expiry")
	}
}

func TestSessionDedup_SessionIsolation(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{Description: semTestDescTest}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}

	// Session A first access
	pcA := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "session-A"}
	resultA := NewToolResultText("resultA")
	enrichedA := requireEnrich(t, enricher, resultA, request, pcA)
	if len(enrichedA.Content) != 2 {
		t.Fatalf("session A first access: expected 2 content items, got %d", len(enrichedA.Content))
	}

	// Session B first access - should also get full enrichment (isolated)
	pcB := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "session-B"}
	resultB := NewToolResultText("resultB")
	enrichedB := requireEnrich(t, enricher, resultB, request, pcB)
	if len(enrichedB.Content) != 2 {
		t.Fatalf("session B first access: expected 2 content items, got %d", len(enrichedB.Content))
	}

	// Verify session B got full enrichment (not reference)
	textContent := requireTextContent(t, enrichedB)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestSemanticCtx]; !ok {
		t.Error("session B should get full semantic_context on first access")
	}
}

func TestSessionDedup_EnabledByDefault(t *testing.T) {
	// When SessionCache is non-nil, dedup is active
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{Description: semTestDescTest}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: semTestSession1}

	// First call
	requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)
	// Second call should be deduped
	result2 := NewToolResultText("r2")
	enriched2 := requireEnrich(t, enricher, result2, request, pc)

	textContent := requireTextContent(t, enriched2)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestMetadataRef]; !ok {
		t.Error("dedup should be active (reference mode) by default when cache is configured")
	}
}

func TestSessionDedup_DisabledFallback(t *testing.T) {
	callCount := 0
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				callCount++
				return &semantic.TableContext{Description: semTestDescTest}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       nil, // Disabled
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: semTestSession1}

	// Both calls should get full enrichment
	requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)
	result2 := NewToolResultText("r2")
	enriched2 := requireEnrich(t, enricher, result2, request, pc)

	textContent := requireTextContent(t, enriched2)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestSemanticCtx]; !ok {
		t.Error("with nil cache, should always get full semantic_context")
	}
	if callCount != 2 {
		t.Errorf("expected provider called twice (no dedup), got %d", callCount)
	}
}

func TestSessionDedup_ConfigurableModes(t *testing.T) {
	makeEnricher := func(mode DedupMode) *semanticEnricher {
		return &semanticEnricher{
			semanticProvider: &mockSemanticProvider{
				getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
					return &semantic.TableContext{
						Description: semTestDescTestTable,
						Tags:        []string{"tag1"},
					}, nil
				},
			},
			cfg: EnrichmentConfig{
				EnrichTrinoResults: true,
				SessionCache:       NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute),
				DedupMode:          mode,
			},
		}
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}

	t.Run("reference mode", func(t *testing.T) {
		enricher := makeEnricher(DedupModeReference)
		pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}

		requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)
		result2 := NewToolResultText("r2")
		enriched2 := requireEnrich(t, enricher, result2, request, pc)

		textContent := requireTextContent(t, enriched2)
		data := requireUnmarshalJSON(t, textContent.Text)
		if _, ok := data[semTestMetadataRef]; !ok {
			t.Error("reference mode: expected metadata_reference")
		}
	})

	t.Run("summary mode", func(t *testing.T) {
		enricher := makeEnricher(DedupModeSummary)
		pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}

		requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)
		result2 := NewToolResultText("r2")
		enriched2 := requireEnrich(t, enricher, result2, request, pc)

		textContent := requireTextContent(t, enriched2)
		data := requireUnmarshalJSON(t, textContent.Text)
		if _, ok := data[semTestSemanticCtx]; !ok {
			t.Error("summary mode: expected semantic_context")
		}
		if _, ok := data["note"]; !ok {
			t.Error("summary mode: expected note about summary")
		}
	})

	t.Run("none mode", func(t *testing.T) {
		enricher := makeEnricher(DedupModeNone)
		pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}

		requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)
		result2 := NewToolResultText("r2")
		enriched2 := requireEnrich(t, enricher, result2, request, pc)

		// "none" mode should NOT add any enrichment content on second access
		if len(enriched2.Content) != 1 {
			t.Errorf("none mode: expected 1 content item (original only), got %d", len(enriched2.Content))
		}
	})
}

func TestSessionDedup_StdioTransport(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{Description: semTestDescTest}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       cache,
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}

	// Use "stdio" as session ID (as extractSessionID would return for stdio transport)
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "stdio"}

	// First call - full enrichment
	result1 := NewToolResultText("r1")
	enriched1 := requireEnrich(t, enricher, result1, request, pc)
	if len(enriched1.Content) != 2 {
		t.Fatalf("first call: expected 2 content items, got %d", len(enriched1.Content))
	}

	// Second call - should be deduped even with "stdio" session
	result2 := NewToolResultText("r2")
	enriched2 := requireEnrich(t, enricher, result2, request, pc)

	textContent := requireTextContent(t, enriched2)
	data := requireUnmarshalJSON(t, textContent.Text)
	if _, ok := data[semTestMetadataRef]; !ok {
		t.Error("stdio transport: expected metadata_reference on second access")
	}
}

func TestExtractTableKeysFromRequest(t *testing.T) {
	t.Run("sql with single table", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"sql": "SELECT * FROM catalog.schema.users WHERE id = 1",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		keys := extractTableKeysFromRequest(request)
		if len(keys) != 1 || keys[0] != "catalog.schema.users" {
			t.Errorf("expected [catalog.schema.users], got %v", keys)
		}
	})

	t.Run("explicit table parameter", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"table": "catalog.schema.users",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		keys := extractTableKeysFromRequest(request)
		if len(keys) != 1 || keys[0] != "catalog.schema.users" {
			t.Errorf("expected [catalog.schema.users], got %v", keys)
		}
	})

	t.Run("no table found", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"other": "value",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		keys := extractTableKeysFromRequest(request)
		if len(keys) != 0 {
			t.Errorf("expected 0 keys, got %d", len(keys))
		}
	})
}

func TestAppendMetadataReference(t *testing.T) {
	result := NewToolResultText("original")
	tableKeys := []string{"catalog.schema.table1", "catalog.schema.table2"}

	enriched := appendMetadataReference(result, tableKeys)

	if len(enriched.Content) != 2 {
		t.Fatalf("expected 2 content items, got %d", len(enriched.Content))
	}

	textContent, ok := enriched.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", enriched.Content[1])
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &data); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	ref, ok := data[semTestMetadataRef].(map[string]any)
	if !ok {
		t.Fatal("expected metadata_reference in output")
	}
	tables, ok := ref["tables"].([]any)
	if !ok || len(tables) != 2 {
		t.Errorf("expected 2 tables in reference, got %v", ref["tables"])
	}
	if _, ok := ref["note"].(string); !ok {
		t.Error("expected note in reference")
	}
}

func TestParseDataHubURNComponents(t *testing.T) {
	tests := []struct {
		name                               string
		urn                                string
		wantCatalog, wantSchema, wantTable string
	}{
		{
			name:        "standard trino URN",
			urn:         "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.orders,PROD)",
			wantCatalog: "rdbms",
			wantSchema:  "public",
			wantTable:   "orders",
		},
		{
			name:        "postgres platform",
			urn:         "urn:li:dataset:(urn:li:dataPlatform:postgres,warehouse.sales.invoices,PROD)",
			wantCatalog: "warehouse",
			wantSchema:  "sales",
			wantTable:   "invoices",
		},
		{
			name:        "not a dataset URN",
			urn:         "urn:li:glossaryTerm:revenue",
			wantCatalog: "",
			wantSchema:  "",
			wantTable:   "",
		},
		{
			name:        "empty string",
			urn:         "",
			wantCatalog: "",
			wantSchema:  "",
			wantTable:   "",
		},
		{
			name:        "missing table part",
			urn:         "urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public,PROD)",
			wantCatalog: "",
			wantSchema:  "",
			wantTable:   "",
		},
		{
			name:        "no commas",
			urn:         "urn:li:dataset:(urn:li:dataPlatform:trino)",
			wantCatalog: "",
			wantSchema:  "",
			wantTable:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog, schema, table := parseDataHubURNComponents(tt.urn)
			if catalog != tt.wantCatalog {
				t.Errorf("catalog = %q, want %q", catalog, tt.wantCatalog)
			}
			if schema != tt.wantSchema {
				t.Errorf("schema = %q, want %q", schema, tt.wantSchema)
			}
			if table != tt.wantTable {
				t.Errorf("table = %q, want %q", table, tt.wantTable)
			}
		})
	}
}

func TestAppendResourceLinks(t *testing.T) {
	t.Run("adds schema and availability links", func(t *testing.T) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "original"}},
		}
		urns := []string{"urn:li:dataset:(urn:li:dataPlatform:trino,rdbms.public.orders,PROD)"}

		got := appendResourceLinks(result, urns)

		// 1 original + 2 resource links (schema + availability)
		if len(got.Content) != 3 {
			t.Fatalf("content count = %d, want 3", len(got.Content))
		}

		schemaLink, ok := got.Content[1].(*mcp.ResourceLink)
		if !ok {
			t.Fatal("content[1] is not a ResourceLink")
		}
		if schemaLink.URI != "schema://rdbms.public/orders" {
			t.Errorf("schema URI = %q, want %q", schemaLink.URI, "schema://rdbms.public/orders")
		}

		availLink, ok := got.Content[2].(*mcp.ResourceLink)
		if !ok {
			t.Fatal("content[2] is not a ResourceLink")
		}
		if availLink.URI != "availability://rdbms.public/orders" {
			t.Errorf("availability URI = %q, want %q", availLink.URI, "availability://rdbms.public/orders")
		}
	})

	t.Run("empty URNs returns unchanged result", func(t *testing.T) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "original"}},
		}
		got := appendResourceLinks(result, nil)
		if len(got.Content) != 1 {
			t.Errorf("content count = %d, want 1", len(got.Content))
		}
	})

	t.Run("invalid URN is skipped", func(t *testing.T) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "original"}},
		}
		urns := []string{"urn:li:glossaryTerm:revenue"}
		got := appendResourceLinks(result, urns)
		if len(got.Content) != 1 {
			t.Errorf("content count = %d, want 1", len(got.Content))
		}
	})
}
