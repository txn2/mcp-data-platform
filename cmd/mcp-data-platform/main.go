// Package main provides the entry point for the mcp-data-platform server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // Swagger API docs
	mcpserver "github.com/txn2/mcp-data-platform/internal/server"
	"github.com/txn2/mcp-data-platform/internal/ui"
	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/gatewayhttp"
	"github.com/txn2/mcp-data-platform/pkg/health"
	httpauth "github.com/txn2/mcp-data-platform/pkg/http"
	"github.com/txn2/mcp-data-platform/pkg/observability/proxy"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/session"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	datahubkit "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/sources"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

const (
	defaultReadHeaderTimeout = 10 * time.Second
	fallbackGracePeriod      = 25 * time.Second
	fallbackPreShutdownDelay = 2 * time.Second
	// lifecycleStopTimeout bounds how long Platform.Stop is allowed to
	// run before Close proceeds anyway. Sized so the full shutdown
	// budget (preDelay + httpGrace + lifecycleStop + close overhead)
	// fits inside a 60s terminationGracePeriodSeconds with headroom.
	lifecycleStopTimeout = 10 * time.Second
	transportHTTP        = "http"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate-config" {
		if err := runMigrateConfig(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
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
	ctx, cancel := context.WithCancel(context.Background()) // #nosec G118 -- root process context; cancel is called in the goroutine below
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

	result.mcpServer, err = mcpserver.NewWithDefaults()
	if err != nil {
		return nil, fmt.Errorf("creating server with defaults: %w", err)
	}
	return result, nil
}

// initLogging configures slog from the LOG_LEVEL environment variable.
// Supported values: debug, info, warn, error. Defaults to info.
func initLogging() {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug", "DEBUG":
		level = slog.LevelDebug
	case "warn", "WARN":
		level = slog.LevelWarn
	case "error", "ERROR":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))
}

func run() error {
	initLogging()
	opts := parseFlags()

	if opts.showVersion {
		fmt.Printf("mcp-data-platform version %s (commit: %s, built: %s)\n",
			mcpserver.Version, mcpserver.Commit, mcpserver.Date)
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
		// Stop runs every Lifecycle OnStop callback (background workers,
		// reapers, listeners). Bounded by lifecycleStopTimeout so a
		// hung worker cannot exceed the K8s termination grace period;
		// abandoned work is safe because PostgreSQL leases expire and
		// another replica reclaims it.
		stopCtx, cancel := context.WithTimeout(context.Background(), lifecycleStopTimeout)
		if err := result.platform.Stop(stopCtx); err != nil {
			slog.Error("shutdown: platform stop error", "error", err)
		}
		cancel()

		if err := result.platform.Close(); err != nil {
			slog.Error("shutdown: platform close error", "error", err)
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
	// Start the /metrics listener for BOTH transports so operators get
	// the same observability surface whether the platform is running
	// in stdio (one-off CLI / Claude Desktop) or HTTP mode. The wire
	// step also instruments any apigateway toolkit registered before
	// the listener came up so existing connections start recording on
	// the same call boundary the new ones do. Both calls are nil-safe
	// and no-op when metrics are disabled.
	if p != nil {
		if err := p.StartMetricsListener(ctx); err != nil {
			return fmt.Errorf("starting metrics listener: %w", err)
		}
		p.WireAPIGatewayMetrics()
		// Wire the process-wide in-flight memory budget for BOTH
		// transports so the OOM guard (issue #535) applies whether the
		// platform runs in stdio or HTTP mode.
		p.WireAPIGatewayMemBudget()
	}

	switch opts.transport {
	case "stdio":
		if err := mcpServer.Run(ctx, &mcp.StdioTransport{}); err != nil {
			return fmt.Errorf("running stdio server: %w", err)
		}
		return nil
	case transportHTTP, "sse":
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
	portalUI      bool
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
		cfg.portalUI = (c.Portal.Enabled == nil || *c.Portal.Enabled) && ui.Available()
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
	// Wire platform-wide integrations BEFORE the root handler is built
	// and before any feature-conditional setup runs. Both calls are
	// idempotent and no-op when there are no gateway toolkits, so it
	// is safe to do this unconditionally — and required for correctness
	// because the SSE long-poll path needs the broadcaster wired into
	// the gateway toolkit before AwareHandler accepts subscribers, and
	// because admin.enabled may be false on locked-down replicas yet
	// the gateway's persisted refresh tokens and tools/list_changed
	// fan-out are still needed.
	if p != nil {
		p.WireGatewayTokenStore()
		p.WireGatewayBroadcaster()
		p.WireAPIGatewayRoutePolicy()
		p.WireAPIGatewayTokenStore()
		p.WireAPIGatewayEmbeddingProvider()
		p.WireAPIGatewayCatalogStoreFromDB()
		// Embed-job queue depends on the catalog store + embedding
		// provider being wired first; call last.
		p.WireAPIGatewayEmbedJobsFromDB()
		// Start the background OAuth refresher once toolkits and
		// connection store are wired. Single-call here (not in the
		// platform constructor) so the resolver can read the live
		// ConnectionStore + OAuthKindHandlers from the fully-set-up
		// platform — these are not available at platform.New time.
		startConnOAuthRefresher(p)
	}

	mux := http.NewServeMux()
	hcfg := extractHTTPConfig(p)
	hc := health.NewChecker()

	if !hcfg.tlsEnabled {
		log.Println("WARNING: HTTP transport without TLS - credentials may be transmitted in plaintext")
	}

	// Health endpoints (registered before catch-all /)
	mux.Handle("/healthz", hc.LivenessHandler())
	mux.Handle("/readyz", hc.ReadinessHandler())

	// Robots.txt — prevent search engines from indexing the portal.
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
	})

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

	// Mount browser auth routes (OIDC login/callback/logout)
	mountBrowserAuth(mux, p)

	// Mount admin API if enabled
	mountAdminAPI(mux, p)

	// Mount portal API if enabled
	mountPortalAPI(mux, p)

	// Mount managed resources API if enabled
	mountResourcesAPI(mux, p)

	// Mount the REST gateway shim if an apigateway toolkit is loaded.
	// Exposes api_invoke_endpoint over plain HTTP for non-MCP clients
	// (e.g. Apache NiFi). Auth + persona + audit all flow through the
	// MCP middleware chain via an in-memory session.
	mountGatewayAPI(mux, mcpServer, p, hcfg.requireAuth)

	// Mount the authenticated PromQL query proxy the portal's
	// observability views read from (#462). Always mounted; returns 503
	// when Prometheus is not configured.
	mountObservabilityProxy(mux, p, hcfg.requireAuth)

	// Mount unified portal UI (includes both portal and admin sections)
	mountPortalUI(mux, p, ui.Available())

	// Mount SSE handler (legacy clients)
	wrappedSSE := newSSEHandler(mcpServer, hcfg.requireAuth, rmURL)
	mux.Handle("/sse", wrappedSSE)
	mux.Handle("/message", wrappedSSE)
	log.Println("SSE transport enabled on /sse, /message")

	// Build and mount the root handler (MCP streamable HTTP + session + browser redirect).
	rootHandler := buildRootHandler(mcpServer, p, hcfg)
	mountRootHandler(mux, rootHandler, hcfg, rmURL)

	return listenAndServe(ctx, opts.address, corsMiddleware(mux), hcfg, hc)
}

// buildRootHandler constructs the MCP streamable HTTP handler with optional
// session-aware wrapping. Browser redirect is applied in mountRootHandler
// so it wraps outside the auth gateway.
func buildRootHandler(mcpServer *mcp.Server, p *platform.Platform, hcfg httpConfig) http.Handler {
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout: hcfg.streamableCfg.SessionTimeout,
		Stateless:      hcfg.streamableCfg.Stateless,
	})

	// Wrap with AwareHandler when using external session store
	// (database mode forces Stateless: true on the SDK, and sessions
	// are managed by our handler against the external store).
	var handler http.Handler = streamableHandler
	if p != nil && p.SessionStore() != nil && hcfg.streamableCfg.Stateless {
		handler = session.NewAwareHandler(streamableHandler, session.HandlerConfig{
			Store:       p.SessionStore(),
			TTL:         p.Config().Sessions.TTL,
			Broadcaster: p.Broadcaster(),
		})
		// Platform.Broadcaster() is non-nil after New (initBroadcaster
		// wires postgres or memory). The "+ broadcaster" tag is part
		// of the log line so operators can grep deployments where the
		// session-aware handler is wired with the SSE long-poll path.
		log.Println("Session-aware handler enabled (external session store + broadcaster)")
	}

	return handler
}

// mountRootHandler registers the root handler on the mux, optionally wrapping
// it with the MCP auth gateway when authentication is required.
// Browser redirect wraps OUTSIDE the auth gateway so that browser requests
// (Accept: text/html) redirect to /portal/ without hitting the 401.
func mountRootHandler(mux *http.ServeMux, rootHandler http.Handler, hcfg httpConfig, rmURL string) {
	handler := rootHandler
	if hcfg.requireAuth {
		handler = httpauth.MCPAuthGateway(rmURL)(handler)
		log.Println("Streamable HTTP transport enabled on / (auth required)")
	} else {
		log.Println("Streamable HTTP transport enabled on / (anonymous)")
	}

	if hcfg.portalUI {
		handler = browserRedirectMiddleware(handler)
	}

	mux.Handle("/", handler)
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

	go func() { // #nosec G118 -- ctx is the application-level shutdown context, not a request-scoped context
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

// stdioMarker is the conventional marker for stdin/stdout in CLI tools.
const stdioMarker = "-"

func runMigrateConfig(args []string) error {
	fs := flag.NewFlagSet("migrate-config", flag.ExitOnError)
	configPath := fs.String("config", stdioMarker, "Config file path (- for stdin)")
	outputPath := fs.String("output", stdioMarker, "Output file path (- for stdout)")
	targetVersion := fs.String("target-version", "", "Target config version (default: latest)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	var r io.Reader
	if *configPath == stdioMarker {
		r = os.Stdin
	} else {
		// #nosec G304 -- path is from CLI args, controlled by admin
		f, err := os.Open(*configPath)
		if err != nil {
			return fmt.Errorf("opening config: %w", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}

	var w io.Writer
	if *outputPath == stdioMarker {
		w = os.Stdout
	} else {
		// #nosec G304 -- path is from CLI args, controlled by admin
		f, err := os.Create(*outputPath)
		if err != nil {
			return fmt.Errorf("creating output: %w", err)
		}
		defer func() { _ = f.Close() }()
		w = f
	}

	if err := platform.MigrateConfig(r, w, *targetVersion); err != nil {
		return fmt.Errorf("migrating config: %w", err)
	}
	return nil
}

// mountAdminAPI registers the admin REST API on the mux if enabled.
func mountAdminAPI(mux *http.ServeMux, p *platform.Platform) {
	if p == nil || !p.Config().Admin.Enabled {
		return
	}
	adminHandler := buildAdminHandler(p)
	prefix := p.Config().Admin.PathPrefix
	mux.Handle(prefix+"/", adminHandler)
	log.Println("Admin API enabled on", prefix)
}

// mountPortalAPI registers the portal REST API on the mux if portal is enabled.
// portalDisabled returns true when portal is explicitly disabled or platform is nil.
func portalDisabled(p *platform.Platform) bool {
	if p == nil {
		return true
	}
	e := p.Config().Portal.Enabled
	return e != nil && !*e
}

func mountPortalAPI(mux *http.ServeMux, p *platform.Platform) {
	if p == nil || portalDisabled(p) {
		return
	}
	if p.PortalAssetStore() == nil || p.PortalShareStore() == nil {
		log.Println("Portal enabled but stores not available (database required)")
		return
	}

	var portalAuthOpts []portal.AuthenticatorOption
	if p.BrowserSessionAuth() != nil {
		portalAuthOpts = append(portalAuthOpts, portal.WithBrowserAuth(p.BrowserSessionAuth()))
	}
	portalAuth := portal.NewAuthenticator(p.Authenticator(), portalAuthOpts...)

	var adminRoles []string
	if pr := p.PersonaRegistry(); pr != nil {
		if adminP, ok := pr.Get(p.Config().Admin.Persona); ok {
			adminRoles = adminP.Roles
		}
	}

	// Platform brand (far right): prefer mcpapps platform-info config, then portal title.
	brandName := mcpappsBrandName(p)
	if brandName == "" {
		brandName = p.Config().Portal.Title
	}
	if brandName == "" {
		brandName = p.Config().Server.Name
	}

	deps := portal.Deps{
		AssetStore:      p.PortalAssetStore(),
		ShareStore:      p.PortalShareStore(),
		VersionStore:    p.PortalVersionStore(),
		CollectionStore: p.PortalCollectionStore(),
		S3Client:        p.PortalS3Client(),
		S3Bucket:        p.Config().Portal.S3Bucket,
		PublicBaseURL:   p.Config().Portal.PublicBaseURL,
		RateLimit: portal.RateLimitConfig{
			RequestsPerMinute: p.Config().Portal.RateLimit.RequestsPerMinute,
			BurstSize:         p.Config().Portal.RateLimit.BurstSize,
		},
		OIDCEnabled:        p.BrowserSessionFlow() != nil,
		AdminRoles:         adminRoles,
		PromptStore:        p.PromptStore(),
		PromptRegistrar:    p,
		PromptInfoProvider: p,
		BrandName:          brandName,
		BrandLogoSVG:       p.BrandLogoSVG(),
		BrandURL:           p.BrandURL(),
		ImplementorName:    p.Config().Portal.Implementor.Name,
		ImplementorLogoSVG: p.ResolveImplementorLogo(),
		ImplementorURL:     p.Config().Portal.Implementor.URL,
	}

	wirePortalOptionalDeps(&deps, p)

	handler := portal.NewHandler(deps, portal.RequirePortalAuth(portalAuth))
	mux.Handle("/api/v1/portal/", handler)
	mux.Handle("/portal/view/", handler)
	log.Println("Portal API enabled on /api/v1/portal/")
}

// mountResourcesAPI registers the managed resources REST API on the mux if enabled.
func mountResourcesAPI(mux *http.ServeMux, p *platform.Platform) {
	if p == nil || p.ResourceStore() == nil {
		return
	}

	var portalAuthOpts []portal.AuthenticatorOption
	if p.BrowserSessionAuth() != nil {
		portalAuthOpts = append(portalAuthOpts, portal.WithBrowserAuth(p.BrowserSessionAuth()))
	}
	portalAuth := portal.NewAuthenticator(p.Authenticator(), portalAuthOpts...)

	pr := p.PersonaRegistry()
	adminPersona := p.Config().Admin.Persona
	extractClaims := func(r *http.Request) (*resource.Claims, error) {
		user, err := portalAuth.Authenticate(r)
		if err != nil || user == nil {
			return nil, fmt.Errorf("authentication required")
		}
		return buildResourceClaims(user, pr, adminPersona), nil
	}

	deps := resource.Deps{
		Store:     p.ResourceStore(),
		S3Client:  p.ResourceS3Client(),
		S3Bucket:  p.Config().Resources.Managed.S3Bucket,
		URIScheme: p.Config().Resources.Managed.URIScheme,
		OnCreate:  p.RegisterManagedResource,
		OnDelete:  p.UnregisterManagedResource,
	}

	handler := resource.NewHandler(deps, extractClaims, nil)
	mux.Handle("/api/v1/resources/", handler)
	mux.Handle("/api/v1/resources", handler)
	log.Println("Managed resources API enabled on /api/v1/resources")
}

// mountGatewayAPI registers the REST shim for the apigateway toolkit
// on the mux. The shim is only mounted when at least one apigateway
// toolkit instance is loaded; otherwise the route would always return
// "connection not found" and add noise to the route table.
//
// Auth wrapping mirrors the MCP root handler: when requireAuth is on,
// the handler is wrapped with httpauth.RequireAuth so that requests
// without a credential are rejected at the HTTP layer before reaching
// the in-memory MCP session. When auth is off, the wrapper is a no-op
// and the request flows through anonymously (matching the rest of the
// platform's behavior in that mode).
// compositeOperationResolver fans an operationId lookup across every
// registered apigateway toolkit (a multi-instance deployment splits
// connections across toolkits). The first non-empty result wins; an
// empty result means no toolkit owns the connection or no spec path
// matched, which the metrics middleware maps to "unknown".
type compositeOperationResolver []*apigatewaykit.Toolkit

func (c compositeOperationResolver) ResolveOperationID(ctx context.Context, connection, method, path string) string {
	for _, tk := range c {
		if op := tk.ResolveOperationID(ctx, connection, method, path); op != "" {
			return op
		}
	}
	return ""
}

func (c compositeOperationResolver) HasConnection(connection string) bool {
	for _, tk := range c {
		if tk.HasConnection(connection) {
			return true
		}
	}
	return false
}

func mountGatewayAPI(mux *http.ServeMux, mcpServer *mcp.Server, p *platform.Platform, requireAuth bool) {
	if mcpServer == nil || p == nil {
		return
	}
	apiToolkits := p.ToolkitRegistry().GetByKind(apigatewaykit.Kind)
	if len(apiToolkits) == 0 {
		return
	}

	resolver := make(compositeOperationResolver, 0, len(apiToolkits))
	for _, tk := range apiToolkits {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			resolver = append(resolver, api)
		}
	}

	handler, err := gatewayhttp.NewHandler(gatewayhttp.Deps{
		MCPServer:   mcpServer,
		Metrics:     p.Metrics(),
		Resolver:    resolver,
		Identity:    p.NewGatewayIdentityResolver(),
		RawMaxBytes: p.APIGatewayRawMaxBytes(),
	})
	if err != nil {
		log.Printf("REST gateway disabled: %v", err)
		return
	}

	wrapped := handler
	if requireAuth {
		wrapped = httpauth.RequireAuth()(handler)
	}
	mux.Handle("/api/v1/gateway/", wrapped)
	log.Println("REST gateway enabled on /api/v1/gateway/{connection}/invoke")
}

// defaultPrometheusURL is the auto-discovered in-cluster Prometheus endpoint
// used when observability.prometheus.url is not configured. Set that config
// value only to point at a Prometheus deployed under a different name.
const defaultPrometheusURL = "http://mcp-data-platform-prometheus:9090"

// mountObservabilityProxy mounts the authenticated PromQL query proxy at
// /api/v1/observability/. It is always mounted (gated behind auth +
// the observability:read persona capability); when Prometheus is not
// configured its endpoints return 503 so the portal renders a clean
// empty state.
func mountObservabilityProxy(mux *http.ServeMux, p *platform.Platform, requireAuth bool) {
	if p == nil {
		return
	}
	pc := p.Config().Observability.Prometheus
	if pc.URL == "" {
		// Auto-discover the default in-cluster Prometheus; the config only
		// needs to override this to point at a non-default deployment.
		pc.URL = defaultPrometheusURL
	}
	handler, err := proxy.New(proxy.Config{
		URL:                pc.URL,
		Timeout:            pc.Timeout,
		BasicAuthUser:      pc.BasicAuth.Username,
		BasicAuthPass:      pc.BasicAuth.Password,
		RateLimitPerSecond: pc.RateLimitPerSecond,
	}, p.NewObservabilityAuthorizer())
	if err != nil {
		log.Printf("observability proxy disabled: %v", err)
		return
	}

	proxyMux := http.NewServeMux()
	handler.Register(proxyMux)

	var wrapped http.Handler = proxyMux
	if requireAuth {
		// The portal SPA calls these endpoints directly with its
		// browser-session cookie, so accept that (like the admin and
		// portal APIs) in addition to Bearer/API-key tokens. The proxy's
		// authorizer enforces authentication (401) and the
		// observability:read capability (403); OptionalAuth only lifts a
		// present token onto the context without rejecting cookie auth.
		wrapped = p.ObservabilityAuthMiddleware()(httpauth.OptionalAuth()(proxyMux))
	}
	mux.Handle("/api/v1/observability/", wrapped)
	if pc.URL == "" {
		log.Println("observability proxy mounted (Prometheus not configured; endpoints return 503)")
	} else {
		log.Println("observability proxy enabled on /api/v1/observability/{query,query_range}")
	}
}

// buildResourceClaims creates resource Claims from an authenticated user,
// resolving persona memberships and admin status from the persona registry.
func buildResourceClaims(user *portal.User, pr *persona.Registry, adminPersona string) *resource.Claims {
	claims := &resource.Claims{
		Sub:   user.UserID,
		Email: user.Email,
		Roles: user.Roles,
	}
	if pr != nil {
		for _, per := range pr.All() {
			if matchesAnyRole(per.Roles, user.Roles) {
				claims.Personas = append(claims.Personas, per.Name)
				if per.Name == adminPersona {
					claims.IsAdmin = true
				}
			}
		}
	}
	claims.AdminOfPersonas = extractPersonaAdminRoles(user.Roles)
	return claims
}

// personaAdminInfix is the role substring that marks a persona-admin grant.
// Roles may carry an arbitrary prefix (e.g., "dp_persona-admin:finance").
const personaAdminInfix = "persona-admin:"

// extractPersonaAdminRoles extracts persona names from roles containing
// the "persona-admin:" pattern, tolerating any prefix.
func extractPersonaAdminRoles(roles []string) []string {
	var out []string
	for _, r := range roles {
		if _, name, ok := strings.Cut(r, personaAdminInfix); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// matchesAnyRole checks if any persona role matches any user role.
func matchesAnyRole(personaRoles, userRoles []string) bool {
	for _, pr := range personaRoles {
		if slices.Contains(userRoles, pr) {
			return true
		}
	}
	return false
}

// mcpappsBrandName extracts brand_name from the mcpapps platform-info config,
// or returns empty string if not configured.
func mcpappsBrandName(p *platform.Platform) string {
	appCfg, ok := p.Config().MCPApps.Apps["platform-info"]
	if !ok {
		return ""
	}
	name, _ := appCfg.Config["brand_name"].(string)
	return name
}

// wirePortalOptionalDeps populates optional portal dependencies (audit, knowledge, persona).
func wirePortalOptionalDeps(deps *portal.Deps, p *platform.Platform) {
	if p.AuditStore() != nil {
		deps.AuditMetrics = p.AuditStore()
	}
	if p.KnowledgeInsightStore() != nil {
		deps.InsightStore = p.KnowledgeInsightStore()
	}
	if p.MemoryStore() != nil {
		deps.MemoryStore = p.MemoryStore()
	}
	if pr := p.PersonaRegistry(); pr != nil {
		tr := p.ToolkitRegistry()
		deps.PersonaResolver = buildPersonaResolver(pr, tr)
	}
}

// buildPersonaResolver creates a portal.PersonaResolver from the persona and toolkit registries.
func buildPersonaResolver(pr *persona.Registry, tr *registry.Registry) portal.PersonaResolver {
	return func(roles []string) *portal.PersonaInfo {
		per, ok := pr.GetForRoles(roles)
		if !ok || per == nil {
			return nil
		}
		info := &portal.PersonaInfo{Name: per.Name}
		if tr != nil {
			filter := persona.NewToolFilter(pr)
			info.Tools = filter.FilterTools(per, tr.AllTools())
		}
		return info
	}
}

// mountPortalUI registers the unified portal SPA frontend on the mux when the
// portal UI config gate is enabled and assets are available.
func mountPortalUI(mux *http.ServeMux, p *platform.Platform, assetsAvailable bool) {
	if p == nil || portalDisabled(p) || !assetsAvailable {
		return
	}
	mux.Handle("/portal/", http.StripPrefix("/portal", ui.Handler()))
	log.Println("Portal UI enabled on /portal/")
}

// buildAdminHandler constructs the admin REST API handler from the platform.
func buildAdminHandler(p *platform.Platform) http.Handler {
	var authOpts []admin.PlatformAuthOption
	if p.BrowserSessionAuth() != nil {
		authOpts = append(authOpts, admin.WithBrowserSessionAuth(p.BrowserSessionAuth()))
	}
	platAuth := admin.NewPlatformAuthenticator(
		p.Authenticator(),
		p.Config().Admin.Persona,
		p.PersonaRegistry(),
		authOpts...,
	)

	deps := admin.Deps{
		Config:             p.Config(),
		ConfigStore:        p.ConfigStore(),
		FileDefaults:       p.FileDefaults(),
		PersonaRegistry:    p.PersonaRegistry(),
		ToolkitRegistry:    p.ToolkitRegistry(),
		ReloadNotifier:     p,
		MCPServer:          p.MCPServer(),
		BrowserAuth:        p.BrowserSessionAuth(),
		DatabaseAvailable:  p.Config().Database.DSN != "",
		PlatformTools:      p.PlatformTools(),
		AssetStore:         p.PortalAssetStore(),
		ShareStore:         p.PortalShareStore(),
		VersionStore:       p.PortalVersionStore(),
		S3Client:           p.PortalS3Client(),
		S3Bucket:           p.Config().Portal.S3Bucket,
		ConnectionStore:    p.ConnectionStore(),
		ConnectionSources:  p.ConnectionSources(),
		EnrichmentStore:    p.EnrichmentStore(),
		ToolkitsConfig:     p.Config().Toolkits,
		PersonaStore:       p.PersonaStore(),
		APIKeyStore:        p.APIKeyStore(),
		PromptStore:        p.PromptStore(),
		PromptRegistrar:    p,
		PromptInfoProvider: p,
		FilePersonaNames:   p.FilePersonaNames(),
	}

	if p.AuditStore() != nil {
		deps.AuditQuerier = p.AuditStore()
		deps.AuditMetricsQuerier = p.AuditStore()
	}

	// Note: WireGatewayTokenStore and WireGatewayBroadcaster run earlier
	// in startHTTPServer so they apply even when admin is disabled.
	if engine := wireEnrichmentEngine(p); engine != nil {
		deps.EnrichmentEngine = engine
	}

	// PKCE state: DB-backed (multi-replica safe) when a database is
	// configured. otherwise an in-memory store with a background GC
	// goroutine is wired here — single-replica only.
	if db := p.DB(); db != nil {
		deps.PKCEStore = admin.NewPostgresPKCEStore(db, p.RestEncryptor())
	} else {
		deps.PKCEStore = admin.NewMemoryPKCEStore()
	}

	// Wire the unified OAuth flow: shared connoauth.Store plus one
	// OAuthKindHandler per connection kind. When both are present the
	// admin handler activates the unified /connections/{kind}/{name}
	// routes; otherwise it falls back to the legacy per-kind routes
	// for backward compatibility during rollout.
	deps.ConnOAuthStore = p.ConnOAuthStore()
	deps.OAuthKinds = buildOAuthKindHandlers(p)
	deps.AuthEvents = p.AuthEventWriter()
	deps.AuthEventStore = p.AuthEventStore()
	if catStore := p.APIGatewayCatalogStore(); catStore != nil {
		deps.APICatalogStore = catStore
	}
	deps.Embedder = p.EmbeddingProvider()
	if jobs := p.APIGatewayEmbedJobsStore(); jobs != nil {
		deps.EmbedJobs = jobs
	}
	if reporter := p.IndexJobsReporter(); reporter != nil {
		deps.IndexJobs = reporter
	}

	if p.KnowledgeInsightStore() != nil {
		deps.Knowledge = admin.NewKnowledgeHandler(
			p.KnowledgeInsightStore(),
			p.KnowledgeChangesetStore(),
			p.KnowledgeDataHubWriter(),
		)
	}

	if p.MemoryStore() != nil {
		deps.Memory = admin.NewMemoryHandler(p.MemoryStore())
	}

	if p.APIKeyAuthenticator() != nil {
		deps.APIKeyManager = p.APIKeyAuthenticator()
	}

	return admin.NewHandler(deps, admin.RequirePersona(platAuth))
}

// connOAuthConfigResolver bridges the connoauth refresher's
// ConfigResolver interface to the platform's ConnectionStore +
// OAuthKindHandlers wiring. The refresher cannot import the platform
// package directly (import cycle), so this adapter lives in main.go
// where both packages are already imported.
type connOAuthConfigResolver struct {
	store     admin.ConnectionStore
	kinds     admin.OAuthKindHandlers
	maxLifeFn func(kind, name string, cfg map[string]any) time.Duration
}

// ResolveConfig fetches the connection_instances row for (kind, name)
// and parses out the connoauth.Config via the per-kind handler. The
// ErrConfigNotResolvable sentinel is returned when the connection no
// longer exists OR is configured for a non-OAuth auth mode — the
// refresher treats either as "skip" rather than "fail" so a stale
// token row doesn't stall keepalive for other connections.
func (r *connOAuthConfigResolver) ResolveConfig(ctx context.Context, key connoauth.Key) (connoauth.Config, error) {
	handler, ok := r.kinds[key.Kind]
	if !ok {
		return connoauth.Config{}, connoauth.ErrConfigNotResolvable
	}
	inst, err := r.store.Get(ctx, key.Kind, key.Name)
	if err != nil {
		return connoauth.Config{}, connoauth.ErrConfigNotResolvable
	}
	cfg, err := handler.ParseOAuthConfig(inst.Config)
	if err != nil {
		return connoauth.Config{}, connoauth.ErrConfigNotResolvable
	}
	return cfg, nil
}

// MaxLifetime reads the per-connection oauth2_refresh_max_lifetime
// config field. Zero when unset; the refresher then relies on
// IdP-disclosed deadlines only (which is correct for Keycloak / Auth0
// / Okta but inadequate for Microsoft / Salesforce / Google APIs
// that don't disclose refresh-token deadlines but enforce wall-clock
// max lifetimes anyway).
func (r *connOAuthConfigResolver) MaxLifetime(ctx context.Context, key connoauth.Key) time.Duration {
	if r.maxLifeFn == nil {
		return 0
	}
	inst, err := r.store.Get(ctx, key.Kind, key.Name)
	if err != nil {
		return 0
	}
	return r.maxLifeFn(key.Kind, key.Name, inst.Config)
}

// readMaxLifetime extracts the operator-configured wall-clock max
// lifetime for the refresh token, parsing the standard Go duration
// string format ("60d" via the d-suffix helper). Returns zero when
// the field is absent, empty, or unparseable — the refresher
// gracefully degrades to IdP-disclosed-deadline-only mode in that
// case.
func readMaxLifetime(_, _ string, cfg map[string]any) time.Duration {
	raw, _ := cfg[configKeyOAuthRefreshMaxLifetime].(string)
	if raw == "" {
		return 0
	}
	d, err := parseDurationWithDays(raw)
	if err != nil {
		return 0
	}
	return d
}

// configKeyOAuthRefreshMaxLifetime is the connection_instances
// config key that holds the operator's wall-clock refresh-token max
// lifetime hint. Stored as a duration string ("60d", "90d", "30d").
const configKeyOAuthRefreshMaxLifetime = "oauth2_refresh_max_lifetime"

// hoursPerDay names the magic number 24 so the lint rule on
// numeric literals doesn't fire and so the math reads as intent.
const hoursPerDay = 24

// parseDurationWithDays is time.ParseDuration with a "d" suffix
// shorthand added. The stdlib's time.ParseDuration tops out at "h",
// so "60d" is unparseable. Refresh-token deadlines are routinely
// expressed in days by operators (Microsoft 90d, Salesforce 30d), so
// asking them to write "1440h" instead would be a thousand-cuts UX
// failure.
func parseDurationWithDays(s string) (time.Duration, error) {
	if head, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(head)
		if err != nil {
			return 0, fmt.Errorf("parse duration %q: %w", s, err)
		}
		if days < 0 {
			return 0, fmt.Errorf("parse duration %q: negative days", s)
		}
		return time.Duration(days) * hoursPerDay * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}
	return d, nil
}

// startConnOAuthRefresher kicks off the keepalive loop. Called after
// the toolkit registry + connection store are wired so the resolver
// can read connection_instances rows. multi-replica is taken from
// the platform's session-store mode — database-backed sessions
// implies multi-replica intent.
func startConnOAuthRefresher(p *platform.Platform) {
	if p.ConnOAuthStore() == nil {
		return
	}
	store := p.ConnectionStore()
	if store == nil {
		return
	}
	kinds := buildOAuthKindHandlers(p)
	if len(kinds) == 0 {
		return
	}
	resolver := &connOAuthConfigResolver{
		store:     store,
		kinds:     kinds,
		maxLifeFn: readMaxLifetime,
	}
	multiReplica := p.Config() != nil && p.Config().Sessions.Store == platform.SessionStoreDatabase
	p.StartConnOAuthRefresher(resolver, multiReplica)
}

// buildOAuthKindHandlers assembles the per-kind OAuth adapter registry
// the admin handler dispatches on. Each registered toolkit kind
// contributes one handler; missing toolkits produce no entry, and the
// unified handler returns 400 "unsupported connection kind" for
// requests targeting an unregistered kind.
func buildOAuthKindHandlers(p *platform.Platform) admin.OAuthKindHandlers {
	out := admin.OAuthKindHandlers{}
	if p.ToolkitRegistry() == nil {
		return out
	}
	for _, tk := range p.ToolkitRegistry().All() {
		switch v := tk.(type) {
		case *gatewaykit.Toolkit:
			if h := gatewaykit.NewOAuthKindHandler(v); h != nil {
				out[connoauth.KindMCP] = h
			}
		case *apigatewaykit.Toolkit:
			out[connoauth.KindAPI] = apigatewaykit.NewOAuthKindHandler(v)
		}
	}
	return out
}

// wireEnrichmentEngine builds the gateway enrichment engine when a rule
// store is available, registers the built-in source adapters (Trino,
// DataHub) bound to the platform's live toolkits, and attaches the
// engine to the live gateway toolkit so forwarded calls pick it up.
func wireEnrichmentEngine(p *platform.Platform) *enrichment.Engine {
	store := p.EnrichmentStore()
	if store == nil {
		return nil
	}
	sourceReg := enrichment.NewSourceRegistry()
	registerEnrichmentSources(p, sourceReg)

	engine := enrichment.NewEngine(store, sourceReg)
	for _, tk := range p.ToolkitRegistry().All() {
		gw, ok := tk.(*gatewaykit.Toolkit)
		if !ok {
			continue
		}
		gw.SetEnrichmentEngine(engine)
	}
	return engine
}

// registerEnrichmentSources binds source adapters to the platform's
// active toolkits. A toolkit that isn't running results in no
// registration for that source — rules referencing it will surface a
// "source not registered" warning at evaluation time.
func registerEnrichmentSources(p *platform.Platform, reg *enrichment.SourceRegistry) {
	if exec := buildTrinoQueryFunc(p); exec != nil {
		reg.Register(sources.NewTrinoSource(exec))
	}
	if getEntity, getTerm := buildDataHubFuncs(p); getEntity != nil || getTerm != nil {
		// DataHub source registers even when only a subset of operations
		// is wired; missing operations report unsupported on dispatch.
		reg.Register(sources.NewDataHubSource(getEntity, getTerm))
	}
}

// buildTrinoQueryFunc returns a TrinoQueryFunc bound to the live trino
// toolkit's manager, or nil if no trino toolkit is registered.
func buildTrinoQueryFunc(p *platform.Platform) sources.TrinoQueryFunc {
	for _, tk := range p.ToolkitRegistry().All() {
		trinoTk, ok := tk.(*trinokit.Toolkit)
		if !ok || trinoTk.Manager() == nil {
			continue
		}
		mgr := trinoTk.Manager()
		return func(ctx context.Context, connection, sql string) ([]map[string]any, error) {
			c, err := mgr.Client(connection)
			if err != nil {
				return nil, fmt.Errorf("trino manager: %w", err)
			}
			res, qerr := c.Query(ctx, sql, trinoclient.DefaultQueryOptions())
			if qerr != nil {
				return nil, fmt.Errorf("trino query: %w", qerr)
			}
			return res.Rows, nil
		}
	}
	return nil
}

// buildDataHubFuncs returns get-entity and get-glossary-term closures
// bound to the live datahub toolkit's client, or nils if no datahub
// toolkit is registered.
func buildDataHubFuncs(p *platform.Platform) (sources.DataHubGetEntityFunc, sources.DataHubGetGlossaryTermFunc) {
	for _, tk := range p.ToolkitRegistry().All() {
		dhTk, ok := tk.(*datahubkit.Toolkit)
		if !ok || dhTk.Client() == nil {
			continue
		}
		client := dhTk.Client()
		getEntity := func(ctx context.Context, urn string) (any, error) {
			return client.GetEntity(ctx, urn)
		}
		getTerm := func(ctx context.Context, urn string) (any, error) {
			return client.GetGlossaryTerm(ctx, urn)
		}
		return getEntity, getTerm
	}
	return nil, nil
}

// mountBrowserAuth registers the OIDC login/callback/logout routes.
func mountBrowserAuth(mux *http.ServeMux, p *platform.Platform) {
	if p == nil || p.BrowserSessionFlow() == nil {
		return
	}
	flow := p.BrowserSessionFlow()
	mux.HandleFunc("/portal/auth/login", flow.LoginHandler)
	mux.HandleFunc("/portal/auth/callback", flow.CallbackHandler)
	mux.HandleFunc("/portal/auth/logout", flow.LogoutHandler)
	log.Println("Browser auth enabled (OIDC login on /portal/auth/login)")
}

// browserRedirectMiddleware redirects browser requests to the portal.
// Non-browser requests (MCP clients) pass through to the MCP handler.
func browserRedirectMiddleware(mcpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/portal/", http.StatusTemporaryRedirect)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
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
