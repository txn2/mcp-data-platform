// Package server provides a factory for creating the MCP server.
package server

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/{{github-org}}/{{project-name}}/pkg/tools"
)

// Version is set at build time.
var Version = "dev"

// New creates a new MCP server with the given configuration.
func New() (*server.MCPServer, *tools.Toolkit, error) {
	mcpServer := server.NewMCPServer("{{project-name}}", Version, server.WithLogging())

	toolkit := tools.NewToolkit()
	toolkit.RegisterTools(mcpServer)

	return mcpServer, toolkit, nil
}

// NewWithDefaults creates a new MCP server with default configuration from environment.
func NewWithDefaults() (*server.MCPServer, *tools.Toolkit, error) {
	return New()
}
