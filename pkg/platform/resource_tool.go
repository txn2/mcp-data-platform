package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type readResourceInput struct {
	URI string `json:"uri"`
}

// registerResourceTool registers the read_resource tool when there are
// resources in the registry. Skipped if no resources are configured.
func (p *Platform) registerResourceTool() {
	if len(p.resourceRegistry) == 0 {
		return
	}

	uris := make([]string, 0, len(p.resourceRegistry))
	for uri := range p.resourceRegistry {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	desc := "Read an MCP resource by URI and return its content. " +
		"Use this to fetch brand assets, operational hints, and other registered resources. " +
		"Available URIs: " + strings.Join(uris, ", ")

	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:        "read_resource",
		Title:       "Read Resource",
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input readResourceInput) (*mcp.CallToolResult, any, error) {
		return p.handleReadResource(ctx, req, input.URI)
	})
}

// handleReadResource executes a resource read from the platform registry.
func (p *Platform) handleReadResource(_ context.Context, _ *mcp.CallToolRequest, uri string) (*mcp.CallToolResult, any, error) {
	handler, ok := p.resourceRegistry[uri]
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("resource not found: %s", uri)},
			},
			IsError: true,
		}, nil, nil
	}

	result, err := handler(context.Background(), &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{URI: uri},
	})
	if err != nil {
		return &mcp.CallToolResult{ //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError, not as Go errors
			Content: []mcp.Content{
				&mcp.TextContent{Text: "error reading resource: " + err.Error()},
			},
			IsError: true,
		}, nil, nil
	}

	if len(result.Contents) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "resource has no content"},
			},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: result.Contents[0].Text},
		},
	}, nil, nil
}
