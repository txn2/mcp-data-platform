// Package httpserver assembles the platform's HTTP surface: the mux and route
// table (MCP streamable HTTP, SSE, OAuth, admin/portal/resources/gateway/
// observability REST APIs, portal UI), CORS, and the drain/shutdown sequencing
// that lets connected agents reconnect to a new build. main.go owns flag
// parsing, config load, platform construction, signal handling, and the
// run/shutdown loop; it hands a fully-constructed platform to Serve.
package httpserver

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	_ "github.com/txn2/mcp-data-platform/internal/apidocs" // Swagger API docs
	"github.com/txn2/mcp-data-platform/internal/httpserver/health"
	"github.com/txn2/mcp-data-platform/internal/httpserver/httpauth"
	"github.com/txn2/mcp-data-platform/internal/ui"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

const (
	defaultReadHeaderTimeout = 10 * time.Second
	fallbackGracePeriod      = 25 * time.Second
	fallbackPreShutdownDelay = 2 * time.Second
)

// logKeyError is the structured-log key for an error value.
const logKeyError = "error"

// sessionDrainSettle is how long in-flight requests are given to finish before
// live MCP sessions are closed on shutdown (#675). Long-lived SSE and streamable
// streams never go idle, so until the sessions are closed the agent stays on the
// old build; closing them drops the stream so Claude Code auto-reconnects to the
// new build. A var so tests can lower it; capped at the grace period at use.
var sessionDrainSettle = 3 * time.Second

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
	// mcpServer is closed-out on shutdown so connected agents reconnect to the new
	// build (#675). Carried on the config so listenAndServe stays within its arg budget.
	mcpServer *mcp.Server
	// authenticator is the same auth entry point the protocol middleware uses. The
	// MCP auth gateway calls it to reject a present-but-invalid token at the HTTP
	// layer with 401 + WWW-Authenticate (#926). Nil when the platform is nil
	// (tests) — the gate then stays presence-only.
	authenticator middleware.Authenticator
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
		cfg.authenticator = p.Authenticator()
	}
	return cfg
}

// newSSEHandler creates an SSE handler with auth middleware. When auth is
// required it always routes through RequireAuthWithOAuth so the invalid-token
// gate (#926) fires on SSE, matching the streamable transport (mountRootHandler).
// With no OAuth server, resourceMetadataURL is empty and the 401 simply omits the
// resource_metadata parameter.
func newSSEHandler(mcpServer *mcp.Server, requireAuth bool, resourceMetadataURL string, authenticator middleware.Authenticator) http.Handler {
	sseHandler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	if requireAuth {
		return httpauth.RequireAuthWithOAuth(authenticator, resourceMetadataURL)(sseHandler)
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

// Serve assembles the full HTTP mux (MCP streamable HTTP + SSE, OAuth, admin/
// portal/resources/gateway/observability REST APIs, portal UI, health) and
// blocks serving it on address until ctx is canceled, then drains gracefully.
// Gateway/api-gateway integrations (broadcaster, token stores, catalog
// store, embed-job queue) and the admin self-connection are wired by
// p.WireRuntime in the caller, which runs before this HTTP setup. That
// keeps their ordering in one place (#854): the SSE long-poll path still
// gets the broadcaster wired into the gateway toolkit before the listener
// comes up at the end of this function, so AwareHandler never accepts a
// subscriber ahead of the wiring.
func Serve(ctx context.Context, mcpServer *mcp.Server, p *platform.Platform, address string) error {
	if p != nil {
		// Start the background OAuth refresher once toolkits and
		// connection store are wired. Single-call here (not in the
		// platform constructor) so the resolver can read the live
		// ConnectionStore + OAuthKindHandlers from the fully-set-up
		// platform — these are not available at platform.New time.
		startConnOAuthRefresher(p)
	}

	// Email notification substrate (queue + send worker + LISTEN adapter).
	// Owned by this composition root: started before the surfaces that
	// enqueue into it mount, stopped after the HTTP server drains.
	notify := buildNotifications(p)
	notify.Start(ctx)
	defer notify.Stop()

	// Scheduled knowledge review-queue staleness check (#803). It enqueues
	// through the substrate above, so it starts after it and stops before it.
	reviewAlert := buildReviewAlert(p, notify)
	reviewAlert.Start(ctx)
	defer reviewAlert.Stop()

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

	// Mount the no-login notification opt-out linked from email footers (#1001)
	mountNotificationUnsubscribe(mux, p, notify)

	// Mount admin API if enabled
	mountAdminAPI(mux, p, notify)

	// The built-in platform-admin self-connection (issue #543) that lets an
	// admin drive /api/v1/admin/* through the api gateway is seeded by
	// p.WireRuntime (caller), after the gateway integrations it depends
	// on. The admin API mounted just above is the loopback surface it targets.

	// Mount portal API if enabled
	if err := mountPortalAPI(mux, p, notify); err != nil {
		return err
	}

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
	wrappedSSE := newSSEHandler(mcpServer, hcfg.requireAuth, rmURL, hcfg.authenticator)
	mux.Handle("/sse", wrappedSSE)
	mux.Handle("/message", wrappedSSE)
	log.Println("SSE transport enabled on /sse, /message")

	// Build and mount the root handler (MCP streamable HTTP + session + browser redirect).
	rootHandler := buildRootHandler(mcpServer, p, hcfg)
	mountRootHandler(mux, rootHandler, hcfg, rmURL)

	hcfg.mcpServer = mcpServer
	return listenAndServe(ctx, address, corsMiddleware(mux), hcfg, hc)
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
		// Platform.Broadcaster() is non-nil after New (the sessionsync
		// layer wires postgres or memory). The "+ broadcaster" tag is part
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
		handler = httpauth.MCPAuthGateway(hcfg.authenticator, rmURL)(handler)
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

		slog.Info("shutdown: draining HTTP connections", "grace_period", gracePeriod)
		drainHTTPServer(server, hcfg.mcpServer, gracePeriod)
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

// drainHTTPServer gracefully drains the HTTP server, then closes lingering MCP
// sessions so connected agents reconnect to the new build (#675). Long-lived
// SSE/streamable streams never go idle, so server.Shutdown does not complete until
// its grace deadline and the agent stays on the old build the whole time. It runs
// concurrently: in-flight requests get a brief settle, then the sessions are closed
// to drop those streams and trigger client auto-reconnect.
func drainHTTPServer(server *http.Server, mcpServer *mcp.Server, gracePeriod time.Duration) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), gracePeriod)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- server.Shutdown(shutdownCtx) }()

	settle := min(sessionDrainSettle, gracePeriod)
	select {
	case err := <-done:
		logHTTPDrainResult(err)
	case <-time.After(settle):
		closeMCPSessions(shutdownCtx, mcpServer)
		logHTTPDrainResult(<-done)
	}
}

// closeMCPSessions closes every live MCP session so connected clients drop their
// stale connection and reconnect to the new build (#675). Claude Code auto-reconnects
// HTTP/SSE servers and re-handshakes (fresh tools/list); Claude Desktop requires an
// app restart.
//
// ServerSession.Close is graceful: an idle session's long-lived SSE/streamable stream
// drops immediately, but a session with an in-flight tool call blocks until that call
// returns, and that wait is not bounded by the HTTP grace period. So the closes run in
// a goroutine bounded by ctx (the shutdown deadline): if they do not all finish in
// time, we return and let process exit drop the remaining connections rather than hang
// past terminationGracePeriodSeconds and risk a SIGKILL mid-call.
func closeMCPSessions(ctx context.Context, mcpServer *mcp.Server) {
	if mcpServer == nil {
		return
	}
	var sessions []*mcp.ServerSession
	for s := range mcpServer.Sessions() {
		sessions = append(sessions, s)
	}
	if len(sessions) == 0 {
		return
	}

	closed := make(chan struct{})
	go func() {
		for _, s := range sessions {
			_ = s.Close() // returns once the session's in-flight requests finish
		}
		close(closed)
	}()

	select {
	case <-closed:
		slog.Info("shutdown: closed live MCP sessions so clients reconnect to the new build", "count", len(sessions))
	case <-ctx.Done():
		slog.Warn("shutdown: MCP session close did not finish before the grace deadline; process exit will drop remaining connections", "count", len(sessions))
	}
}

// logHTTPDrainResult records the outcome of the HTTP server drain.
func logHTTPDrainResult(err error) {
	if err != nil {
		slog.Error("shutdown: HTTP drain error", logKeyError, err)
		return
	}
	slog.Info("shutdown: HTTP server stopped")
}
