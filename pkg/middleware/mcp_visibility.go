package middleware

import (
	"context"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP method names used across middleware.
const (
	methodToolsList              = "tools/list"
	methodResourcesTemplatesList = "resources/templates/list"
	methodPromptsList            = "prompts/list"
)

// MCPToolVisibilityMiddleware creates MCP protocol-level middleware that filters
// tools/list responses based on allow/deny glob patterns. This reduces token
// usage in LLM clients by hiding tools that are not needed for a deployment.
//
// This is a visibility filter, not a security boundary — persona auth continues
// to gate tools/call independently.
func MCPToolVisibilityMiddleware(allow, deny []string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			return filterToolVisibility(allow, deny, method, result)
		}
	}
}

// filterToolVisibility filters tools from a tools/list response based on
// allow/deny patterns. Non-tools/list methods pass through unchanged.
func filterToolVisibility(allow, deny []string, method string, result mcp.Result) (mcp.Result, error) {
	if method != methodToolsList {
		return result, nil
	}

	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil {
		return result, nil
	}

	filtered := make([]*mcp.Tool, 0, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		if IsToolVisible(tool.Name, allow, deny) {
			filtered = append(filtered, tool)
		}
	}
	listResult.Tools = filtered

	return listResult, nil
}

// IsToolVisible determines whether a tool should appear in tools/list based on
// allow/deny glob patterns. Semantics:
//   - No patterns configured: all tools visible
//   - Allow only: only matching tools pass
//   - Deny only: all pass except denied
//   - Both: allow first, then deny removes from that set
//   - Invalid glob patterns are treated as non-matching (silent skip)
func IsToolVisible(name string, allow, deny []string) bool {
	if len(allow) == 0 && len(deny) == 0 {
		return true
	}

	visible := len(allow) == 0 // If no allow rules, default to visible

	for _, pattern := range allow {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			visible = true
			break
		}
	}

	if !visible {
		return false
	}

	for _, pattern := range deny {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return false
		}
	}

	return true
}
