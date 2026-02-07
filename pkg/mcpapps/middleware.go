package mcpapps

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolMetadataMiddleware creates MCP protocol-level middleware that injects
// _meta.ui metadata into tools/list responses for tools that have registered apps.
//
// When a host requests tools/list, this middleware intercepts the response and
// adds UI metadata to tools that have associated MCP Apps. This allows
// MCP Apps-compatible hosts to render interactive UIs for tool results.
func ToolMetadataMiddleware(reg *Registry) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			return injectToolMetadata(reg, method, result)
		}
	}
}

// injectToolMetadata adds UI metadata to tools/list responses for tools that
// have registered MCP Apps.
func injectToolMetadata(reg *Registry, method string, result mcp.Result) (mcp.Result, error) {
	// Only process tools/list responses.
	if method != "tools/list" {
		return result, nil
	}

	// Type assert to ListToolsResult.
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil {
		return result, nil
	}

	// Inject UI metadata into tools that have registered apps.
	for _, tool := range listResult.Tools {
		app := reg.GetForTool(tool.Name)
		if app == nil {
			continue
		}

		// Initialize Meta if nil.
		if tool.Meta == nil {
			tool.Meta = make(mcp.Meta)
		}

		// Add UI metadata.
		tool.Meta["ui"] = map[string]string{
			"resourceUri": app.ResourceURI,
		}
	}

	return listResult, nil
}
