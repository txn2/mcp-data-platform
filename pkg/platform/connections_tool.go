package platform

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/connview"
)

// connectionEntry and listConnectionsOutput are the list_connections view types,
// owned by pkg/connview (kept out of pkg/platform for the size budget). The aliases
// preserve the existing platform-internal names and JSON shape.
type connectionEntry = connview.Entry

type listConnectionsOutput = connview.Output

// listConnectionsInput is empty since this tool has no parameters.
type listConnectionsInput struct{}

// registerConnectionsTool registers the list_connections tool with the MCP server.
func (p *Platform) registerConnectionsTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:  toolListConns,
		Title: "List Connections",
		Description: "List all configured data connections across toolkits (Trino, DataHub, S3, etc.). " +
			"Each connection includes a count and a bounded sample of the canonical knowledge pages that document it.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ listConnectionsInput) (*mcp.CallToolResult, any, error) {
		return p.handleListConnections(ctx, req)
	})
}

// handleListConnections handles the list_connections tool call, delegating the view
// build (and the knowledge-page reverse-lookup enrichment) to pkg/connview.
func (p *Platform) handleListConnections(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, any, error) {
	var src connview.SourceResolver
	if p.connectionSources != nil {
		src = p.connectionSources
	}
	var pages connview.PageLookup
	if p.portalKnowledgePageStore != nil {
		pages = p.portalKnowledgePageStore
	}

	out := connview.Build(ctx, p.toolkitRegistry.All(), src, pages)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return &mcp.CallToolResult{ //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError, not as Go errors
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Error: " + err.Error()},
			},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}
