package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/platform"
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
			req := httptest.NewRequest(http.MethodGet, route, nil)
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
		req := httptest.NewRequest("GET", "/", nil)
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
		req := httptest.NewRequest("OPTIONS", "/mcp", nil)
		req.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("OPTIONS status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("defaults origin to wildcard", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Allow-Origin = %q, want %q", got, "*")
		}
	})
}

func TestStartServer_UnknownTransport(t *testing.T) {
	err := startServer(context.TODO(), nil, nil, serverOptions{transport: "websocket"})
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
	if !strings.Contains(err.Error(), "unknown transport") {
		t.Errorf("error = %q, want 'unknown transport'", err.Error())
	}
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
				SessionTimeout: 10 * time.Minute,
				Stateless:      true,
			},
		},
		Auth: platform.AuthConfig{
			AllowAnonymous: false,
		},
	})
	defer p.Close()

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
	if cfg.streamableCfg.SessionTimeout != 10*time.Minute {
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
	defer p.Close()

	cfg := extractHTTPConfig(p)
	if cfg.requireAuth {
		t.Error("expected requireAuth false when AllowAnonymous is true")
	}
}

func TestNewSSEHandler(t *testing.T) {
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	t.Run("without auth", func(t *testing.T) {
		handler := newSSEHandler(mcpServer, false, "")
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})

	t.Run("with auth no OAuth", func(t *testing.T) {
		handler := newSSEHandler(mcpServer, true, "")
		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})

	t.Run("with auth and OAuth", func(t *testing.T) {
		handler := newSSEHandler(mcpServer, true, "https://mcp.example.com/.well-known/oauth-protected-resource")
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
		defer p.Close()

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
		defer p.Close()

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

	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe(ctx, "127.0.0.1:0", handler, httpConfig{})
	}()

	// Give the server a moment to start, then cancel.
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

	err := listenAndServe(ctx, "127.0.0.1:0", handler, hcfg)
	if err == nil {
		t.Fatal("expected error for bad TLS cert path")
	}
}

func TestStartHTTPServer_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	p := newTestPlatform(t, &platform.Config{
		Server: platform.ServerConfig{
			Name: "test",
		},
		Auth: platform.AuthConfig{AllowAnonymous: true},
	})
	defer p.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- startHTTPServer(ctx, mcpServer, p, serverOptions{address: "127.0.0.1:0"})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("startHTTPServer returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startHTTPServer did not shut down in time")
	}
}

func TestStartServer_HTTPBackwardCompat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- startServer(ctx, mcpServer, nil, serverOptions{
			transport: "sse",
			address:   "127.0.0.1:0",
		})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("startServer with 'sse' transport returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startServer did not shut down in time")
	}
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
