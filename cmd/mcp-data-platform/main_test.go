package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	err := startServer(nil, nil, nil, serverOptions{transport: "websocket"})
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
	if !strings.Contains(err.Error(), "unknown transport") {
		t.Errorf("error = %q, want 'unknown transport'", err.Error())
	}
}
