// Package main provides the entry point for the {{project-name}} server.
package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"

	mcpserver "github.com/{{github-org}}/{{project-name}}/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Create the server with defaults from environment
	mcpServer, toolkit, err := mcpserver.NewWithDefaults()
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	defer func() { _ = toolkit.Close() }()

	// Start the server using stdio transport
	if err := server.ServeStdio(mcpServer); err != nil {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}
