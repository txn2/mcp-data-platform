// Package main provides the entry point for the mcp-data-platform server.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	mcpServer *mcp.Server
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

func startServer(mcpServer *mcp.Server, opts serverOptions) error {
	switch opts.transport {
	case "stdio":
		return mcpServer.Run(context.Background(), &mcp.StdioTransport{})
	case "sse":
		handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
			return mcpServer
		}, nil)
		server := &http.Server{
			Addr:              opts.address,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		return server.ListenAndServe()
	default:
		return fmt.Errorf("unknown transport: %s", opts.transport)
	}
}
