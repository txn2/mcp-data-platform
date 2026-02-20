// Package server provides a factory for creating the MCP server.
package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Commit is the git short commit hash, set at build time via ldflags.
var Commit = "none"

// Date is the build timestamp, set at build time via ldflags.
var Date = "unknown"

// New creates a new MCP server with the given configuration.
func New(cfg *platform.Config) (*mcp.Server, *platform.Platform, error) {
	// Use build-time version when config doesn't specify one
	if cfg.Server.Version == "" {
		cfg.Server.Version = Version
	}

	// Create platform
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("creating platform: %w", err)
	}

	// Start platform
	if err := p.Start(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("starting platform: %w", err)
	}

	mcpSrv := p.MCPServer()
	return mcpSrv, p, nil
}

// NewWithDefaults creates a new MCP server with default configuration.
func NewWithDefaults() (*mcp.Server, error) {
	impl := &mcp.Implementation{
		Name:    "mcp-data-platform",
		Version: Version,
	}
	mcpServer := mcp.NewServer(impl, nil)

	return mcpServer, nil
}

// NewWithConfig creates a new MCP server with the specified config file.
func NewWithConfig(configPath string) (*mcp.Server, *platform.Platform, error) {
	cfg, err := platform.LoadConfig(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	return New(cfg)
}
