package platform

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectionEntry describes a single toolkit connection.
type connectionEntry struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Connection string `json:"connection"`
}

// listConnectionsOutput is the JSON response for the list_connections tool.
type listConnectionsOutput struct {
	Connections []connectionEntry `json:"connections"`
	Count       int               `json:"count"`
}

// listConnectionsInput is empty since this tool has no parameters.
type listConnectionsInput struct{}

// registerConnectionsTool registers the list_connections tool with the MCP server.
func (p *Platform) registerConnectionsTool() {
	mcp.AddTool(p.mcpServer, &mcp.Tool{
		Name:        "list_connections",
		Description: "List all configured data connections across toolkits (Trino, DataHub, S3, etc.).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ listConnectionsInput) (*mcp.CallToolResult, any, error) {
		return p.handleListConnections(ctx, req)
	})
}

// handleListConnections handles the list_connections tool call.
func (p *Platform) handleListConnections(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, any, error) {
	toolkits := p.toolkitRegistry.All()

	entries := make([]connectionEntry, 0, len(toolkits))
	for _, tk := range toolkits {
		entries = append(entries, connectionEntry{
			Kind:       tk.Kind(),
			Name:       tk.Name(),
			Connection: tk.Connection(),
		})
	}

	out := listConnectionsOutput{
		Connections: entries,
		Count:       len(entries),
	}

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
