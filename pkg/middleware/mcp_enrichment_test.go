package middleware

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Note: The mockSemanticProvider and mockQueryProvider types used in these tests
// are defined in semantic_test.go to avoid duplication.

// Test constants for enrichment tests.
const (
	enrichTestMethodToolsCall = "tools/call"
	enrichTestCallToolFmt     = "expected *mcp.CallToolResult, got %T"
	enrichTestRowCount        = 1000
	enrichTestDescribeTable   = "trino_describe_table"
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
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
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
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, assert.AnError
	}

	wrapped := mw(mockHandler)

	// Create mock request using ServerRequest
	req := createServerRequest(t, "trino_query", map[string]any{})

	result, err := wrapped(context.Background(), enrichTestMethodToolsCall, req)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestMCPSemanticEnrichmentMiddleware_IsErrorPassthrough(t *testing.T) {
	// Create middleware
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{})

	// Mock handler that returns error result
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "error message"}},
		}, nil
	}

	wrapped := mw(mockHandler)
	req := createServerRequest(t, "trino_query", map[string]any{})

	result, err := wrapped(context.Background(), enrichTestMethodToolsCall, req)

	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, enrichTestCallToolFmt, result)
	assert.True(t, callResult.IsError)
	assert.Len(t, callResult.Content, 1) // No enrichment added
}

func TestMCPSemanticEnrichmentMiddleware_TrinoEnrichment(t *testing.T) {
	// Create mock semantic provider
	mockProvider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{
				URN:         "urn:li:dataset:(urn:li:dataPlatform:postgres,test.public.users,PROD)",
				Description: "User accounts table",
				Tags:        []string{"pii", "important"},
				Owners: []semantic.Owner{
					{URN: "owner@example.com", Name: "owner@example.com", Type: "user"},
				},
			}, nil
		},
	}

	// Create middleware with enrichment enabled
	mw := MCPSemanticEnrichmentMiddleware(
		mockProvider, nil, nil,
		EnrichmentConfig{EnrichTrinoResults: true},
	)

	// Mock handler returns basic Trino result
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "column_name | type\n-----------\nid | bigint"},
			},
		}, nil
	}

	wrapped := mw(mockHandler)

	// Create request for trino_describe_table
	req := createServerRequest(t, enrichTestDescribeTable, map[string]any{
		"catalog": "test",
		"schema":  "public",
		"table":   "users",
	})

	result, err := wrapped(context.Background(), enrichTestMethodToolsCall, req)

	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, enrichTestCallToolFmt, result)

	// Should have original content plus enrichment.
	require.Len(t, callResult.Content, 2)

	// Verify enrichment content.
	tc, ok := callResult.Content[1].(*mcp.TextContent)
	require.True(t, ok, "expected *TextContent, got %T", callResult.Content[1])
	assert.Contains(t, tc.Text, "semantic_context")
	assert.Contains(t, tc.Text, "User accounts table")
}

func TestMCPSemanticEnrichmentMiddleware_DataHubEnrichment(t *testing.T) {
	rowCount := int64(enrichTestRowCount)
	// Create mock query provider
	mockProvider := &mockQueryProvider{
		getTableAvailabilityFunc: func(_ context.Context, _ string) (*query.TableAvailability, error) {
			return &query.TableAvailability{
				Available:     true,
				QueryTable:    "rdbms.public.test",
				Connection:    "trino",
				EstimatedRows: &rowCount,
			}, nil
		},
	}

	// Create middleware with enrichment enabled
	mw := MCPSemanticEnrichmentMiddleware(
		nil, mockProvider, nil,
		EnrichmentConfig{EnrichDataHubResults: true},
	)

	// Mock handler returns DataHub search result with URN
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		resultJSON := `{"results":[{"urn":"urn:li:dataset:(urn:li:dataPlatform:postgres,test,PROD)"}]}`
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: resultJSON}},
		}, nil
	}

	wrapped := mw(mockHandler)

	req := createServerRequest(t, "datahub_search", map[string]any{
		"query": "test",
	})

	result, err := wrapped(context.Background(), enrichTestMethodToolsCall, req)

	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, enrichTestCallToolFmt, result)

	// Should have original content plus query context enrichment.
	require.Len(t, callResult.Content, 2)

	// Verify query context enrichment.
	tc, ok := callResult.Content[1].(*mcp.TextContent)
	require.True(t, ok, "expected *TextContent, got %T", callResult.Content[1])
	assert.Contains(t, tc.Text, "query_context")
}

func TestMCPSemanticEnrichmentMiddleware_UnknownToolPassthrough(t *testing.T) {
	// Create middleware
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{
		EnrichTrinoResults:   true,
		EnrichDataHubResults: true,
		EnrichS3Results:      true,
	})

	// Mock handler
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	// Use unknown tool name
	req := createServerRequest(t, "custom_tool", map[string]any{})

	result, err := wrapped(context.Background(), enrichTestMethodToolsCall, req)

	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, enrichTestCallToolFmt, result)
	assert.Len(t, callResult.Content, 1) // No enrichment added
}

func TestMCPSemanticEnrichmentMiddleware_DisabledEnrichment(t *testing.T) {
	mockProvider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{Description: "Test"}, nil
		},
	}

	// Create middleware with enrichment DISABLED
	mw := MCPSemanticEnrichmentMiddleware(
		mockProvider, nil, nil,
		EnrichmentConfig{EnrichTrinoResults: false},
	)

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	req := createServerRequest(t, enrichTestDescribeTable, map[string]any{
		"catalog": "test",
		"schema":  "public",
		"table":   "users",
	})

	result, err := wrapped(context.Background(), enrichTestMethodToolsCall, req)

	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, enrichTestCallToolFmt, result)
	assert.Len(t, callResult.Content, 1) // No enrichment because disabled
}

func TestBuildCallToolRequest(t *testing.T) {
	req := createServerRequest(t, "test_tool", map[string]any{
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
	assert.Nil(t, callReq.Params)
}

func TestMCPSemanticEnrichmentMiddleware_SetsEnrichmentApplied(t *testing.T) {
	// Create mock semantic provider that returns table context
	mockProvider := &mockSemanticProvider{
		getTableContextFunc: func(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
			return &semantic.TableContext{
				Description: "Test table",
				Owners:      []semantic.Owner{{Name: "team", Type: "group"}},
			}, nil
		},
	}

	mw := MCPSemanticEnrichmentMiddleware(
		mockProvider, nil, nil,
		EnrichmentConfig{EnrichTrinoResults: true},
	)

	// Mock handler
	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "original"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	// Set up PlatformContext to check the flag
	pc := NewPlatformContext("req-enrich")
	ctx := WithPlatformContext(context.Background(), pc)

	req := createServerRequest(t, enrichTestDescribeTable, map[string]any{
		"catalog": "c", "schema": "s", "table": "t",
	})

	_, err := wrapped(ctx, enrichTestMethodToolsCall, req)
	require.NoError(t, err)

	assert.True(t, pc.EnrichmentApplied, "EnrichmentApplied should be true after enrichment")
}

func TestMCPSemanticEnrichmentMiddleware_NoEnrichmentAppliedForUnknownTool(t *testing.T) {
	mw := MCPSemanticEnrichmentMiddleware(nil, nil, nil, EnrichmentConfig{
		EnrichTrinoResults: true,
	})

	mockHandler := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
		}, nil
	}

	wrapped := mw(mockHandler)

	pc := NewPlatformContext("req-no-enrich")
	ctx := WithPlatformContext(context.Background(), pc)

	req := createServerRequest(t, "custom_tool", map[string]any{})

	_, err := wrapped(ctx, enrichTestMethodToolsCall, req)
	require.NoError(t, err)

	assert.False(t, pc.EnrichmentApplied, "EnrichmentApplied should be false for non-enrichable tool")
}

func TestAppendDiscoveryNoteIfNeeded(t *testing.T) {
	t.Run("no discovery appends note", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)
		pc := NewPlatformContext("req")
		pc.SessionID = "s1"
		pc.EnrichmentApplied = true

		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "data"}},
		}
		appendDiscoveryNoteIfNeeded(result, pc, tracker)

		require.Len(t, result.Content, 2)
		tc, ok := result.Content[1].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, tc.Text, "discovery_note")
		assert.Contains(t, tc.Text, "datahub_search")
	})

	t.Run("discovery done skips note", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)
		tracker.RecordToolCall("s1", "datahub_search")

		pc := NewPlatformContext("req")
		pc.SessionID = "s1"
		pc.EnrichmentApplied = true

		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "data"}},
		}
		appendDiscoveryNoteIfNeeded(result, pc, tracker)

		assert.Len(t, result.Content, 1, "no note after discovery")
	})

	t.Run("no enrichment applied skips note", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)
		pc := NewPlatformContext("req")
		pc.SessionID = "s1"
		pc.EnrichmentApplied = false

		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "data"}},
		}
		appendDiscoveryNoteIfNeeded(result, pc, tracker)

		assert.Len(t, result.Content, 1, "no note when enrichment not applied")
	})

	t.Run("nil tracker is no-op", func(t *testing.T) {
		pc := NewPlatformContext("req")
		pc.SessionID = "s1"
		pc.EnrichmentApplied = true

		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "data"}},
		}
		appendDiscoveryNoteIfNeeded(result, pc, nil)

		assert.Len(t, result.Content, 1, "no note with nil tracker")
	})
}

// Helper to create ServerRequest for testing.
func createServerRequest(t *testing.T, toolName string, args map[string]any) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	t.Helper()
	var argsJSON json.RawMessage
	if args != nil {
		var err error
		argsJSON, err = json.Marshal(args)
		require.NoError(t, err)
	}

	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{
			Name:      toolName,
			Arguments: argsJSON,
		},
	}
}
