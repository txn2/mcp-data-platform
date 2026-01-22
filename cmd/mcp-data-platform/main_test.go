package main

import (
	"net/http"
	"net/http/httptest"
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
