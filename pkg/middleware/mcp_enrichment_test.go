package middleware

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestInferToolkitKind(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected string
	}{
		{"trino tool", "trino_query", "trino"},
		{"trino describe", "trino_describe_table", "trino"},
		{"datahub tool", "datahub_search", "datahub"},
		{"datahub entity", "datahub_get_entity", "datahub"},
		{"s3 tool", "s3_list_buckets", "s3"},
		{"s3 get", "s3_get_object", "s3"},
		{"unknown tool", "unknown_tool", ""},
		{"no prefix", "query", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferToolkitKind(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMCPSemanticEnrichmentMiddleware_NonToolsCallPassthrough(t *testing.T) {
	// Create middleware with nil providers (should be fine for non-tools/call)
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{})

	// Mock handler that tracks calls
	handlerCalled := false
	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		handlerCalled = true
		return &mcp.ListResourcesResult{}, nil
	}

	// Wrap handler
	wrapped := mw(mockHandler)

	// Call with non-tools/call method
	result, err := wrapped(context.Background(), "resources/list", nil)

	require.NoError(t, err)
	assert.True(t, handlerCalled)
	assert.IsType(t, &mcp.ListResourcesResult{}, result)
}

func TestMCPSemanticEnrichmentMiddleware_ErrorPassthrough(t *testing.T) {
	// Create middleware
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{})

	// Mock handler that returns error
	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return nil, assert.AnError
	}

	wrapped := mw(mockHandler)

	// Create mock request
	req := createMockToolRequest(t, "trino_query", map[string]any{})

	result, err := wrapped(context.Background(), "tools/call", req)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestMCPSemanticEnrichmentMiddleware_IsErrorPassthrough(t *testing.T) {
	// Create middleware
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{})

	// Mock handler that returns error result
	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "error message"}},
		}, nil
	}

	wrapped := mw(mockHandler)
	req := createMockToolRequest(t, "trino_query", map[string]any{})

	result, err := wrapped(context.Background(), "tools/call", req)

	require.NoError(t, err)
	callResult := result.(*mcp.CallToolResult)
	assert.True(t, callResult.IsError)
	assert.Len(t, callResult.Content, 1) // No enrichment added
}

func TestMCPSemanticEnrichmentMiddleware_TrinoEnrichment(t *testing.T) {
	// Create mock semantic provider
	mockProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{
			URN:         "urn:li:dataset:(urn:li:dataPlatform:postgres,test.public.users,PROD)",
			Description: "User accounts table",
			Tags:        []string{"pii", "important"},
			Owners:      []string{"owner@example.com"},
		},
	}

	// Create middleware with enrichment enabled
	mw := MCPSemanticEnrichmentMiddleware(
		mockProvider, nil, nil,
		EnrichmentConfig{EnrichTrinoResults: true},
	)

	// Mock handler returns basic Trino result
	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "column_name | type\n-----------\nid | bigint"},
			},
		}, nil
	}

	wrapped := mw(mockHandler)

	// Create request for trino_describe_table
	req := createMockToolRequest(t, "trino_describe_table", map[string]any{
		"catalog": "test",
		"schema":  "public",
		"table":   "users",
	})

	result, err := wrapped(context.Background(), "tools/call", req)

	require.NoError(t, err)
	callResult := result.(*mcp.CallToolResult)

	// Should have original content plus enrichment
	require.Len(t, callResult.Content, 2)

	// Verify enrichment content
	enrichmentText := callResult.Content[1].(*mcp.TextContent).Text
	assert.Contains(t, enrichmentText, "semantic_context")
	assert.Contains(t, enrichmentText, "User accounts table")
}

func TestMCPSemanticEnrichmentMiddleware_DataHubEnrichment(t *testing.T) {
	// Create mock query provider
	mockProvider := &mockQueryProvider{
		availability: &query.TableAvailability{
			QueryExecutor:  "trino",
			ExecutorConfig: "rdbms",
			CanQuery:       true,
			RowCount:       1000,
		},
	}

	// Create middleware with enrichment enabled
	mw := MCPSemanticEnrichmentMiddleware(
		nil, mockProvider, nil,
		EnrichmentConfig{EnrichDataHubResults: true},
	)

	// Mock handler returns DataHub search result with URN
	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		resultJSON := `{"results":[{"urn":"urn:li:dataset:(urn:li:dataPlatform:postgres,test,PROD)"}]}`
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: resultJSON}},
		}, nil
	}

	wrapped := mw(mockHandler)

	req := createMockToolRequest(t, "datahub_search", map[string]any{
		"query": "test",
	})

	result, err := wrapped(context.Background(), "tools/call", req)

	require.NoError(t, err)
	callResult := result.(*mcp.CallToolResult)

	// Should have original content plus query context enrichment
	require.Len(t, callResult.Content, 2)

	// Verify query context enrichment
	enrichmentText := callResult.Content[1].(*mcp.TextContent).Text
	assert.Contains(t, enrichmentText, "query_context")
}

func TestMCPSemanticEnrichmentMiddleware_UnknownToolPassthrough(t *testing.T) {
	// Create middleware
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{
		EnrichTrinoResults:   true,
		EnrichDataHubResults: true,
		EnrichS3Results:      true,
	})

	// Mock handler
	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	// Use unknown tool name
	req := createMockToolRequest(t, "custom_tool", map[string]any{})

	result, err := wrapped(context.Background(), "tools/call", req)

	require.NoError(t, err)
	callResult := result.(*mcp.CallToolResult)
	assert.Len(t, callResult.Content, 1) // No enrichment added
}

func TestMCPSemanticEnrichmentMiddleware_DisabledEnrichment(t *testing.T) {
	mockProvider := &mockSemanticProvider{
		tableContext: &semantic.TableContext{Description: "Test"},
	}

	// Create middleware with enrichment DISABLED
	mw := MCPSemanticEnrichmentMiddleware(
		mockProvider, nil, nil,
		EnrichmentConfig{EnrichTrinoResults: false},
	)

	mockHandler := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	req := createMockToolRequest(t, "trino_describe_table", map[string]any{
		"catalog": "test",
		"schema":  "public",
		"table":   "users",
	})

	result, err := wrapped(context.Background(), "tools/call", req)

	require.NoError(t, err)
	callResult := result.(*mcp.CallToolResult)
	assert.Len(t, callResult.Content, 1) // No enrichment because disabled
}

func TestBuildCallToolRequest(t *testing.T) {
	req := createMockToolRequest(t, "test_tool", map[string]any{
		"key": "value",
	})

	callReq := buildCallToolRequest(req)

	assert.Equal(t, "test_tool", callReq.Params.Name)

	// Verify arguments were preserved
	var args map[string]any
	err := json.Unmarshal(callReq.Params.Arguments, &args)
	require.NoError(t, err)
	assert.Equal(t, "value", args["key"])
}

func TestBuildCallToolRequest_NilParams(t *testing.T) {
	callReq := buildCallToolRequest(nil)
	assert.Equal(t, "", callReq.Params.Name)
	assert.Nil(t, callReq.Params.Arguments)
}

// Helper to create mock tool requests
func createMockToolRequest(t *testing.T, toolName string, args map[string]any) *mockMCPRequest {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)

	return &mockMCPRequest{
		params: &mcp.CallToolParamsRaw{
			Name:      toolName,
			Arguments: argsJSON,
		},
	}
}

// mockMCPRequest implements mcp.Request for testing
type mockMCPRequest struct {
	params *mcp.CallToolParamsRaw
}

func (m *mockMCPRequest) GetParams() mcp.Params {
	if m == nil || m.params == nil {
		return nil
	}
	return m.params
}

func (m *mockMCPRequest) GetMeta() *mcp.RequestMeta {
	return nil
}

// mockSemanticProvider implements semantic.Provider for testing
type mockSemanticProvider struct {
	tableContext   *semantic.TableContext
	columnContext  *semantic.ColumnContext
	columnsContext map[string]*semantic.ColumnContext
	lineageInfo    *semantic.LineageInfo
	glossaryTerm   *semantic.GlossaryTerm
	searchResults  []semantic.TableSearchResult
	err            error
}

func (m *mockSemanticProvider) Name() string {
	return "mock"
}

func (m *mockSemanticProvider) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	return m.tableContext, m.err
}

func (m *mockSemanticProvider) GetColumnContext(ctx context.Context, column semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return m.columnContext, m.err
}

func (m *mockSemanticProvider) GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return m.columnsContext, m.err
}

func (m *mockSemanticProvider) GetLineage(ctx context.Context, table semantic.TableIdentifier, direction semantic.LineageDirection, maxDepth int) (*semantic.LineageInfo, error) {
	return m.lineageInfo, m.err
}

func (m *mockSemanticProvider) GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error) {
	return m.glossaryTerm, m.err
}

func (m *mockSemanticProvider) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return m.searchResults, m.err
}

func (m *mockSemanticProvider) Close() error {
	return nil
}

// mockQueryProvider implements query.Provider for testing
type mockQueryProvider struct {
	availability *query.TableAvailability
	examples     []query.QueryExample
	execContext  *query.ExecutionContext
	tableSchema  *query.TableSchema
	tableID      *query.TableIdentifier
	err          error
}

func (m *mockQueryProvider) Name() string {
	return "mock"
}

func (m *mockQueryProvider) ResolveTable(ctx context.Context, urn string) (*query.TableIdentifier, error) {
	return m.tableID, m.err
}

func (m *mockQueryProvider) GetTableAvailability(ctx context.Context, urn string) (*query.TableAvailability, error) {
	return m.availability, m.err
}

func (m *mockQueryProvider) GetQueryExamples(ctx context.Context, urn string) ([]query.QueryExample, error) {
	return m.examples, m.err
}

func (m *mockQueryProvider) GetExecutionContext(ctx context.Context, urns []string) (*query.ExecutionContext, error) {
	return m.execContext, m.err
}

func (m *mockQueryProvider) GetTableSchema(ctx context.Context, table query.TableIdentifier) (*query.TableSchema, error) {
	return m.tableSchema, m.err
}

func (m *mockQueryProvider) Close() error {
	return nil
}
