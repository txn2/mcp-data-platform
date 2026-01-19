// Package server provides a factory for creating the MCP server.
package server

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/tools"
)

// Version is set at build time.
var Version = "dev"

// New creates a new MCP server with the given configuration.
func New(cfg *platform.Config) (*server.MCPServer, *platform.Platform, error) {
	// Create platform
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("creating platform: %w", err)
	}

	// Start platform
	if err := p.Start(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("starting platform: %w", err)
	}

	// Create default toolkit and register
	toolkit := tools.NewToolkit()
	toolkit.RegisterTools(p.MCPServer())

	return p.MCPServer(), p, nil
}

// NewWithDefaults creates a new MCP server with default configuration.
func NewWithDefaults() (*server.MCPServer, *tools.Toolkit, error) {
	mcpServer := server.NewMCPServer("mcp-data-platform", Version, server.WithLogging())

	toolkit := tools.NewToolkit()
	toolkit.RegisterTools(mcpServer)

	return mcpServer, toolkit, nil
}

// NewWithConfig creates a new MCP server with the specified config file.
func NewWithConfig(configPath string) (*server.MCPServer, *platform.Platform, error) {
	cfg, err := platform.LoadConfig(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	return New(cfg)
}
