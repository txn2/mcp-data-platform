package middleware

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// MCPRuleEnforcementMiddleware creates MCP protocol-level middleware that enforces
// operational rules and adds guidance to tool responses.
//
// This middleware intercepts tools/call responses and:
// 1. Checks if the tool is a query tool that should have DataHub context first
// 2. Adds warnings for rule violations to the response
// 3. Does NOT block requests - it only adds informational guidance
func MCPRuleEnforcementMiddleware(engine *tuning.RuleEngine, hints *tuning.HintManager) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// Only intercept tools/call requests
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			pc := GetPlatformContext(ctx)
			if pc == nil {
				return next(ctx, method, req)
			}

			// Collect hints/warnings to prepend
			var hints []string

			// Check if DataHub should be checked first for query tools
			if engine.ShouldRequireDataHubCheck() && isQueryTool(pc.ToolName) {
				hints = append(hints,
					"💡 Tip: Consider using datahub_search or datahub_get_entity first "+
						"to understand the data context before querying.")
			}

			// Execute the actual tool
			result, err := next(ctx, method, req)

			// If there are hints and the result is successful, prepend them
			if len(hints) > 0 && err == nil {
				result = prependHintsToResult(result, hints)
			}

			return result, err
		}
	}
}

// isQueryTool returns true if the tool name indicates a query/write operation.
func isQueryTool(toolName string) bool {
	queryTools := []string{
		"trino_query",
		"trino_execute",
	}

	for _, qt := range queryTools {
		if toolName == qt {
			return true
		}
	}
	return false
}

// prependHintsToResult adds hints as text content at the beginning of the result.
func prependHintsToResult(result mcp.Result, hints []string) mcp.Result {
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil {
		return result
	}

	// Don't add hints to error results
	if callResult.IsError {
		return result
	}

	// Build hints text
	hintsText := strings.Join(hints, "\n") + "\n\n---\n\n"

	// Prepend hints as text content
	hintContent := &mcp.TextContent{Text: hintsText}
	callResult.Content = append([]mcp.Content{hintContent}, callResult.Content...)

	return callResult
}
