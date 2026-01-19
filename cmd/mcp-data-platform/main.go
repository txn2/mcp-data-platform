// Package main provides the entry point for the mcp-data-platform server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"

	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type serverOptions struct {
	configPath  string
	transport   string
	address     string
	showVersion bool
}

func parseFlags() serverOptions {
	opts := serverOptions{}
	flag.StringVar(&opts.configPath, "config", "", "Path to configuration file")
	flag.StringVar(&opts.transport, "transport", "stdio", "Transport type: stdio, sse")
	flag.StringVar(&opts.address, "address", ":8080", "Server address for SSE transport")
	flag.BoolVar(&opts.showVersion, "version", false, "Show version and exit")
	flag.Parse()
	return opts
}

func setupSignalHandler() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx
}

type serverResult struct {
	mcpServer *server.MCPServer
	platform  *platform.Platform
	toolkit   interface{ Close() error }
}

func createServer(opts serverOptions) (*serverResult, error) {
	result := &serverResult{}
	var err error

	if opts.configPath != "" {
		result.mcpServer, result.platform, err = mcpserver.NewWithConfig(opts.configPath)
		return result, err
	}

	result.mcpServer, result.toolkit, err = mcpserver.NewWithDefaults()
	return result, err
}

func run() error {
	opts := parseFlags()

	if opts.showVersion {
		fmt.Printf("mcp-data-platform version %s\n", mcpserver.Version)
		return nil
	}

	_ = setupSignalHandler()

	result, err := createServer(opts)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	defer closeServer(result)

	applyConfigOverrides(result.platform, &opts)

	return startServer(result.mcpServer, opts)
}

func closeServer(result *serverResult) {
	if result.platform != nil {
		_ = result.platform.Close()
	}
	if result.toolkit != nil {
		_ = result.toolkit.Close()
	}
}

func applyConfigOverrides(p *platform.Platform, opts *serverOptions) {
	if p == nil {
		return
	}
	if p.Config().Server.Transport != "" {
		opts.transport = p.Config().Server.Transport
	}
	if p.Config().Server.Address != "" {
		opts.address = p.Config().Server.Address
	}
}

func startServer(mcpServer *server.MCPServer, opts serverOptions) error {
	switch opts.transport {
	case "stdio":
		return server.ServeStdio(mcpServer)
	case "sse":
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL(opts.address))
		return sseServer.Start(opts.address)
	default:
		return fmt.Errorf("unknown transport: %s", opts.transport)
	}
}
