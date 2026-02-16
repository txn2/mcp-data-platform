package middleware

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// IconConfig defines an icon for middleware injection.
type IconConfig struct {
	Source   string
	MIMEType string
}

// IconsMiddlewareConfig holds icon definitions keyed by name/URI.
type IconsMiddlewareConfig struct {
	Tools     map[string]IconConfig // keyed by tool name
	Resources map[string]IconConfig // keyed by URI template
	Prompts   map[string]IconConfig // keyed by prompt name
}

// MCPIconMiddleware creates MCP protocol-level middleware that injects icons
// into tools/list, resources/templates/list, and prompts/list responses.
// Icons are matched by name (tools, prompts) or URI template (resources).
func MCPIconMiddleware(cfg IconsMiddlewareConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}

			switch method {
			case methodToolsList:
				return injectToolIcons(cfg.Tools, result), nil
			case methodResourcesTemplatesList:
				return injectResourceTemplateIcons(cfg.Resources, result), nil
			case methodPromptsList:
				return injectPromptIcons(cfg.Prompts, result), nil
			default:
				return result, nil
			}
		}
	}
}

// injectToolIcons adds icons to tools in a tools/list response.
func injectToolIcons(icons map[string]IconConfig, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil || len(icons) == 0 {
		return result
	}

	for _, tool := range listResult.Tools {
		if ic, found := icons[tool.Name]; found {
			tool.Icons = appendIcon(tool.Icons, ic)
		}
	}

	return listResult
}

// injectResourceTemplateIcons adds icons to resource templates in a
// resources/templates/list response.
func injectResourceTemplateIcons(icons map[string]IconConfig, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListResourceTemplatesResult)
	if !ok || listResult == nil || len(icons) == 0 {
		return result
	}

	for _, rt := range listResult.ResourceTemplates {
		if ic, found := icons[rt.URITemplate]; found {
			rt.Icons = appendIcon(rt.Icons, ic)
		}
	}

	return listResult
}

// injectPromptIcons adds icons to prompts in a prompts/list response.
func injectPromptIcons(icons map[string]IconConfig, result mcp.Result) mcp.Result {
	listResult, ok := result.(*mcp.ListPromptsResult)
	if !ok || listResult == nil || len(icons) == 0 {
		return result
	}

	for _, prompt := range listResult.Prompts {
		if ic, found := icons[prompt.Name]; found {
			prompt.Icons = appendIcon(prompt.Icons, ic)
		}
	}

	return listResult
}

// appendIcon creates an mcp.Icon and appends it to the existing slice.
func appendIcon(existing []mcp.Icon, ic IconConfig) []mcp.Icon {
	return append(existing, mcp.Icon{
		Source:   ic.Source,
		MIMEType: ic.MIMEType,
	})
}
