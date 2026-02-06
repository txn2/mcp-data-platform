// Package http provides HTTP middleware for the MCP data platform.
package http

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// AuthMiddleware extracts authentication tokens from HTTP headers and adds them to the request context.
// This middleware should be applied to SSE handlers to enable HTTP-level authentication.
func AuthMiddleware(requireAuth bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var token string

			// Extract Bearer token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}

			// If no Bearer token, try X-API-Key header
			if token == "" {
				token = r.Header.Get("X-API-Key")
			}

			// If auth is required and no token found, return 401
			if requireAuth && token == "" {
				http.Error(w, "Unauthorized: missing authentication token", http.StatusUnauthorized)
				return
			}

			// Add token to context for downstream authenticators
			if token != "" {
				ctx = auth.WithToken(ctx, token)
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// MCPAuthGateway creates HTTP middleware that gates access for MCP endpoints.
//
// When no credentials (Bearer token or API key) are present, it returns
// HTTP 401 with a WWW-Authenticate header that triggers the OAuth discovery
// flow in MCP clients (Claude.ai, Claude Desktop).
//
// Per the MCP authorization spec and RFC 9728, the header includes:
//
//	WWW-Authenticate: Bearer resource_metadata="<url>"
//
// The resourceMetadataURL should point to the server's
// /.well-known/oauth-protected-resource endpoint.
//
// This middleware does NOT validate tokens — it only checks for their presence.
// Actual token validation happens in the MCP protocol middleware chain.
func MCPAuthGateway(resourceMetadataURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hasBearer := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
			hasAPIKey := r.Header.Get("X-API-Key") != ""

			if !hasBearer && !hasAPIKey {
				if resourceMetadataURL != "" {
					w.Header().Set("WWW-Authenticate",
						`Bearer resource_metadata="`+resourceMetadataURL+`"`)
				} else {
					w.Header().Set("WWW-Authenticate", "Bearer")
				}
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth returns middleware that requires authentication.
func RequireAuth() func(http.Handler) http.Handler {
	return AuthMiddleware(true)
}

// RequireAuthWithOAuth returns middleware that requires authentication and
// includes the WWW-Authenticate header with resource metadata URL in 401
// responses, enabling OAuth discovery for MCP clients.
func RequireAuthWithOAuth(resourceMetadataURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var token string

			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}

			if token == "" {
				token = r.Header.Get("X-API-Key")
			}

			if token == "" {
				if resourceMetadataURL != "" {
					w.Header().Set("WWW-Authenticate",
						`Bearer resource_metadata="`+resourceMetadataURL+`"`)
				} else {
					w.Header().Set("WWW-Authenticate", "Bearer")
				}
				http.Error(w, "Unauthorized: missing authentication token", http.StatusUnauthorized)
				return
			}

			ctx = auth.WithToken(ctx, token)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuth returns middleware that allows anonymous requests.
func OptionalAuth() func(http.Handler) http.Handler {
	return AuthMiddleware(false)
}
