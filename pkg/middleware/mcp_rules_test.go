package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// Test constants for rule enforcement tests.
const (
	rulesTestMethodToolsCall = "tools/call"
	rulesTestToolTrinoQuery  = "trino_query"
)

func TestMCPRuleEnforcementMiddleware_StaticFallback(t *testing.T) {
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
			method:         rulesTestMethodToolsCall,
			toolName:       rulesTestToolTrinoQuery,
			requireCheck:   true,
			expectHint:     true,
			nextResult:     &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}}},
			expectNextCall: true,
		},
		{
			name:           "query tool without require_datahub_check no hint",
			method:         rulesTestMethodToolsCall,
			toolName:       rulesTestToolTrinoQuery,
			requireCheck:   false,
			expectHint:     false,
			nextResult:     &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}}},
			expectNextCall: true,
		},
		{
			name:           "non-query tool no hint",
			method:         rulesTestMethodToolsCall,
			toolName:       "datahub_search",
			requireCheck:   true,
			expectHint:     false,
			nextResult:     &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}}},
			expectNextCall: true,
		},
		{
			name:           "error result does not get hint",
			method:         rulesTestMethodToolsCall,
			toolName:       rulesTestToolTrinoQuery,
			requireCheck:   true,
			expectHint:     false,
			nextResult:     &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "error"}}},
			expectNextCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := &tuning.Rules{
				RequireDataHubCheck: tt.requireCheck,
			}
			engine := tuning.NewRuleEngine(rules)

			nextCalled := false

			cfg := RuleEnforcementConfig{Engine: engine}
			mw := MCPRuleEnforcementMiddleware(cfg)
			handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
				nextCalled = true
				return tt.nextResult, tt.nextErr
			})

			ctx := context.Background()
			if tt.toolName != "" {
				pc := NewPlatformContext("test-req")
				pc.ToolName = tt.toolName
				ctx = WithPlatformContext(ctx, pc)
			}

			result, err := handler(ctx, tt.method, nil)

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

func TestMCPRuleEnforcementMiddleware_SessionAware(t *testing.T) {
	t.Run("query without discovery gets warning", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

		cfg := RuleEnforcementConfig{
			WorkflowTracker: tracker,
			WorkflowConfig: WorkflowRulesConfig{
				RequireDiscoveryBeforeQuery: true,
			},
		}
		mw := MCPRuleEnforcementMiddleware(cfg)
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "data"}}}, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = rulesTestToolTrinoQuery
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, rulesTestMethodToolsCall, nil)
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.GreaterOrEqual(t, len(callResult.Content), 2)
		textContent, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "datahub_search")
	})

	t.Run("query after discovery gets no warning", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)
		tracker.RecordToolCall("s1", "datahub_search")

		cfg := RuleEnforcementConfig{
			WorkflowTracker: tracker,
			WorkflowConfig: WorkflowRulesConfig{
				RequireDiscoveryBeforeQuery: true,
			},
		}
		mw := MCPRuleEnforcementMiddleware(cfg)
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "data"}}}, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = rulesTestToolTrinoQuery
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, rulesTestMethodToolsCall, nil)
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		assert.Len(t, callResult.Content, 1, "no hint should be prepended after discovery")
	})

	t.Run("escalation after threshold", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

		cfg := RuleEnforcementConfig{
			WorkflowTracker: tracker,
			WorkflowConfig: WorkflowRulesConfig{
				RequireDiscoveryBeforeQuery: true,
				EscalationAfterWarnings:     2, // escalate after 2
			},
		}
		mw := MCPRuleEnforcementMiddleware(cfg)
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "data"}}}, nil
		})

		// First 2 calls get standard warning
		for i := range 2 {
			pc := NewPlatformContext("test-req")
			pc.ToolName = rulesTestToolTrinoQuery
			pc.SessionID = "s1"
			ctx := WithPlatformContext(context.Background(), pc)

			result, err := handler(ctx, rulesTestMethodToolsCall, nil)
			require.NoError(t, err)

			callResult, ok := result.(*mcp.CallToolResult)
			require.True(t, ok)
			textContent, ok := callResult.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Contains(t, textContent.Text, "REQUIRED", "call %d should have standard warning", i+1)
			assert.NotContains(t, textContent.Text, "MANDATORY", "call %d should not have escalation", i+1)
		}

		// 3rd call gets escalated message
		pc := NewPlatformContext("test-req")
		pc.ToolName = rulesTestToolTrinoQuery
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, rulesTestMethodToolsCall, nil)
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		textContent, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "MANDATORY")
		assert.Contains(t, textContent.Text, "3") // warning count
	})

	t.Run("nil tracker falls back to static", func(t *testing.T) {
		rules := &tuning.Rules{RequireDataHubCheck: true}
		engine := tuning.NewRuleEngine(rules)

		cfg := RuleEnforcementConfig{
			Engine:          engine,
			WorkflowTracker: nil,
		}
		mw := MCPRuleEnforcementMiddleware(cfg)
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "data"}}}, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = rulesTestToolTrinoQuery
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, rulesTestMethodToolsCall, nil)
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.GreaterOrEqual(t, len(callResult.Content), 2)
		textContent, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "datahub_search")
	})

	t.Run("custom warning message", func(t *testing.T) {
		tracker := NewSessionWorkflowTracker(nil, nil, 30*time.Minute)

		cfg := RuleEnforcementConfig{
			WorkflowTracker: tracker,
			WorkflowConfig: WorkflowRulesConfig{
				RequireDiscoveryBeforeQuery: true,
				WarningMessage:              "Custom warning: discover first!",
			},
		}
		mw := MCPRuleEnforcementMiddleware(cfg)
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "data"}}}, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = rulesTestToolTrinoQuery
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, rulesTestMethodToolsCall, nil)
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		textContent, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, textContent.Text, "Custom warning: discover first!")
	})
}

func TestFormatEscalationMessage(t *testing.T) {
	msg := formatEscalationMessage("Warning #{count}: stop now! ({count} queries)", 5)
	assert.Equal(t, "Warning #5: stop now! (5 queries)", msg)
}

func TestEffectiveEscalationMessage(t *testing.T) {
	t.Run("custom message", func(t *testing.T) {
		cfg := WorkflowRulesConfig{EscalationMessage: "custom escalation"}
		assert.Equal(t, "custom escalation", effectiveEscalationMessage(cfg))
	})
	t.Run("default message", func(t *testing.T) {
		cfg := WorkflowRulesConfig{}
		assert.Equal(t, DefaultEscalationMessage, effectiveEscalationMessage(cfg))
	})
}

func TestIsQueryTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		{rulesTestToolTrinoQuery, true},
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
		callResult, ok := modified.(*mcp.CallToolResult)
		require.True(t, ok, "expected *CallToolResult, got %T", modified)

		require.Len(t, callResult.Content, 2)
		textContent, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok, "expected *TextContent, got %T", callResult.Content[0])
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
		callResult, ok := modified.(*mcp.CallToolResult)
		require.True(t, ok, "expected *CallToolResult, got %T", modified)

		require.Len(t, callResult.Content, 1)
	})

	t.Run("handles nil result", func(t *testing.T) {
		var result mcp.Result
		modified := prependHintsToResult(result, []string{"hint"})
		assert.Nil(t, modified)
	})
}
