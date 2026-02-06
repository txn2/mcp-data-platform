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
	flag.StringVar(&opts.transport, "transport", "stdio", "Transport type: stdio, http")
	flag.StringVar(&opts.address, "address", ":8080", "Server address for HTTP transports")
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

	ctx := setupSignalHandler()

	result, err := createServer(opts)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	defer closeServer(result)

	applyConfigOverrides(result.platform, &opts)

	return startServer(ctx, result.mcpServer, result.platform, opts)
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

func startServer(ctx context.Context, mcpServer *mcp.Server, p *platform.Platform, opts serverOptions) error {
	switch opts.transport {
	case "stdio":
		return mcpServer.Run(ctx, &mcp.StdioTransport{})
	case "http", "sse":
		// HTTP serves both SSE (/sse, /message) and Streamable HTTP (/mcp).
		// "sse" is accepted for backward compatibility.
		return startHTTPServer(ctx, mcpServer, p, opts)
	default:
		return fmt.Errorf("unknown transport: %s", opts.transport)
	}
}

// corsMiddleware adds CORS headers for browser-based MCP clients.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers",
			"Content-Type, Authorization, Accept, X-API-Key, "+
				"Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// httpConfig holds configuration extracted from the platform for HTTP servers.
type httpConfig struct {
	requireAuth   bool
	tlsEnabled    bool
	tlsCertFile   string
	tlsKeyFile    string
	streamableCfg platform.StreamableConfig
}

func extractHTTPConfig(p *platform.Platform) httpConfig {
	var cfg httpConfig
	if p != nil && p.Config() != nil {
		c := p.Config()
		cfg.requireAuth = !c.Auth.AllowAnonymous
		cfg.tlsEnabled = c.Server.TLS.Enabled
		cfg.tlsCertFile = c.Server.TLS.CertFile
		cfg.tlsKeyFile = c.Server.TLS.KeyFile
		cfg.streamableCfg = c.Server.Streamable
	}
	return cfg
}

// newSSEHandler creates an SSE handler with auth middleware.
func newSSEHandler(mcpServer *mcp.Server, requireAuth bool) http.Handler {
	sseHandler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	if requireAuth {
		return httpauth.RequireAuth()(sseHandler)
	}
	return httpauth.OptionalAuth()(sseHandler)
}

func startHTTPServer(ctx context.Context, mcpServer *mcp.Server, p *platform.Platform, opts serverOptions) error {
	mux := http.NewServeMux()
	hcfg := extractHTTPConfig(p)

	if !hcfg.tlsEnabled {
		log.Println("WARNING: HTTP transport without TLS - credentials may be transmitted in plaintext")
	}

	// Mount OAuth server if enabled
	if p != nil && p.OAuthServer() != nil {
		registerOAuthRoutes(mux, p.OAuthServer())
		log.Println("OAuth server enabled")
	}

	// Mount SSE handler (legacy clients)
	wrappedSSE := newSSEHandler(mcpServer, hcfg.requireAuth)
	mux.Handle("/sse", wrappedSSE)
	mux.Handle("/message", wrappedSSE)
	log.Println("SSE transport enabled on /sse, /message")

	// Mount Streamable HTTP handler at root (modern clients)
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: hcfg.streamableCfg.SessionTimeout,
		Stateless:      hcfg.streamableCfg.Stateless,
	})
	// Streamable HTTP handles auth via RequestExtra headers (bridged in
	// MCPToolCallMiddleware), so no HTTP-level auth middleware is needed.
	mux.Handle("/", streamableHandler)
	log.Println("Streamable HTTP transport enabled on /")

	return listenAndServe(ctx, opts.address, corsMiddleware(mux), hcfg)
}

func listenAndServe(ctx context.Context, addr string, handler http.Handler, hcfg httpConfig) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if hcfg.tlsEnabled {
		log.Printf("Starting HTTP server with TLS on %s\n", addr)
		if err := server.ListenAndServeTLS(hcfg.tlsCertFile, hcfg.tlsKeyFile); err != http.ErrServerClosed {
			return err
		}
		return nil
	}

	log.Printf("Starting HTTP server on %s\n", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// registerOAuthRoutes registers OAuth endpoints on the given mux.
// Supports both standard paths (with /oauth prefix) and Claude Desktop
// compatible paths (without /oauth prefix).
func registerOAuthRoutes(mux *http.ServeMux, handler http.Handler) {
	// Standard paths (with /oauth prefix)
	mux.Handle("/.well-known/oauth-authorization-server", handler)
	mux.Handle("/oauth/authorize", handler)
	mux.Handle("/oauth/callback", handler)
	mux.Handle("/oauth/token", handler)
	mux.Handle("/oauth/register", handler)
	// Claude Desktop compatibility paths (without /oauth prefix)
	mux.Handle("/authorize", handler)
	mux.Handle("/callback", handler)
	mux.Handle("/token", handler)
	mux.Handle("/register", handler)
}
