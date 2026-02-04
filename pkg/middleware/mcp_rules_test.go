package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

func TestMCPRuleEnforcementMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		toolName       string
		requireCheck   bool
		expectHint     bool
		nextResult     mcp.Result
		nextErr        error
		expectNextCall bool
	}{
		{
			name:           "non-tools/call passes through",
			method:         "resources/list",
			toolName:       "",
			requireCheck:   true,
			expectHint:     false,
			nextResult:     &mcp.ListResourcesResult{},
			expectNextCall: true,
		},
		{
			name:           "query tool with require_datahub_check gets hint",
			method:         "tools/call",
			toolName:       "trino_query",
			requireCheck:   true,
			expectHint:     true,
			nextResult:     &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}}},
			expectNextCall: true,
		},
		{
			name:           "query tool without require_datahub_check no hint",
			method:         "tools/call",
			toolName:       "trino_query",
			requireCheck:   false,
			expectHint:     false,
			nextResult:     &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}}},
			expectNextCall: true,
		},
		{
			name:           "non-query tool no hint",
			method:         "tools/call",
			toolName:       "datahub_search",
			requireCheck:   true,
			expectHint:     false,
			nextResult:     &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}}},
			expectNextCall: true,
		},
		{
			name:           "error result does not get hint",
			method:         "tools/call",
			toolName:       "trino_query",
			requireCheck:   true,
			expectHint:     false,
			nextResult:     &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "error"}}},
			expectNextCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create rule engine
			rules := &tuning.Rules{
				RequireDataHubCheck: tt.requireCheck,
			}
			engine := tuning.NewRuleEngine(rules)
			hints := tuning.NewHintManager()

			// Track if next was called
			nextCalled := false

			// Create middleware
			mw := MCPRuleEnforcementMiddleware(engine, hints)
			handler := mw(func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return tt.nextResult, tt.nextErr
			})

			// Create context with platform context
			ctx := context.Background()
			if tt.toolName != "" {
				pc := NewPlatformContext("test-req")
				pc.ToolName = tt.toolName
				ctx = WithPlatformContext(ctx, pc)
			}

			// Execute
			result, err := handler(ctx, tt.method, nil)

			// Assertions
			assert.Equal(t, tt.expectNextCall, nextCalled, "next handler call mismatch")
			assert.Equal(t, tt.nextErr, err)

			if tt.expectHint {
				callResult, ok := result.(*mcp.CallToolResult)
				require.True(t, ok)
				require.GreaterOrEqual(t, len(callResult.Content), 2, "expected hint prepended")
				textContent, ok := callResult.Content[0].(*mcp.TextContent)
				require.True(t, ok)
				assert.Contains(t, textContent.Text, "datahub_search")
			}
		})
	}
}

func TestIsQueryTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		{"trino_query", true},
		{"trino_execute", true},
		{"trino_describe_table", false},
		{"datahub_search", false},
		{"s3_list_objects", false},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			assert.Equal(t, tt.expected, isQueryTool(tt.toolName))
		})
	}
}

func TestPrependHintsToResult(t *testing.T) {
	t.Run("adds hints to successful result", func(t *testing.T) {
		result := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "original"}},
		}
		hints := []string{"hint1", "hint2"}

		modified := prependHintsToResult(result, hints)
		callResult := modified.(*mcp.CallToolResult)

		require.Len(t, callResult.Content, 2)
		textContent := callResult.Content[0].(*mcp.TextContent)
		assert.Contains(t, textContent.Text, "hint1")
		assert.Contains(t, textContent.Text, "hint2")
	})

	t.Run("does not modify error result", func(t *testing.T) {
		result := &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "error"}},
		}
		hints := []string{"hint"}

		modified := prependHintsToResult(result, hints)
		callResult := modified.(*mcp.CallToolResult)

		require.Len(t, callResult.Content, 1)
	})

	t.Run("handles nil result", func(t *testing.T) {
		var result mcp.Result = nil
		modified := prependHintsToResult(result, []string{"hint"})
		assert.Nil(t, modified)
	})
}
