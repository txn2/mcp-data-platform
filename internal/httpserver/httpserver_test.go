package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpserver/health"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

const (
	testSessionTimeout = 10 * time.Minute
	testGracePeriod20s = 20 * time.Second
	testPreDelay3s     = 3 * time.Second
	// transportHTTP mirrors the production transport label the streamable
	// HTTP integration tests pass to the tool-call middleware.
	transportHTTP = "http"
)

func TestRegisterOAuthRoutes(t *testing.T) {
	mux := http.NewServeMux()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	registerOAuthRoutes(mux, handler)

	// Test all registered routes
	routes := []string{
		// Standard paths (with /oauth prefix)
		"/.well-known/oauth-authorization-server",
		"/oauth/authorize",
		"/oauth/callback",
		"/oauth/token",
		"/oauth/register",
		// Claude Desktop compatibility paths (without /oauth prefix)
		"/authorize",
		"/callback",
		"/token",
		"/register",
	}

	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, route, http.NoBody)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("route %s: expected status 200, got %d", route, w.Code)
			}
		})
	}
}

func TestCorsMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsMiddleware(inner)

	t.Run("sets CORS headers", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Errorf("Allow-Origin = %q, want %q", got, "https://example.com")
		}

		methods := w.Header().Get("Access-Control-Allow-Methods")
		for _, m := range []string{"GET", "POST", "DELETE", "OPTIONS"} {
			if !strings.Contains(methods, m) {
				t.Errorf("Allow-Methods missing %q: %s", m, methods)
			}
		}

		allowHeaders := w.Header().Get("Access-Control-Allow-Headers")
		for _, h := range []string{"Mcp-Session-Id", "Mcp-Protocol-Version", "X-API-Key", "Last-Event-ID"} {
			if !strings.Contains(allowHeaders, h) {
				t.Errorf("Allow-Headers missing %q: %s", h, allowHeaders)
			}
		}

		exposeHeaders := w.Header().Get("Access-Control-Expose-Headers")
		if !strings.Contains(exposeHeaders, "Mcp-Session-Id") {
			t.Errorf("Expose-Headers missing Mcp-Session-Id: %s", exposeHeaders)
		}

		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want %q", got, "true")
		}
	})

	t.Run("handles OPTIONS preflight", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "OPTIONS", "/mcp", http.NoBody)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("OPTIONS status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("defaults origin to wildcard", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Allow-Origin = %q, want %q", got, "*")
		}
	})
}

func TestExtractHTTPConfig_NilPlatform(t *testing.T) {
	cfg := extractHTTPConfig(nil)
	if cfg.requireAuth {
		t.Error("expected requireAuth false for nil platform")
	}
	if cfg.tlsEnabled {
		t.Error("expected tlsEnabled false for nil platform")
	}
	if cfg.tlsCertFile != "" {
		t.Error("expected empty tlsCertFile for nil platform")
	}
	if cfg.tlsKeyFile != "" {
		t.Error("expected empty tlsKeyFile for nil platform")
	}
	if cfg.streamableCfg.Stateless {
		t.Error("expected stateless false for nil platform")
	}
}

func TestExtractHTTPConfig_WithPlatform(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{
			Name: "test",
			TLS: platform.TLSConfig{
				Enabled:  true,
				CertFile: "/cert.pem",
				KeyFile:  "/key.pem",
			},
			Streamable: platform.StreamableConfig{
				SessionTimeout: testSessionTimeout,
				Stateless:      true,
			},
		},
		Auth: platform.AuthConfig{
			AllowAnonymous: false,
		},
	})
	defer func() { _ = p.Close() }()

	cfg := extractHTTPConfig(p)
	if !cfg.requireAuth {
		t.Error("expected requireAuth true")
	}
	if !cfg.tlsEnabled {
		t.Error("expected tlsEnabled true")
	}
	if cfg.tlsCertFile != "/cert.pem" {
		t.Errorf("tlsCertFile = %q, want /cert.pem", cfg.tlsCertFile)
	}
	if cfg.tlsKeyFile != "/key.pem" {
		t.Errorf("tlsKeyFile = %q, want /key.pem", cfg.tlsKeyFile)
	}
	if cfg.streamableCfg.SessionTimeout != testSessionTimeout {
		t.Errorf("sessionTimeout = %v, want 10m", cfg.streamableCfg.SessionTimeout)
	}
	if !cfg.streamableCfg.Stateless {
		t.Error("expected stateless true")
	}
}

func TestExtractHTTPConfig_AllowAnonymous(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Auth:   platform.AuthConfig{AllowAnonymous: true},
	})
	defer func() { _ = p.Close() }()

	cfg := extractHTTPConfig(p)
	if cfg.requireAuth {
		t.Error("expected requireAuth false when AllowAnonymous is true")
	}
}

func TestNewSSEHandler(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	t.Run("without auth", func(t *testing.T) {
		handler := newSSEHandler(mcpServer, false, "", nil)
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})

	t.Run("with auth no OAuth", func(t *testing.T) {
		handler := newSSEHandler(mcpServer, true, "", nil)
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})

	t.Run("with auth and OAuth", func(t *testing.T) {
		handler := newSSEHandler(mcpServer, true, "https://mcp.example.com/.well-known/oauth-protected-resource", nil)
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})
}

func TestResourceMetadataURL(t *testing.T) {
	t.Run("returns empty for nil platform", func(t *testing.T) {
		if got := resourceMetadataURL(nil); got != "" {
			t.Errorf("resourceMetadataURL(nil) = %q, want empty", got)
		}
	})

	t.Run("returns empty when OAuth not enabled", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
		})
		defer func() { _ = p.Close() }()

		if got := resourceMetadataURL(p); got != "" {
			t.Errorf("resourceMetadataURL = %q, want empty (OAuth not enabled)", got)
		}
	})

	t.Run("returns URL when OAuth enabled", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
			OAuth: platform.OAuthConfig{
				Enabled:    true,
				Issuer:     "https://mcp.example.com",
				SigningKey: "dGVzdC1zaWduaW5nLWtleS0xMjM0NTY3ODkwYWJjZGVm", // base64, 33 bytes
			},
		})
		defer func() { _ = p.Close() }()

		want := "https://mcp.example.com/.well-known/oauth-protected-resource"
		if got := resourceMetadataURL(p); got != want {
			t.Errorf("resourceMetadataURL = %q, want %q", got, want)
		}
	})
}

func TestListenAndServe_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	hc := health.NewChecker()
	hcfg := httpConfig{
		shutdownCfg: platform.ShutdownConfig{
			GracePeriod:      1 * time.Second,
			PreShutdownDelay: 0, // skip delay in tests
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(ctx, "127.0.0.1:0", handler, hcfg, hc)
	}()

	// Give the server a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)

	if !hc.IsReady() {
		t.Error("health checker should be ready after server starts")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("listenAndServe returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listenAndServe did not shut down in time")
	}

	if hc.IsReady() {
		t.Error("health checker should not be ready after shutdown")
	}
}

// TestListenAndServe_ReturnsOnlyAfterTheDrain pins the shutdown ordering the
// deployment budget describes and the background workers depend on: the caller
// stops the notification and managed-script workers as soon as this returns, so
// returning while a request is still being served would tear down the machinery
// that request is using. Shutdown closes the listeners before it waits for
// connections, so the naive return happens mid-drain.
func TestListenAndServe_ReturnsOnlyAfterTheDrain(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		w.WriteHeader(http.StatusOK)
	})

	// Take a port, then hand its address to the server under test.
	ln := listenLocal(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the probe listener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(ctx, addr, handler, httpConfig{
			shutdownCfg: platform.ShutdownConfig{GracePeriod: 5 * time.Second, PreShutdownDelay: time.Millisecond},
		}, nil)
	}()

	// Retry until a request lands: the server is still binding the port this
	// test just released, so a single immediate attempt races the bind and is
	// refused, leaving the handler unentered and this test failing on a
	// timeout that says nothing about the drain it exists to pin. Once a
	// request is in the handler it blocks on release, so Do does not return
	// and the loop stops there.
	go func() {
		for {
			select {
			case <-entered:
				return
			default:
			}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr, http.NoBody)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}

	cancel()
	select {
	case err := <-errCh:
		t.Fatalf("listenAndServe returned mid-drain with a request in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("listenAndServe returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listenAndServe did not return once the drain finished")
	}
}

// TestListenAndServe_BindFailureReturnsInsteadOfWaiting is the other side of
// the drain wait: a server that never bound has no drain to wait for, and
// waiting on one would hang the process on a startup error.
func TestListenAndServe_BindFailureReturnsInsteadOfWaiting(t *testing.T) {
	ln := listenLocal(t)
	defer func() { _ = ln.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(t.Context(), ln.Addr().String(), http.NewServeMux(), httpConfig{}, nil)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error for an address already in use")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a listen failure must return rather than wait on a drain that never runs")
	}
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func sessionCount(s *mcp.Server) int {
	n := 0
	for range s.Sessions() {
		n++
	}
	return n
}

// mcpServerWithLiveSession returns an MCP server with one connected client session
// and a cleanup that closes both ends.
func mcpServerWithLiveSession(t *testing.T) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0"}, nil)
	ctx := context.Background()
	c1, c2 := mcp.NewInMemoryTransports()
	serverSess, err := srv.Connect(ctx, c1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSess.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSess.Close() })
	return srv
}

func TestCloseMCPSessions(t *testing.T) {
	closeMCPSessions(context.Background(), nil) // nil server is a no-op

	srv := mcpServerWithLiveSession(t)
	if got := sessionCount(srv); got != 1 {
		t.Fatalf("expected 1 live session before close, got %d", got)
	}

	closeMCPSessions(context.Background(), srv)

	deadline := time.After(2 * time.Second)
	for sessionCount(srv) != 0 {
		select {
		case <-deadline:
			t.Fatalf("session not closed: %d remain", sessionCount(srv))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestCloseMCPSessions_BoundedByContext proves the close does not hang past the
// grace deadline when a session has a long-running in-flight tool call (Close is
// graceful and blocks on that call). With a short context it must return promptly.
func TestCloseMCPSessions_BoundedByContext(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1.0"}, nil)
	callStarted := make(chan struct{})
	releaseCall := make(chan struct{})
	var once sync.Once
	mcp.AddTool(srv, &mcp.Tool{Name: "slow"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		once.Do(func() { close(callStarted) })
		<-releaseCall
		return &mcp.CallToolResult{}, nil, nil
	})

	ctx := context.Background()
	c1, c2 := mcp.NewInMemoryTransports()
	serverSess, err := srv.Connect(ctx, c1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSess.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, c2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = clientSess.Close() }()

	// Fire a tool call that hangs, so the session has an in-flight request.
	go func() { _, _ = clientSess.CallTool(ctx, &mcp.CallToolParams{Name: "slow"}) }()
	<-callStarted
	defer close(releaseCall)

	// closeMCPSessions must respect the short deadline rather than block on the call.
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	returned := make(chan struct{})
	go func() { closeMCPSessions(deadlineCtx, srv); close(returned) }()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("closeMCPSessions hung past the context deadline on an in-flight call")
	}
}

func TestLogHTTPDrainResult(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	logHTTPDrainResult(nil)
	logHTTPDrainResult(errors.New("drain boom"))

	out := buf.String()
	if !strings.Contains(out, "HTTP server stopped") {
		t.Error("success outcome should be logged")
	}
	if !strings.Contains(out, "drain boom") {
		t.Error("drain error should be logged")
	}
}

// TestDrainHTTPServer_ClosesSessionsAfterSettle proves the #675 settle branch: an
// in-flight request keeps server.Shutdown from completing within the settle window,
// so live MCP sessions are closed to release the long-lived streams and let
// connected agents reconnect to the new build.
func TestDrainHTTPServer_ClosesSessionsAfterSettle(t *testing.T) {
	orig := sessionDrainSettle
	sessionDrainSettle = 20 * time.Millisecond
	defer func() { sessionDrainSettle = orig }()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	ln := listenLocal(t)
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			once.Do(func() { close(started) })
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() { _ = srv.Serve(ln) }()

	// In-flight request that hangs, so server.Shutdown blocks past the settle window.
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String(), http.NoBody)
		if resp, gErr := http.DefaultClient.Do(req); gErr == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started

	srvMCP := mcpServerWithLiveSession(t)
	if got := sessionCount(srvMCP); got != 1 {
		t.Fatalf("expected 1 live session, got %d", got)
	}

	drained := make(chan struct{})
	go func() { drainHTTPServer(srv, srvMCP, 2*time.Second); close(drained) }()

	time.Sleep(80 * time.Millisecond) // exceed the settle window so sessions are closed
	close(release)                    // let the hung request finish so Shutdown completes

	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("drainHTTPServer did not return")
	}
	if got := sessionCount(srvMCP); got != 0 {
		t.Errorf("settle branch should have closed live MCP sessions, got %d", got)
	}
}

// TestDrainHTTPServer_CompletesWhenIdle covers the no-in-flight path (Shutdown
// returns immediately) and the grace-shorter-than-settle clamp.
func TestDrainHTTPServer_CompletesWhenIdle(t *testing.T) {
	ln := listenLocal(t)
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.NewServeMux()}
	go func() { _ = srv.Serve(ln) }()
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() { drainHTTPServer(srv, nil, 50*time.Millisecond); close(done) }() // grace < default settle
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drainHTTPServer did not return for an idle server")
	}
}

func TestListenAndServe_TLSBadCert(t *testing.T) {
	ctx := t.Context()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	hcfg := httpConfig{
		tlsEnabled:  true,
		tlsCertFile: "/nonexistent/cert.pem",
		tlsKeyFile:  "/nonexistent/key.pem",
	}

	err := listenAndServe(ctx, "127.0.0.1:0", handler, hcfg, nil)
	if err == nil {
		t.Fatal("expected error for bad TLS cert path")
	}
}

func TestListenAndServe_NilHealthChecker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(ctx, "127.0.0.1:0", handler, httpConfig{}, nil)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("listenAndServe returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listenAndServe did not shut down in time")
	}
}

func TestServe_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{
			Name: "test",
		},
		Auth: platform.AuthConfig{AllowAnonymous: true},
	})
	defer func() { _ = p.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, mcpServer, p, "127.0.0.1:0")
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not shut down in time")
	}
}

// TestServe_OAuthEnabled drives Serve with the built-in OAuth server enabled so
// the OAuth route registration and protected-resource-metadata branches run
// during assembly. It shares the graceful-shutdown lifecycle with the anonymous
// variant above.
func TestServe_OAuthEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		OAuth: platform.OAuthConfig{
			Enabled:    true,
			Issuer:     "https://mcp.example.com",
			SigningKey: "dGVzdC1zaWduaW5nLWtleS0xMjM0NTY3ODkwYWJjZGVm", // base64, 33 bytes
		},
	})
	defer func() { _ = p.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, mcpServer, p, "127.0.0.1:0")
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not shut down in time")
	}
}

func TestExtractHTTPConfig_ShutdownConfig(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{
			Name: "test",
			Shutdown: platform.ShutdownConfig{
				GracePeriod:      testGracePeriod20s,
				PreShutdownDelay: testPreDelay3s,
			},
		},
	})
	defer func() { _ = p.Close() }()

	cfg := extractHTTPConfig(p)
	if cfg.shutdownCfg.GracePeriod != testGracePeriod20s {
		t.Errorf("shutdownCfg.GracePeriod = %v, want %v", cfg.shutdownCfg.GracePeriod, testGracePeriod20s)
	}
	if cfg.shutdownCfg.PreShutdownDelay != testPreDelay3s {
		t.Errorf("shutdownCfg.PreShutdownDelay = %v, want %v", cfg.shutdownCfg.PreShutdownDelay, testPreDelay3s)
	}
}

func TestHealthEndpointsRegistered(t *testing.T) {
	mux := http.NewServeMux()
	hc := health.NewChecker()
	mux.Handle("/healthz", hc.LivenessHandler())
	mux.Handle("/readyz", hc.ReadinessHandler())

	// Set ready (simulating what listenAndServe does)
	hc.SetReady()

	// Test /healthz
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/healthz", http.NoBody))
	if w.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want %d", w.Code, http.StatusOK)
	}

	// Test /readyz when ready
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/readyz", http.NoBody))
	if w.Code != http.StatusOK {
		t.Errorf("/readyz status = %d, want %d (ready)", w.Code, http.StatusOK)
	}

	// Test /readyz when draining
	hc.SetDraining()
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/readyz", http.NoBody))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz status = %d, want %d (draining)", w.Code, http.StatusServiceUnavailable)
	}
}

func TestRobotsTxt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
	})

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/robots.txt", http.NoBody))

	if w.Code != http.StatusOK {
		t.Fatalf("/robots.txt status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain")
	}
	body := w.Body.String()
	if !strings.Contains(body, "Disallow: /") {
		t.Errorf("body missing Disallow directive: %q", body)
	}
}

func TestAwareHandlerWiring(t *testing.T) {
	t.Run("not wired when stateless is false", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{
				Name: "test",
				Streamable: platform.StreamableConfig{
					Stateless: false,
				},
			},
		})
		defer func() { _ = p.Close() }()

		hcfg := extractHTTPConfig(p)

		// SessionStore is non-nil (memory), but Stateless is false.
		if p.SessionStore() == nil {
			t.Fatal("expected non-nil session store")
		}
		if hcfg.streamableCfg.Stateless {
			t.Error("expected Stateless false for memory mode")
		}
	})

	t.Run("wired when stateless is true with session store", func(t *testing.T) {
		store := session.NewMemoryStore(10 * time.Minute)
		defer func() { _ = store.Close() }()

		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{
				Name: "test",
				Streamable: platform.StreamableConfig{
					Stateless:      true,
					SessionTimeout: 10 * time.Minute,
				},
			},
			Sessions: platform.SessionsConfig{
				TTL: 10 * time.Minute,
			},
		})
		defer func() { _ = p.Close() }()

		hcfg := extractHTTPConfig(p)

		// Stateless + session store → handler should wrap.
		if !hcfg.streamableCfg.Stateless {
			t.Error("expected Stateless true")
		}

		// Verify the conditional logic would create an AwareHandler.
		if p.SessionStore() == nil {
			t.Fatal("expected non-nil session store")
		}

		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler := session.NewAwareHandler(inner, session.HandlerConfig{
			Store: p.SessionStore(),
			TTL:   p.Config().Sessions.TTL,
		})

		// First request (no session) should get a session ID in response.
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		sessionID := w.Header().Get("Mcp-Session-Id")
		if sessionID == "" {
			t.Error("expected Mcp-Session-Id header in response")
		}

		// Second request with session ID should succeed.
		req2 := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
		req2.Header.Set("Mcp-Session-Id", sessionID)
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, req2)

		if w2.Code != http.StatusOK {
			t.Errorf("expected 200 for existing session, got %d", w2.Code)
		}
	})
}

// newTestPlatform creates a minimal platform for testing.
func newTestPlatform(t *testing.T, cfg *platform.Config) *platform.Platform {
	t.Helper()
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("failed to create test platform: %v", err)
	}
	return p
}

func TestMountAdminAPI(t *testing.T) {
	t.Run("skips when platform is nil", func(_ *testing.T) {
		mux := http.NewServeMux()
		mountAdminAPI(mux, nil, nil) // should not panic
	})

	t.Run("skips when admin not enabled", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
			Admin:  platform.AdminConfig{Enabled: new(false)},
		})
		defer func() { _ = p.Close() }()

		mux := http.NewServeMux()
		mountAdminAPI(mux, p, nil) // should not register any routes
	})

	t.Run("mounts when admin enabled", func(t *testing.T) {
		cfg := &platform.Config{
			Server:   platform.ServerConfig{Name: "test", Transport: "http"},
			Semantic: platform.SemanticConfig{Provider: "noop"},
			Query:    platform.QueryConfig{Provider: "noop"},
			Storage:  platform.StorageConfig{Provider: "noop"},
			Admin:    platform.AdminConfig{Enabled: new(true), Persona: "admin"},
			Personas: platform.PersonasConfig{
				Definitions: map[string]platform.PersonaDef{
					"admin": {
						DisplayName: "Administrator",
						Roles:       []string{"admin"},
						Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
					},
				},
			},
		}

		p, err := platform.New(platform.WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer func() { _ = p.Close() }()

		mux := http.NewServeMux()
		mountAdminAPI(mux, p, nil)

		// Admin route should be registered and return 401 (no auth)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// TestMountObservabilityProxy covers mountObservabilityProxy: the nil-platform
// skip and the requireAuth wiring (browser-session + OptionalAuth wrapping).
// An unauthenticated request must be rejected by the proxy authorizer with a
// 401 rather than panicking or, as in the v1.69.0 bug, never being reachable
// by cookie-authenticated portal users.
func TestMountObservabilityProxy(t *testing.T) {
	t.Run("skips when platform is nil", func(_ *testing.T) {
		mux := http.NewServeMux()
		mountObservabilityProxy(mux, nil, true) // must not panic
	})

	t.Run("mounts and requires auth", func(t *testing.T) {
		cfg := &platform.Config{
			Server:   platform.ServerConfig{Name: "test", Transport: "http"},
			Semantic: platform.SemanticConfig{Provider: "noop"},
			Query:    platform.QueryConfig{Provider: "noop"},
			Storage:  platform.StorageConfig{Provider: "noop"},
		}
		p, err := platform.New(platform.WithConfig(cfg))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer func() { _ = p.Close() }()

		mux := http.NewServeMux()
		mountObservabilityProxy(mux, p, true)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/observability/query?query=up", http.NoBody)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		// A request with no credentials must be denied by the proxy
		// authorizer (401 unauthenticated, or 403 when an anonymous user is
		// resolved but lacks observability:read) rather than reaching
		// Prometheus or returning 200.
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Errorf("expected 401 or 403 for unauthenticated request, got %d", w.Code)
		}
	})
}

// TestAdminPortalGate was removed — admin UI is now part of the unified portal
// SPA served at /portal/. Admin sections are role-gated in the SPA itself and
// the admin API routes at /api/v1/admin/ enforce server-side authorization.

// TestMountGatewayAPI covers the four branches in mountGatewayAPI:
// (1) nil platform → skip, (2) no apigateway toolkit → skip,
// (3) toolkit present + auth required → 401 on unauthenticated POST,
// (4) toolkit present + auth disabled → request reaches the handler.
// Each branch is exercised through the real ServeMux so a regression
// in the registration path is caught here, not at runtime.
func TestMountGatewayAPI(t *testing.T) {
	t.Run("skips when platform is nil", func(_ *testing.T) {
		mux := http.NewServeMux()
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v1"}, nil)
		mountGatewayAPI(mux, server, nil, false)
	})

	t.Run("skips when no apigateway toolkit is loaded", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server:   platform.ServerConfig{Name: "test"},
			Semantic: platform.SemanticConfig{Provider: "noop"},
			Query:    platform.QueryConfig{Provider: "noop"},
			Storage:  platform.StorageConfig{Provider: "noop"},
		})
		defer func() { _ = p.Close() }()

		mux := http.NewServeMux()
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v1"}, nil)
		mountGatewayAPI(mux, server, p, false)

		// No /api/v1/gateway/ route should have been registered.
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/gateway/acme/invoke", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 (no route), got %d", w.Code)
		}
	})

	t.Run("mounts with auth wrapper when requireAuth is true", func(t *testing.T) {
		p := newGatewayTestPlatform(t)
		defer func() { _ = p.Close() }()

		mux := http.NewServeMux()
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v1"}, nil)
		mountGatewayAPI(mux, server, p, true)

		// Unauthenticated request must be rejected before reaching the
		// handler. The auth wrapper returns 401 with a missing-token
		// message — that's what proves requireAuth=true wired through.
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/gateway/acme/invoke", strings.NewReader(`{"method":"GET","path":"/x"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 from auth wrapper, got %d", w.Code)
		}
	})

	t.Run("mounts without auth wrapper when requireAuth is false", func(t *testing.T) {
		p := newGatewayTestPlatform(t)
		defer func() { _ = p.Close() }()

		mux := http.NewServeMux()
		server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v1"}, nil)
		mountGatewayAPI(mux, server, p, false)

		// Anonymous request reaches the handler; without the
		// api_invoke_endpoint tool registered on this bare MCP server,
		// the in-memory CallTool returns an error → 500.
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"/api/v1/gateway/acme/invoke", strings.NewReader(`{"method":"GET","path":"/x"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code == http.StatusUnauthorized {
			t.Errorf("expected handler to be reached without auth wrapper, but got 401")
		}
	})
}

// newGatewayTestPlatform builds a minimal platform with an apigateway
// toolkit loaded. The toolkit needs at least one configured kind in
// `toolkits.api` for the registry GetByKind(api) lookup to return a
// non-empty slice, which is what mountGatewayAPI checks before
// registering the REST route.
func newGatewayTestPlatform(t *testing.T) *platform.Platform {
	t.Helper()
	return newTestPlatform(t, &platform.Config{
		Server:   platform.ServerConfig{Name: "test"},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
		Toolkits: map[string]any{
			"api": map[string]any{
				"enabled": true,
				"instances": map[string]any{
					"acme": map[string]any{
						"base_url":  "https://api.example.com",
						"auth_mode": "none",
					},
				},
			},
		},
	})
}

func TestBuildAdminHandler(t *testing.T) {
	cfg := &platform.Config{
		Server: platform.ServerConfig{
			Name:      "test-platform",
			Transport: "http",
		},
		Semantic: platform.SemanticConfig{Provider: "noop"},
		Query:    platform.QueryConfig{Provider: "noop"},
		Storage:  platform.StorageConfig{Provider: "noop"},
		Admin: platform.AdminConfig{
			Enabled: new(true),
			Persona: "admin",
		},
		Auth: platform.AuthConfig{
			APIKeys: platform.APIKeyAuthConfig{
				Enabled: true,
				Keys: []platform.APIKeyDef{
					{Key: "test-admin-key", Name: "admin", Roles: []string{"admin"}},
				},
			},
		},
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"admin": {
					DisplayName: "Administrator",
					Roles:       []string{"admin"},
					Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
				},
			},
		},
	}

	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = p.Close() }()

	handler := buildAdminHandler(p, nil)
	if handler == nil {
		t.Fatal("buildAdminHandler() returned nil")
	}

	// The handler should respond to admin routes
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/admin/system/info", http.NoBody)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Without a valid API key, we expect 401
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated request, got %d", w.Code)
	}
}

func TestBuildRootHandler_NilPlatform(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)
	handler := buildRootHandler(mcpServer, nil, httpConfig{})
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestBuildRootHandler_WithSessionStore(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{
			Name: "test",
			Streamable: platform.StreamableConfig{
				Stateless:      true,
				SessionTimeout: testSessionTimeout,
			},
		},
		Sessions: platform.SessionsConfig{
			TTL: testSessionTimeout,
		},
	})
	defer func() { _ = p.Close() }()

	hcfg := extractHTTPConfig(p)
	handler := buildRootHandler(mcpServer, p, hcfg)
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	// The handler should be wrapped with session awareness
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Session-aware handler adds Mcp-Session-Id header
	if sessionID := w.Header().Get("Mcp-Session-Id"); sessionID == "" {
		t.Error("expected Mcp-Session-Id header from session-aware wrapper")
	}
}

func TestMountRootHandler_AuthRequired(t *testing.T) {
	mux := http.NewServeMux()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mountRootHandler(mux, inner, httpConfig{requireAuth: true}, "")

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Auth gateway should reject unauthenticated requests
	if w.Code == http.StatusOK {
		t.Error("expected auth gateway to reject request")
	}
}

func TestMountRootHandler_NoAuth(t *testing.T) {
	mux := http.NewServeMux()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mountRootHandler(mux, inner, httpConfig{requireAuth: false}, "")

	req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestBrowserRedirectMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := browserRedirectMiddleware(inner)

	t.Run("redirects browsers", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusTemporaryRedirect {
			t.Errorf("expected 307, got %d", w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "/portal/" {
			t.Errorf("Location = %q, want /portal/", loc)
		}
	})

	t.Run("passes through non-browser", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("passes through GET without html accept", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestMountRootHandler_AuthWithPortalUI_BrowserRedirects(t *testing.T) {
	mux := http.NewServeMux()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mountRootHandler(mux, inner, httpConfig{requireAuth: true, portalUI: true}, "")

	// Browser request should redirect to /portal/ instead of getting 401.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Errorf("browser request: expected 307 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/portal/" {
		t.Errorf("Location = %q, want /portal/", loc)
	}
}

func TestMountRootHandler_AuthWithPortalUI_MCPStillRequiresAuth(t *testing.T) {
	mux := http.NewServeMux()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mountRootHandler(mux, inner, httpConfig{requireAuth: true, portalUI: true}, "")

	// MCP request (POST, no Accept: text/html) should still hit auth gateway.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("MCP request should still be rejected by auth gateway")
	}
}

func TestMountBrowserAuth_NilPlatform(_ *testing.T) {
	mux := http.NewServeMux()
	mountBrowserAuth(mux, nil) // should not panic
}

// mountedPattern reports the ServeMux pattern matching path, or "" when the mux
// has no handler for it. Matching a pattern does not invoke the handler, so this
// asserts what a mount registered without issuing a real request.
func mountedPattern(mux *http.ServeMux, path string) string {
	_, pattern := mux.Handler(httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	return pattern
}

// TestMountBrowserAuth_NilFlow asserts that with no browser-session flow the
// sign-in routes are left unmounted. A mounted route backed by a nil flow would
// answer the login callback by panicking rather than 404ing.
func TestMountBrowserAuth_NilFlow(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
	})
	defer func() { _ = p.Close() }()

	mux := http.NewServeMux()
	mountBrowserAuth(mux, p)

	for _, path := range []string{"/portal/auth/login", "/portal/auth/callback", "/portal/auth/logout"} {
		if got := mountedPattern(mux, path); got != "" {
			t.Errorf("mounted %q on %s without a browser-session flow", got, path)
		}
	}
}

// TestMountPortalUI_Disabled asserts the config gate: with the portal disabled
// the SPA route must not exist, so the server 404s rather than serving a shell
// whose every API call is refused.
func TestMountPortalUI_Disabled(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Portal: platform.PortalConfig{Enabled: new(false)},
	})
	defer func() { _ = p.Close() }()

	mux := http.NewServeMux()
	mountPortalUI(mux, p, true)

	if got := mountedPattern(mux, "/portal/"); got != "" {
		t.Errorf("mounted %q with the portal disabled in config", got)
	}
}

// TestMountPortalUI_NoAssets asserts the asset gate, which is independent of the
// config gate above: enabled in config but with no embedded build, the SPA route
// must still be left unmounted.
func TestMountPortalUI_NoAssets(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Portal: platform.PortalConfig{Enabled: new(true)},
	})
	defer func() { _ = p.Close() }()

	mux := http.NewServeMux()
	mountPortalUI(mux, p, false)

	if got := mountedPattern(mux, "/portal/"); got != "" {
		t.Errorf("mounted %q with no portal assets available", got)
	}
}

func TestMountPortalAPI_Disabled(t *testing.T) {
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Portal: platform.PortalConfig{Enabled: new(false)},
	})
	defer func() { _ = p.Close() }()

	mux := http.NewServeMux()
	if err := mountPortalAPI(mux, p, nil, true); err != nil {
		t.Fatalf("mountPortalAPI() disabled = %v, want nil", err)
	}
}

func TestMountPortalAPI_NoStores(t *testing.T) {
	// Portal enabled but no database means no asset/share stores, so the API is
	// not mounted and no error is returned.
	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{Name: "test"},
		Portal: platform.PortalConfig{Enabled: new(true)},
	})
	defer func() { _ = p.Close() }()

	mux := http.NewServeMux()
	if err := mountPortalAPI(mux, p, nil, true); err != nil {
		t.Fatalf("mountPortalAPI() no stores = %v, want nil", err)
	}
}

func TestPortalRateLimitResolver(t *testing.T) {
	t.Run("valid trusted proxies", func(t *testing.T) {
		r, err := portalRateLimitResolver(platform.PortalRateLimitConfig{
			TrustedProxies: []string{"10.0.0.0/8"},
		})
		if err != nil {
			t.Fatalf("portalRateLimitResolver() = %v, want nil", err)
		}
		if r == nil {
			t.Fatal("portalRateLimitResolver() returned nil resolver")
		}
	})

	t.Run("empty trusts none", func(t *testing.T) {
		r, err := portalRateLimitResolver(platform.PortalRateLimitConfig{})
		if err != nil {
			t.Fatalf("portalRateLimitResolver() = %v, want nil", err)
		}
		if r == nil {
			t.Fatal("portalRateLimitResolver() returned nil resolver")
		}
	})

	t.Run("malformed CIDR errors", func(t *testing.T) {
		_, err := portalRateLimitResolver(platform.PortalRateLimitConfig{
			TrustedProxies: []string{"not-a-cidr"},
		})
		if err == nil {
			t.Fatal("portalRateLimitResolver() malformed CIDR = nil, want error")
		}
		if !strings.Contains(err.Error(), "portal rate limiter") {
			t.Errorf("error %q missing 'portal rate limiter' context", err.Error())
		}
	})
}

func TestWarnOnUntrustedPortalRateLimit(t *testing.T) {
	// The warning fires only when no trusted proxies are configured. Capture the
	// standard logger's output to assert on the message rather than just that
	// the call does not panic.
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	warnOnUntrustedPortalRateLimit([]string{"10.0.0.0/8"})
	if buf.Len() != 0 {
		t.Errorf("warn with trusted proxies emitted output: %q", buf.String())
	}

	warnOnUntrustedPortalRateLimit(nil)
	if !strings.Contains(buf.String(), "trusted_proxies is empty") {
		t.Errorf("warn with empty trusted proxies missing expected message, got: %q", buf.String())
	}
}

func TestMcpappsBrandName(t *testing.T) {
	t.Run("returns brand_name from mcpapps config", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
			MCPApps: platform.MCPAppsConfig{
				Apps: map[string]platform.AppConfig{
					"platform-info": {
						Config: map[string]any{"brand_name": "Plexara"},
					},
				},
			},
		})
		defer func() { _ = p.Close() }()

		got := mcpappsBrandName(p)
		if got != "Plexara" {
			t.Errorf("mcpappsBrandName() = %q, want %q", got, "Plexara")
		}
	})

	t.Run("returns empty when no platform-info app", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
		})
		defer func() { _ = p.Close() }()

		got := mcpappsBrandName(p)
		if got != "" {
			t.Errorf("mcpappsBrandName() = %q, want empty", got)
		}
	})

	t.Run("returns empty when brand_name not in config", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "test"},
			MCPApps: platform.MCPAppsConfig{
				Apps: map[string]platform.AppConfig{
					"platform-info": {
						Config: map[string]any{"other_key": "value"},
					},
				},
			},
		})
		defer func() { _ = p.Close() }()

		got := mcpappsBrandName(p)
		if got != "" {
			t.Errorf("mcpappsBrandName() = %q, want empty", got)
		}
	})
}

func TestPortalBrandName(t *testing.T) {
	t.Run("prefers portal brand_name", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "server-name"},
			Portal: platform.PortalConfig{Title: "portal-title", BrandName: "Contoso"},
			MCPApps: platform.MCPAppsConfig{
				Apps: map[string]platform.AppConfig{
					"platform-info": {Config: map[string]any{"brand_name": "ACME"}},
				},
			},
		})
		defer func() { _ = p.Close() }()

		if got := portalBrandName(p); got != "Contoso" {
			t.Errorf("portalBrandName() = %q, want %q", got, "Contoso")
		}
	})

	t.Run("prefers mcpapps brand_name", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "server-name"},
			Portal: platform.PortalConfig{Title: "portal-title"},
			MCPApps: platform.MCPAppsConfig{
				Apps: map[string]platform.AppConfig{
					"platform-info": {Config: map[string]any{"brand_name": "Plexara"}},
				},
			},
		})
		defer func() { _ = p.Close() }()

		if got := portalBrandName(p); got != "Plexara" {
			t.Errorf("portalBrandName() = %q, want %q", got, "Plexara")
		}
	})

	t.Run("falls back to portal title", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "server-name"},
			Portal: platform.PortalConfig{Title: "portal-title"},
		})
		defer func() { _ = p.Close() }()

		if got := portalBrandName(p); got != "portal-title" {
			t.Errorf("portalBrandName() = %q, want %q", got, "portal-title")
		}
	})

	t.Run("falls back to server name", func(t *testing.T) {
		p := newTestPlatform(t, &platform.Config{
			Server: platform.ServerConfig{Name: "server-name"},
		})
		defer func() { _ = p.Close() }()

		if got := portalBrandName(p); got != "server-name" {
			t.Errorf("portalBrandName() = %q, want %q", got, "server-name")
		}
	})
}

func TestBuildResourceClaims(t *testing.T) {
	reg := persona.NewRegistry()
	_ = reg.Register(&persona.Persona{
		Name:  "admin",
		Roles: []string{"dp_admin"},
	})
	_ = reg.Register(&persona.Persona{
		Name:  "analyst",
		Roles: []string{"dp_analyst"},
	})

	t.Run("prefixed admin role resolves IsAdmin via persona", func(t *testing.T) {
		user := &portal.User{
			UserID: "u1",
			Email:  "admin@example.com",
			Roles:  []string{"dp_admin"},
		}
		claims, err := buildResourceClaims(user, reg, "admin")
		if err != nil {
			t.Fatalf("buildResourceClaims() = %v", err)
		}
		if !claims.IsAdmin {
			t.Error("expected IsAdmin=true for user with dp_admin role mapped to admin persona")
		}
		if !slices.Contains(claims.Personas, "admin") {
			t.Errorf("expected admin in Personas, got %v", claims.Personas)
		}
	})

	t.Run("non-admin role does not set IsAdmin", func(t *testing.T) {
		user := &portal.User{
			UserID: "u2",
			Email:  "analyst@example.com",
			Roles:  []string{"dp_analyst"},
		}
		claims, err := buildResourceClaims(user, reg, "admin")
		if err != nil {
			t.Fatalf("buildResourceClaims() = %v", err)
		}
		if claims.IsAdmin {
			t.Error("expected IsAdmin=false for non-admin user")
		}
		if !slices.Contains(claims.Personas, "analyst") {
			t.Errorf("expected analyst in Personas, got %v", claims.Personas)
		}
	})

	// With no registry there are no personas to belong to, which is the same
	// refusal an unmapped caller gets: resources are not readable by an identity
	// no persona claims.
	t.Run("nil registry is refused", func(t *testing.T) {
		user := &portal.User{
			UserID: "u3",
			Email:  "u3@example.com",
			Roles:  []string{"dp_admin"},
		}
		claims, err := buildResourceClaims(user, nil, "admin")
		if !errors.Is(err, resource.ErrForbidden) {
			t.Errorf("err = %v, want resource.ErrForbidden", err)
		}
		if claims != nil {
			t.Errorf("claims = %+v, want nil on refusal", claims)
		}
	})

	// The hole the persona gate closes, on the managed-resources surface: an
	// authenticated account carrying a role no persona names.
	t.Run("roles matching no persona are refused", func(t *testing.T) {
		user := &portal.User{
			UserID: "u5",
			Email:  "nobody@example.com",
			Roles:  []string{"dp_unmapped"},
		}
		claims, err := buildResourceClaims(user, reg, "admin")
		if !errors.Is(err, resource.ErrForbidden) {
			t.Errorf("err = %v, want resource.ErrForbidden", err)
		}
		if claims != nil {
			t.Errorf("claims = %+v, want nil on refusal", claims)
		}
	})

	t.Run("no roles at all is refused", func(t *testing.T) {
		user := &portal.User{UserID: "u6", Email: "empty@example.com"}
		_, err := buildResourceClaims(user, reg, "admin")
		if !errors.Is(err, resource.ErrForbidden) {
			t.Errorf("err = %v, want resource.ErrForbidden", err)
		}
	})

	t.Run("user with multiple roles gets all matching personas", func(t *testing.T) {
		user := &portal.User{
			UserID: "u4",
			Email:  "multi@example.com",
			Roles:  []string{"dp_admin", "dp_analyst"},
		}
		claims, err := buildResourceClaims(user, reg, "admin")
		if err != nil {
			t.Fatalf("buildResourceClaims() = %v", err)
		}
		if !claims.IsAdmin {
			t.Error("expected IsAdmin=true")
		}
		if len(claims.Personas) != 2 {
			t.Errorf("expected 2 personas, got %v", claims.Personas)
		}
	})

	t.Run("persona-admin roles populate AdminOfPersonas", func(t *testing.T) {
		user := &portal.User{
			UserID: "u5",
			Email:  "pa@example.com",
			Roles:  []string{"dp_persona-admin:finance", "dp_analyst"},
		}
		claims, err := buildResourceClaims(user, reg, "admin")
		if err != nil {
			t.Fatalf("buildResourceClaims() = %v", err)
		}
		if !slices.Contains(claims.AdminOfPersonas, "finance") {
			t.Errorf("expected finance in AdminOfPersonas, got %v", claims.AdminOfPersonas)
		}
	})
}

func TestExtractPersonaAdminRoles(t *testing.T) {
	t.Run("unprefixed role", func(t *testing.T) {
		got := extractPersonaAdminRoles([]string{"persona-admin:finance"})
		if len(got) != 1 || got[0] != "finance" {
			t.Errorf("got %v, want [finance]", got)
		}
	})

	t.Run("prefixed role", func(t *testing.T) {
		got := extractPersonaAdminRoles([]string{"dp_persona-admin:engineering"})
		if len(got) != 1 || got[0] != "engineering" {
			t.Errorf("got %v, want [engineering]", got)
		}
	})

	t.Run("multiple persona-admin roles", func(t *testing.T) {
		got := extractPersonaAdminRoles([]string{"dp_persona-admin:finance", "dp_persona-admin:ops", "dp_analyst"})
		if len(got) != 2 {
			t.Errorf("got %v, want 2 entries", got)
		}
	})

	t.Run("no persona-admin roles", func(t *testing.T) {
		got := extractPersonaAdminRoles([]string{"dp_admin", "dp_analyst"})
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("empty persona name ignored", func(t *testing.T) {
		got := extractPersonaAdminRoles([]string{"persona-admin:"})
		if len(got) != 0 {
			t.Errorf("got %v, want empty for trailing colon", got)
		}
	})
}
