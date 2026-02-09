// Package main provides the entry point for the mcp-data-platform server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/pkg/health"
	httpauth "github.com/txn2/mcp-data-platform/pkg/http"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

const (
	defaultReadHeaderTimeout = 10 * time.Second
	fallbackGracePeriod      = 25 * time.Second
	fallbackPreShutdownDelay = 2 * time.Second
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
	flag.Parse() //nolint:revive // flag.Parse in main-called function is intentional
	return opts
}

func setupSignalHandler() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received shutdown signal", "signal", sig)
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
		if err != nil {
			return nil, fmt.Errorf("creating server with config: %w", err)
		}
		return result, nil
	}

	result.mcpServer, result.toolkit, err = mcpserver.NewWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("creating server with defaults: %w", err)
	}
	return result, nil
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
		if err := result.platform.Close(); err != nil {
			slog.Error("shutdown: platform close error", "error", err)
		}
	}
	if result.toolkit != nil {
		if err := result.toolkit.Close(); err != nil {
			slog.Error("shutdown: toolkit close error", "error", err)
		}
	}
	slog.Info("shutdown: complete")
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
		if err := mcpServer.Run(ctx, &mcp.StdioTransport{}); err != nil {
			return fmt.Errorf("running stdio server: %w", err)
		}
		return nil
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
	shutdownCfg   platform.ShutdownConfig
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
		cfg.shutdownCfg = c.Server.Shutdown
	}
	return cfg
}

// newSSEHandler creates an SSE handler with auth middleware.
func newSSEHandler(mcpServer *mcp.Server, requireAuth bool, resourceMetadataURL string) http.Handler {
	sseHandler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	if requireAuth && resourceMetadataURL != "" {
		return httpauth.RequireAuthWithOAuth(resourceMetadataURL)(sseHandler)
	}
	if requireAuth {
		return httpauth.RequireAuth()(sseHandler)
	}
	return httpauth.OptionalAuth()(sseHandler)
}

// resourceMetadataURL returns the protected resource metadata URL if OAuth is
// enabled, or empty string otherwise.
func resourceMetadataURL(p *platform.Platform) string {
	if p == nil || p.OAuthServer() == nil {
		return ""
	}
	return p.Config().OAuth.Issuer + "/.well-known/oauth-protected-resource"
}

func startHTTPServer(ctx context.Context, mcpServer *mcp.Server, p *platform.Platform, opts serverOptions) error {
	mux := http.NewServeMux()
	hcfg := extractHTTPConfig(p)
	hc := health.NewChecker()

	if !hcfg.tlsEnabled {
		log.Println("WARNING: HTTP transport without TLS - credentials may be transmitted in plaintext")
	}

	// Health endpoints (registered before catch-all /)
	mux.Handle("/healthz", hc.LivenessHandler())
	mux.Handle("/readyz", hc.ReadinessHandler())

	// Mount OAuth server if enabled
	if p != nil && p.OAuthServer() != nil {
		registerOAuthRoutes(mux, p.OAuthServer())
		log.Println("OAuth server enabled")
	}

	// Mount OAuth protected resource metadata (RFC 9728) when OAuth is
	// enabled. MCP clients discover the authorization server from this
	// endpoint after receiving an HTTP 401 with WWW-Authenticate header.
	rmURL := resourceMetadataURL(p)
	if rmURL != "" {
		issuer := p.Config().OAuth.Issuer
		mux.Handle("/.well-known/oauth-protected-resource",
			sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
				Resource:               issuer,
				AuthorizationServers:   []string{issuer},
				BearerMethodsSupported: []string{"header"},
				ResourceName:           p.Config().Server.Name,
			}))
		log.Println("OAuth protected resource metadata enabled on /.well-known/oauth-protected-resource")
	}

	// Mount SSE handler (legacy clients)
	wrappedSSE := newSSEHandler(mcpServer, hcfg.requireAuth, rmURL)
	mux.Handle("/sse", wrappedSSE)
	mux.Handle("/message", wrappedSSE)
	log.Println("SSE transport enabled on /sse, /message")

	// Mount Streamable HTTP handler at root (modern clients).
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: hcfg.streamableCfg.SessionTimeout,
		Stateless:      hcfg.streamableCfg.Stateless,
	})

	// Wrap with AwareHandler when using external session store
	// (database mode forces Stateless: true on the SDK, and sessions
	// are managed by our handler against the external store).
	var rootHandler http.Handler = streamableHandler
	if p != nil && p.SessionStore() != nil && hcfg.streamableCfg.Stateless {
		rootHandler = session.NewAwareHandler(streamableHandler, session.HandlerConfig{
			Store: p.SessionStore(),
			TTL:   p.Config().Sessions.TTL,
		})
		log.Println("Session-aware handler enabled (external session store)")
	}

	if hcfg.requireAuth {
		mux.Handle("/", httpauth.MCPAuthGateway(rmURL)(rootHandler))
		log.Println("Streamable HTTP transport enabled on / (auth required)")
	} else {
		mux.Handle("/", rootHandler)
		log.Println("Streamable HTTP transport enabled on / (anonymous)")
	}

	return listenAndServe(ctx, opts.address, corsMiddleware(mux), hcfg, hc)
}

func listenAndServe(ctx context.Context, addr string, handler http.Handler, hcfg httpConfig, hc *health.Checker) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
	}

	gracePeriod := hcfg.shutdownCfg.GracePeriod
	if gracePeriod == 0 {
		gracePeriod = fallbackGracePeriod
	}
	preDelay := hcfg.shutdownCfg.PreShutdownDelay
	if preDelay == 0 {
		preDelay = fallbackPreShutdownDelay
	}

	go func() {
		<-ctx.Done()

		// Mark not-ready so K8s load balancer stops sending traffic.
		if hc != nil {
			hc.SetDraining()
			slog.Info("shutdown: readiness set to draining, waiting for LB deregistration",
				"pre_shutdown_delay", preDelay)
			time.Sleep(preDelay)
		}

		// Drain in-flight requests.
		slog.Info("shutdown: draining HTTP connections", "grace_period", gracePeriod)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), gracePeriod)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown: HTTP drain error", "error", err)
		} else {
			slog.Info("shutdown: HTTP server stopped")
		}
	}()

	// Mark ready just before we start accepting connections.
	if hc != nil {
		hc.SetReady()
	}

	if hcfg.tlsEnabled {
		log.Printf("Starting HTTP server with TLS on %s\n", addr)
		if err := server.ListenAndServeTLS(hcfg.tlsCertFile, hcfg.tlsKeyFile); err != http.ErrServerClosed {
			return fmt.Errorf("listening with TLS on %s: %w", addr, err)
		}
		return nil
	}

	log.Printf("Starting HTTP server on %s\n", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("listening on %s: %w", addr, err)
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
