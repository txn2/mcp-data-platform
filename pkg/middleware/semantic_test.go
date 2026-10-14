package middleware

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/sqltables"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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
	semTestDatasetURN1     = "urn:li:dataset:1"
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

// requireQueryContextEntry extracts query_context[urn] from enriched result content.
//
//nolint:unparam // urn varies by test case even if current callers use the same constant
func requireQueryContextEntry(t *testing.T, result *mcp.CallToolResult, urn string) map[string]any {
	t.Helper()
	tc := requireTextContent(t, result)
	data := requireUnmarshalJSON(t, tc.Text)
	qcRaw, ok := data["query_context"]
	if !ok {
		t.Fatal("expected query_context in enrichment")
	}
	qcMap, ok := qcRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected query_context to be object, got %T", qcRaw)
	}
	entryRaw, ok := qcMap[urn]
	if !ok {
		t.Fatalf("expected query_context[%s]", urn)
	}
	entry, ok := entryRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected query_context[%s] to be object, got %T", urn, entryRaw)
	}
	return entry
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

// testEnricher builds a semanticEnricher with the given provider and column filtering setting.
func testEnricher(provider semantic.Provider, columnFiltering bool) *semanticEnricher {
	return &semanticEnricher{
		semanticProvider: provider,
		cfg:              EnrichmentConfig{ColumnContextFiltering: columnFiltering},
	}
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

// mockSemanticProvider implements semantic.Provider for testing.
type mockSemanticProvider struct {
	getTableContextFunc      func(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error)
	getColumnsContextFunc    func(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error)
	searchTablesFunc         func(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
	getCuratedQueryCountFunc func(ctx context.Context, urn string) (int, error)
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

func (m *mockSemanticProvider) GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	if m.getColumnsContextFunc != nil {
		return m.getColumnsContextFunc(ctx, table)
	}
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

func (m *mockSemanticProvider) GetCuratedQueryCount(ctx context.Context, urn string) (int, error) {
	if m.getCuratedQueryCountFunc != nil {
		return m.getCuratedQueryCountFunc(ctx, urn)
	}
	return 0, nil
}
func (*mockSemanticProvider) Close() error { return nil }

// mockQueryProvider implements query.Provider for testing.
type mockQueryProvider struct {
	getTableAvailabilityFunc func(ctx context.Context, urn string) (*query.TableAvailability, error)
	resolveTableFunc         func(ctx context.Context, urn string) (*query.TableIdentifier, error)
	getTableSchemaFunc       func(ctx context.Context, table query.TableIdentifier) (*query.TableSchema, error)
}

func (*mockQueryProvider) Name() string { return semTestMock }

func (m *mockQueryProvider) ResolveTable(ctx context.Context, urn string) (*query.TableIdentifier, error) {
	if m.resolveTableFunc != nil {
		return m.resolveTableFunc(ctx, urn)
	}
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

func (m *mockQueryProvider) GetTableSchema(ctx context.Context, table query.TableIdentifier) (*query.TableSchema, error) {
	if m.getTableSchemaFunc != nil {
		return m.getTableSchemaFunc(ctx, table)
	}
	return nil, nil //nolint:nilnil // test mock returns zero values
}
func (*mockQueryProvider) Close() error { return nil }

func TestEnrichTrinoResult(t *testing.T) {
	t.Run("no table in request", func(t *testing.T) {
		result := NewToolResultText("original")
		provider := &mockSemanticProvider{}
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{},
		}

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
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

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
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

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
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

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
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

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
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

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
		requireNoErr(t, err)
		// Should still have enrichment even if columns fail
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

	t.Run("an s3 result passes through unenriched", func(t *testing.T) {
		enricher := &semanticEnricher{
			semanticProvider: &mockSemanticProvider{},
			cfg:              EnrichmentConfig{EnrichTrinoResults: true},
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

	t.Run("filters out empty columns", func(t *testing.T) {
		columns := map[string]*semantic.ColumnContext{
			"id":          {Name: "id"},
			"name":        {Name: "name", Description: "User name"},
			"created_at":  {Name: "created_at"},
			"email":       {Name: "email", IsPII: true},
			"internal_id": {Name: "internal_id"},
		}

		ctx, _ := buildColumnContexts(columns)

		if len(ctx) != 2 {
			t.Errorf("expected 2 columns with content, got %d", len(ctx))
		}
		if _, ok := ctx["name"]; !ok {
			t.Error("expected 'name' column to be included")
		}
		if _, ok := ctx["email"]; !ok {
			t.Error("expected 'email' column to be included")
		}
		if _, ok := ctx["id"]; ok {
			t.Error("expected 'id' column to be filtered out")
		}
	})

	t.Run("all columns empty returns empty map", func(t *testing.T) {
		columns := map[string]*semantic.ColumnContext{
			"id":         {Name: "id"},
			"created_at": {Name: "created_at"},
			"updated_at": {Name: "updated_at"},
		}

		ctx, sources := buildColumnContexts(columns)

		if len(ctx) != 0 {
			t.Errorf("expected empty context, got %d entries", len(ctx))
		}
		if len(sources) != 0 {
			t.Errorf("expected empty sources, got %d entries", len(sources))
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

	t.Run("all columns empty produces note instead of column_context", func(t *testing.T) {
		result := NewToolResultText("original")
		tableCtx := &semantic.TableContext{Description: semTestDescTestTable}
		columnsCtx := map[string]*semantic.ColumnContext{
			"id":         {Name: "id"},
			"created_at": {Name: "created_at"},
		}

		enriched, err := appendSemanticContextWithColumns(result, tableCtx, columnsCtx)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2)

		tc := requireTextContent(t, enriched)
		data := requireUnmarshalJSON(t, tc.Text)

		if _, ok := data["column_context"]; ok {
			t.Error("column_context should not exist when all columns are empty")
		}
		note, ok := data["column_context_note"].(string)
		if !ok {
			t.Fatal("expected column_context_note when all columns are filtered")
		}
		if note != "No column-level metadata available" {
			t.Errorf("unexpected note: %q", note)
		}
	})

	t.Run("mix of empty and content columns filters correctly", func(t *testing.T) {
		result := NewToolResultText("original")
		tableCtx := &semantic.TableContext{Description: semTestDescTestTable}
		columnsCtx := map[string]*semantic.ColumnContext{
			"id":     {Name: "id"},
			"email":  {Name: "email", IsPII: true, Description: "User email"},
			"status": {Name: "status"},
			"amount": {Name: "amount", Tags: []string{"financial"}},
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
			t.Errorf("expected 2 columns with content, got %d", len(colCtx))
		}
		if _, ok := colCtx["email"]; !ok {
			t.Error("expected 'email' column in context")
		}
		if _, ok := colCtx["amount"]; !ok {
			t.Error("expected 'amount' column in context")
		}
		if _, ok := colCtx["id"]; ok {
			t.Error("'id' column should be filtered out")
		}
		if _, ok := data["column_context_note"]; ok {
			t.Error("column_context_note should not exist when some columns have content")
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
	refs := []sqltables.Ref{
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
	ref := sqltables.Ref{
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
	ref := sqltables.Ref{FullPath: "catalog.schema.table"}
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

		enriched, err := testEnricher(provider, false).enrichTrinoQueryResult(context.Background(), result, []sqltables.Ref{}, "")
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

		tables := []sqltables.Ref{
			{Catalog: "cat1", Schema: "sch1", Table: "primary", FullPath: "cat1.sch1.primary"},
			{Catalog: "cat2", Schema: "sch2", Table: "secondary", FullPath: "cat2.sch2.secondary"},
		}

		enriched, err := testEnricher(provider, false).enrichTrinoQueryResult(context.Background(), result, tables, "")
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

		tables := []sqltables.Ref{
			{Catalog: "cat1", Schema: "sch1", Table: "primary", FullPath: "cat1.sch1.primary"},
		}

		enriched, err := testEnricher(provider, false).enrichTrinoQueryResult(context.Background(), result, tables, "")
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

		tables := []sqltables.Ref{
			{Catalog: "cat1", Schema: "sch1", Table: "primary", FullPath: "cat1.sch1.primary"},
			{Catalog: "cat2", Schema: "sch2", Table: "secondary", FullPath: "cat2.sch2.secondary"},
		}

		enriched, err := testEnricher(provider, false).enrichTrinoQueryResult(context.Background(), result, tables, "")
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
		if _, ok := data["compact_context"]; !ok {
			t.Error("summary mode: expected compact_context")
		}
		if _, ok := data["note"]; !ok {
			t.Error("summary mode: expected note about compact view")
		}
		if _, ok := data["tables"]; !ok {
			t.Error("summary mode: expected tables list")
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

func TestBuildCompactSemanticContext(t *testing.T) {
	score := 85.0
	ctx := &semantic.TableContext{
		URN:          "urn:li:dataset:test",
		Description:  "Should not appear in compact",
		Owners:       []semantic.Owner{{URN: "urn:li:corpuser:owner1", Type: semantic.OwnerTypeUser}},
		Tags:         []string{"pii_email", "normal_tag", "sensitive_data"},
		Domain:       &semantic.Domain{Name: "Sales"},
		Deprecation:  &semantic.Deprecation{Deprecated: true, Note: "Use v2"},
		QualityScore: &score,
	}

	compact := buildCompactSemanticContext(ctx)

	// Critical fields present
	assert.Equal(t, "urn:li:dataset:test", compact[keyURN])
	assert.NotNil(t, compact["domain"])
	assert.NotNil(t, compact["deprecation"])
	assert.Equal(t, 85.0, compact["quality_score"])
	assert.Equal(t, []string{"pii_email", "sensitive_data"}, compact["critical_tags"])

	// Non-critical fields absent
	assert.Nil(t, compact[keyDescription])
	assert.Nil(t, compact["owners"])
	assert.Nil(t, compact[keyTags])
	assert.Nil(t, compact["glossary_terms"])
}

func TestBuildCompactSemanticContext_MinimalContext(t *testing.T) {
	ctx := &semantic.TableContext{
		Description: "Just a description",
		Tags:        []string{"normal_tag"},
	}

	compact := buildCompactSemanticContext(ctx)
	assert.Empty(t, compact)
}

func TestBuildCompactSemanticContext_NonDeprecated(t *testing.T) {
	ctx := &semantic.TableContext{
		Deprecation: &semantic.Deprecation{Deprecated: false},
	}

	compact := buildCompactSemanticContext(ctx)
	assert.Nil(t, compact["deprecation"])
}

func TestFilterCriticalTags(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		expected []string
	}{
		{
			name:     "pii tag",
			tags:     []string{"pii_email", "normal"},
			expected: []string{"pii_email"},
		},
		{
			name:     "sensitive tag",
			tags:     []string{"Sensitive_Data", "tag2"},
			expected: []string{"Sensitive_Data"},
		},
		{
			name:     "quality tag",
			tags:     []string{"quality_issue"},
			expected: []string{"quality_issue"},
		},
		{
			name:     "restricted tag",
			tags:     []string{"restricted_access"},
			expected: []string{"restricted_access"},
		},
		{
			name:     "confidential tag",
			tags:     []string{"Confidential"},
			expected: []string{"Confidential"},
		},
		{
			name:     "no critical tags",
			tags:     []string{"normal", "tag2", "another"},
			expected: nil,
		},
		{
			name:     "empty tags",
			tags:     nil,
			expected: nil,
		},
		{
			name:     "multiple critical",
			tags:     []string{"pii_phone", "sensitive_ssn", "normal"},
			expected: []string{"pii_phone", "sensitive_ssn"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := filterCriticalTags(tc.tags)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	assert.Equal(t, 0, estimateTokens(nil))
	assert.Equal(t, 0, estimateTokens([]byte("ab")))
	assert.Equal(t, 1, estimateTokens([]byte("abcd")))
	assert.Equal(t, 25, estimateTokens(make([]byte, 100)))
}

func TestMeasureEnrichmentTokens(t *testing.T) {
	assert.Equal(t, 0, measureEnrichmentTokens(100, 100))
	assert.Equal(t, 0, measureEnrichmentTokens(100, 50))
	assert.Equal(t, 25, measureEnrichmentTokens(0, 100))
	assert.Equal(t, 50, measureEnrichmentTokens(100, 300))
}

func TestContentChars(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
			&mcp.TextContent{Text: "world!"},
			&mcp.ImageContent{Data: []byte("image")},
		},
	}
	// Only counts TextContent
	assert.Equal(t, 11, contentChars(result))
}

func TestSumStoredTokenCounts(t *testing.T) {
	cache := NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute)
	cache.MarkSent("s1", "t1", 100)
	cache.MarkSent("s1", "t2", 200)

	assert.Equal(t, 300, sumStoredTokenCounts(cache, "s1", []string{"t1", "t2"}))
	assert.Equal(t, 100, sumStoredTokenCounts(cache, "s1", []string{"t1"}))
	assert.Equal(t, 0, sumStoredTokenCounts(cache, "s1", []string{"t3"}))
}

func TestSessionDedup_SummaryMode_CriticalFieldsPassThrough(t *testing.T) {
	score := 90.0
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					URN:          "urn:li:dataset:test",
					Description:  "Full description that should not appear",
					Tags:         []string{"pii_email", "normal_tag"},
					Domain:       &semantic.Domain{Name: "Sales"},
					Deprecation:  &semantic.Deprecation{Deprecated: true, Note: "Use v2"},
					QualityScore: &score,
					Owners:       []semantic.Owner{{URN: "urn:li:corpuser:owner1", Type: semantic.OwnerTypeUser}},
				}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute),
			DedupMode:          DedupModeSummary,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}

	// First call — full enrichment
	requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)

	// Second call — compact summary
	enriched2 := requireEnrich(t, enricher, NewToolResultText("r2"), request, pc)
	textContent := requireTextContent(t, enriched2)
	data := requireUnmarshalJSON(t, textContent.Text)

	compact, ok := data["compact_context"].(map[string]any)
	if !ok {
		t.Fatal("expected compact_context map")
	}

	// Critical fields present
	assert.Equal(t, "urn:li:dataset:test", compact[keyURN])
	assert.NotNil(t, compact["domain"])
	assert.NotNil(t, compact["deprecation"])
	assert.NotNil(t, compact["quality_score"])
	assert.NotNil(t, compact["critical_tags"])
}

func TestSessionDedup_SummaryMode_OmitsNonCritical(t *testing.T) {
	enricher := &semanticEnricher{
		semanticProvider: &mockSemanticProvider{
			getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
				return &semantic.TableContext{
					Description:      "Full description",
					Owners:           []semantic.Owner{{URN: "urn:li:corpuser:owner1", Type: semantic.OwnerTypeUser}},
					Tags:             []string{"normal_tag"},
					GlossaryTerms:    []semantic.GlossaryTerm{{URN: "urn:li:glossaryTerm:Revenue", Name: "Revenue"}},
					CustomProperties: map[string]string{"key": "value"},
				}, nil
			},
		},
		cfg: EnrichmentConfig{
			EnrichTrinoResults: true,
			SessionCache:       NewSessionEnrichmentCache(5*time.Minute, 30*time.Minute),
			DedupMode:          DedupModeSummary,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	pc := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}

	requireEnrich(t, enricher, NewToolResultText("r1"), request, pc)
	enriched2 := requireEnrich(t, enricher, NewToolResultText("r2"), request, pc)
	textContent := requireTextContent(t, enriched2)
	data := requireUnmarshalJSON(t, textContent.Text)

	compact, ok := data["compact_context"].(map[string]any)
	if !ok {
		t.Fatal("expected compact_context map")
	}

	// Non-critical fields absent
	assert.Nil(t, compact[keyDescription])
	assert.Nil(t, compact["owners"])
	assert.Nil(t, compact["glossary_terms"])
	assert.Nil(t, compact["custom_properties"])
}

func TestSessionDedup_TokenTracking(t *testing.T) {
	enricher := &semanticEnricher{
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
			DedupMode:          DedupModeReference,
		},
	}

	args, _ := json.Marshal(map[string]any{"table": semTestCatalogSchTable})
	request := mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}

	// First call — full enrichment sets EnrichmentTokensFull
	pc1 := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}
	requireEnrich(t, enricher, NewToolResultText("r1"), request, pc1)
	assert.Greater(t, pc1.EnrichmentTokensFull, 0, "full enrichment should set token count")
	assert.Equal(t, 0, pc1.EnrichmentTokensDedup, "first call should not set dedup tokens")

	// Second call — dedup sets both full (stored) and dedup tokens
	pc2 := &PlatformContext{ToolkitKind: semTestTrino, SessionID: "s1"}
	requireEnrich(t, enricher, NewToolResultText("r2"), request, pc2)
	assert.Greater(t, pc2.EnrichmentTokensFull, 0, "dedup should report stored full token count")
	assert.GreaterOrEqual(t, pc2.EnrichmentTokensDedup, 0, "dedup should report dedup token count")
}

func TestFilterColumnsBySQL(t *testing.T) {
	allColumns := map[string]*semantic.ColumnContext{
		"id":               {Name: "id", Description: "Primary key"},
		"name":             {Name: "name", Description: "User name"},
		"email":            {Name: "email", Description: "Email address", IsPII: true},
		"ssn":              {Name: "ssn", Description: "Social security", IsSensitive: true},
		"salary":           {Name: "salary", Description: "Salary", Tags: []string{"confidential"}},
		"location_type_id": {Name: "location_type_id", Description: "Location type"},
		"total_amount":     {Name: "total_amount", Description: "Total"},
		"status":           {Name: "status", Description: "Status"},
		"created_at":       {Name: "created_at", Description: "Created timestamp"},
		"updated_at":       {Name: "updated_at", Description: "Updated timestamp"},
	}

	t.Run("filters to referenced columns", func(t *testing.T) {
		sql := "SELECT id, name, status FROM users"
		filtered := filterColumnsBySQL(allColumns, sql)

		// Referenced columns present
		assert.Contains(t, filtered, "id")
		assert.Contains(t, filtered, "name")
		assert.Contains(t, filtered, "status")

		// PII/sensitive always included
		assert.Contains(t, filtered, "email")
		assert.Contains(t, filtered, "ssn")
		assert.Contains(t, filtered, "salary") // has "confidential" tag

		// Unreferenced non-safety columns excluded
		assert.NotContains(t, filtered, "location_type_id")
		assert.NotContains(t, filtered, "total_amount")
		assert.NotContains(t, filtered, "created_at")
		assert.NotContains(t, filtered, "updated_at")
	})

	t.Run("always includes PII columns", func(t *testing.T) {
		sql := "SELECT id FROM users"
		filtered := filterColumnsBySQL(allColumns, sql)

		assert.Contains(t, filtered, "email")
		assert.Contains(t, filtered, "ssn")
	})

	t.Run("always includes critical tag columns", func(t *testing.T) {
		sql := "SELECT id FROM users"
		filtered := filterColumnsBySQL(allColumns, sql)

		assert.Contains(t, filtered, "salary") // has "confidential" tag
	})

	t.Run("empty SQL returns all when no safety columns", func(t *testing.T) {
		// When no identifiers match and no safety-relevant columns exist,
		// the fallback returns all columns.
		cols := map[string]*semantic.ColumnContext{
			"alpha": {Name: "alpha", Description: "A"},
			"beta":  {Name: "beta", Description: "B"},
		}
		filtered := filterColumnsBySQL(cols, "")
		assert.Equal(t, len(cols), len(filtered))
	})

	t.Run("empty SQL with safety columns returns only safety", func(t *testing.T) {
		// When no identifiers match but safety-relevant columns exist,
		// only those are returned (non-zero matches, no fallback).
		filtered := filterColumnsBySQL(allColumns, "")
		// Only PII (email), sensitive (ssn), and critical-tag (salary) columns
		assert.Contains(t, filtered, "email")
		assert.Contains(t, filtered, "ssn")
		assert.Contains(t, filtered, "salary")
		assert.NotContains(t, filtered, "id")
	})

	t.Run("no matches returns all columns (graceful degradation)", func(t *testing.T) {
		// SQL with no column name matches and no safety-relevant columns
		cols := map[string]*semantic.ColumnContext{
			"alpha": {Name: "alpha", Description: "A"},
			"beta":  {Name: "beta", Description: "B"},
		}
		sql := "SELECT xyz FROM unknown_table"
		filtered := filterColumnsBySQL(cols, sql)
		assert.Equal(t, len(cols), len(filtered))
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		sql := "SELECT ID, Name, STATUS FROM users"
		filtered := filterColumnsBySQL(allColumns, sql)

		assert.Contains(t, filtered, "id")
		assert.Contains(t, filtered, "name")
		assert.Contains(t, filtered, "status")
	})
}

func TestIsSafetyRelevant(t *testing.T) {
	tests := []struct {
		name string
		col  *semantic.ColumnContext
		want bool
	}{
		{"PII column", &semantic.ColumnContext{IsPII: true}, true},
		{"sensitive column", &semantic.ColumnContext{IsSensitive: true}, true},
		{"quality tag", &semantic.ColumnContext{Tags: []string{"quality_issue"}}, true},
		{"pii tag", &semantic.ColumnContext{Tags: []string{"PII"}}, true},
		{"restricted tag", &semantic.ColumnContext{Tags: []string{"Restricted-Data"}}, true},
		{"confidential tag", &semantic.ColumnContext{Tags: []string{"confidential"}}, true},
		{"normal column", &semantic.ColumnContext{Description: "just a column"}, false},
		{"empty column", &semantic.ColumnContext{}, false},
		{"unrelated tags", &semantic.ColumnContext{Tags: []string{"analytics", "core"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSafetyRelevant(tt.col))
		})
	}
}

func TestEnrichTrinoResult_ColumnFiltering(t *testing.T) {
	tenColumns := map[string]*semantic.ColumnContext{
		"id":               {Name: "id", Description: "Primary key"},
		"name":             {Name: "name", Description: "User name"},
		"email":            {Name: "email", Description: "Email address", IsPII: true},
		"status":           {Name: "status", Description: "Status"},
		"location_type_id": {Name: "location_type_id", Description: "Location type"},
		"total_amount":     {Name: "total_amount", Description: "Total"},
		"created_at":       {Name: "created_at", Description: "Created timestamp"},
		"updated_at":       {Name: "updated_at", Description: "Updated timestamp"},
		"category":         {Name: "category", Description: "Category"},
		"region":           {Name: "region", Description: "Region"},
	}

	provider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{Description: semTestDescTestTable}, nil
		},
		getColumnsContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
			return tenColumns, nil
		},
	}

	t.Run("with filtering enabled, only SQL-referenced columns appear", func(t *testing.T) {
		sql := "SELECT id, name, status FROM iceberg.public.users WHERE id > 10"
		args, _ := json.Marshal(map[string]any{"sql": sql})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		result := NewToolResultText("query result")

		enriched, err := testEnricher(provider, true).enrichTrinoResult(context.Background(), result, request, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2) //nolint:mnd // original + enrichment

		tc := requireTextContent(t, enriched)
		data := requireUnmarshalJSON(t, tc.Text)

		colCtx, ok := data["column_context"].(map[string]any)
		assert.True(t, ok, "expected column_context in enrichment")

		// Referenced columns
		assert.Contains(t, colCtx, "id")
		assert.Contains(t, colCtx, "name")
		assert.Contains(t, colCtx, "status")

		// PII always included
		assert.Contains(t, colCtx, "email")

		// Unreferenced non-safety columns excluded
		assert.NotContains(t, colCtx, "location_type_id")
		assert.NotContains(t, colCtx, "total_amount")
		assert.NotContains(t, colCtx, "created_at")
		assert.NotContains(t, colCtx, "updated_at")
		assert.NotContains(t, colCtx, "category")
		assert.NotContains(t, colCtx, "region")
	})

	t.Run("with filtering disabled, all columns appear", func(t *testing.T) {
		sql := "SELECT id, name, status FROM iceberg.public.users"
		args, _ := json.Marshal(map[string]any{"sql": sql})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		result := NewToolResultText("query result")

		enriched, err := testEnricher(provider, false).enrichTrinoResult(context.Background(), result, request, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2) //nolint:mnd // original + enrichment

		tc := requireTextContent(t, enriched)
		data := requireUnmarshalJSON(t, tc.Text)

		colCtx, ok := data["column_context"].(map[string]any)
		assert.True(t, ok, "expected column_context in enrichment")

		// All 10 columns should be present
		assert.Len(t, colCtx, len(tenColumns))
	})

	t.Run("describe_table path (no SQL) always shows all columns", func(t *testing.T) {
		args, _ := json.Marshal(map[string]any{
			"catalog": "iceberg",
			"schema":  "public",
			"table":   "users",
		})
		request := mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Arguments: args},
		}
		result := NewToolResultText("describe result")

		enriched, err := testEnricher(provider, true).enrichTrinoResult(context.Background(), result, request, nil)
		requireNoErr(t, err)
		requireContentLen(t, enriched, 2) //nolint:mnd // original + enrichment

		tc := requireTextContent(t, enriched)
		data := requireUnmarshalJSON(t, tc.Text)

		colCtx, ok := data["column_context"].(map[string]any)
		assert.True(t, ok, "expected column_context in enrichment")

		// All 10 columns should be present (no SQL filtering on describe path)
		assert.Len(t, colCtx, len(tenColumns))
	})
}

// ---------------------------------------------------------------------------
// Structured properties, incidents and data contract enrichment fields
// ---------------------------------------------------------------------------

func TestBuildTrinoSemanticContext_V14Fields(t *testing.T) {
	ctx := &semantic.TableContext{
		Description: "test",
		StructuredProperties: []semantic.StructuredProperty{
			{QualifiedName: "retention.days", Values: []any{float64(90)}},
		},
		ActiveIncidents: 2,
		Incidents: []semantic.Incident{
			{URN: "urn:li:incident:1", Type: "OPERATIONAL", Title: "Down", State: "ACTIVE"},
			{URN: "urn:li:incident:2", Type: "CUSTOM", Title: "Drift", State: "ACTIVE"},
		},
		DataContract: &semantic.DataContractStatus{
			Status: "FAILING",
			AssertionResults: []semantic.AssertionResult{
				{AssertionURN: "urn:li:assertion:freshness-1", Type: "FRESHNESS"},
			},
		},
	}

	result := buildTrinoSemanticContext(ctx)

	// Structured properties
	props, ok := result["structured_properties"].([]semantic.StructuredProperty)
	assert.True(t, ok, "expected structured_properties")
	assert.Len(t, props, 1)
	assert.Equal(t, "retention.days", props[0].QualifiedName)

	// Incidents
	assert.Equal(t, 2, result["active_incidents"])
	incidents, ok := result["incidents"].([]semantic.Incident)
	assert.True(t, ok, "expected incidents")
	assert.Len(t, incidents, 2)

	// Data contract
	dc, ok := result["data_contract"].(*semantic.DataContractStatus)
	assert.True(t, ok, "expected data_contract")
	assert.Equal(t, "FAILING", dc.Status)
}

func TestBuildTrinoSemanticContext_V14Fields_Absent(t *testing.T) {
	// A context carrying none of those fields
	ctx := &semantic.TableContext{Description: "legacy"}

	result := buildTrinoSemanticContext(ctx)

	assert.Nil(t, result["structured_properties"])
	assert.Nil(t, result["active_incidents"])
	assert.Nil(t, result["incidents"])
	assert.Nil(t, result["data_contract"])
}

func TestBuildCompactSemanticContext_V14Fields(t *testing.T) {
	t.Run("incidents appear in compact", func(t *testing.T) {
		ctx := &semantic.TableContext{
			ActiveIncidents: 1,
		}
		compact := buildCompactSemanticContext(ctx)
		assert.Equal(t, 1, compact["active_incidents"])
	})

	t.Run("failing contract appears in compact", func(t *testing.T) {
		ctx := &semantic.TableContext{
			DataContract: &semantic.DataContractStatus{Status: "FAILING"},
		}
		compact := buildCompactSemanticContext(ctx)
		assert.NotNil(t, compact["data_contract"])
	})

	t.Run("passing contract omitted from compact", func(t *testing.T) {
		ctx := &semantic.TableContext{
			DataContract: &semantic.DataContractStatus{Status: "PASSING"},
		}
		compact := buildCompactSemanticContext(ctx)
		assert.Nil(t, compact["data_contract"])
	})
}

func TestBuildAdditionalTableContext_V14Fields(t *testing.T) {
	ref := sqltables.Ref{FullPath: "catalog.schema.table"}
	ctx := &semantic.TableContext{
		Description:     "test",
		ActiveIncidents: 1,
		DataContract:    &semantic.DataContractStatus{Status: "FAILING"},
	}

	result := buildAdditionalTableContext(ref, ctx)
	assert.Equal(t, 1, result["active_incidents"])
	assert.NotNil(t, result["data_contract"])
}

func TestProvenQueriesDatasetURNUsesThePlatformsBuilder(t *testing.T) {
	e := &semanticEnricher{cfg: EnrichmentConfig{
		DatasetURN: func(kind, conn, cat, schema, table string) string {
			return kind + "|" + conn + "|" + cat + "." + schema + "." + table
		},
	}}
	assert.Equal(t, "trino|w|hive.sales.orders",
		e.datasetURN(semantic.TableIdentifier{Catalog: "hive", Schema: "sales", Table: "orders"}, "trino", "w"))
}
