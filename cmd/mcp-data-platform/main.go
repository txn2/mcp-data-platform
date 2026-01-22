// Package main provides the entry point for the mcp-data-platform server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	httpauth "github.com/txn2/mcp-data-platform/pkg/http"
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

	return startServer(result.mcpServer, result.platform, opts)
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

func startServer(mcpServer *mcp.Server, p *platform.Platform, opts serverOptions) error {
	switch opts.transport {
	case "stdio":
		return mcpServer.Run(context.Background(), &mcp.StdioTransport{})
	case "sse":
		return startSSEServer(mcpServer, p, opts)
	default:
		return fmt.Errorf("unknown transport: %s", opts.transport)
	}
}

func startSSEServer(mcpServer *mcp.Server, p *platform.Platform, opts serverOptions) error {
	mux := http.NewServeMux()

	// SSE handler for MCP protocol
	sseHandler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	// Get config if available
	var requireAuth bool
	var tlsEnabled bool
	var tlsCertFile, tlsKeyFile string

	if p != nil && p.Config() != nil {
		cfg := p.Config()
		// Require auth unless explicitly allowing anonymous
		requireAuth = !cfg.Auth.AllowAnonymous
		tlsEnabled = cfg.Server.TLS.Enabled
		tlsCertFile = cfg.Server.TLS.CertFile
		tlsKeyFile = cfg.Server.TLS.KeyFile
	}

	// Warn if SSE transport is used without TLS
	if !tlsEnabled {
		log.Println("WARNING: SSE transport without TLS - credentials may be transmitted in plaintext")
	}

	// Apply HTTP auth middleware to SSE handler
	var wrappedSSE http.Handler
	if requireAuth {
		wrappedSSE = httpauth.RequireAuth()(sseHandler)
	} else {
		wrappedSSE = httpauth.OptionalAuth()(sseHandler)
	}

	// Mount OAuth server if enabled
	if p != nil && p.OAuthServer() != nil {
		oauthServer := p.OAuthServer()
		// Mount OAuth endpoints (no auth middleware - OAuth handles its own auth)
		mux.Handle("/.well-known/oauth-authorization-server", oauthServer)
		mux.Handle("/oauth/authorize", oauthServer)
		mux.Handle("/oauth/callback", oauthServer)
		mux.Handle("/oauth/token", oauthServer)
		mux.Handle("/oauth/register", oauthServer)
		log.Println("OAuth server enabled")
	}

	// Mount SSE handler for MCP protocol
	mux.Handle("/sse", wrappedSSE)
	mux.Handle("/message", wrappedSSE)
	mux.Handle("/", wrappedSSE)

	server := &http.Server{
		Addr:              opts.address,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Use TLS if configured
	if tlsEnabled {
		log.Printf("Starting SSE server with TLS on %s\n", opts.address)
		return server.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
	}

	log.Printf("Starting SSE server on %s\n", opts.address)
	return server.ListenAndServe()
}
