// Package tools provides MCP tool definitions for mcp-data-platform.
package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Toolkit provides MCP tools.
type Toolkit struct {
	// Add client or other dependencies here
}

// NewToolkit creates a new Toolkit.
func NewToolkit() *Toolkit {
	return &Toolkit{}
}

// RegisterTools registers all tools with the MCP server.
func (t *Toolkit) RegisterTools(s *server.MCPServer) {
	// Register example tool
	s.AddTool(
		mcp.NewTool("example_tool",
			mcp.WithDescription("An example tool that echoes a message"),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("The message to echo"),
			),
		),
		t.handleExampleTool,
	)
}

// handleExampleTool handles the example_tool MCP call.
func (t *Toolkit) handleExampleTool(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}

	message, ok := args["message"].(string)
	if !ok {
		return mcp.NewToolResultError("message must be a string"), nil
	}

	return mcp.NewToolResultText("Echo: " + message), nil
}

// Close cleans up any resources used by the toolkit.
func (t *Toolkit) Close() error {
	return nil
}
