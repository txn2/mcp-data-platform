//nolint:revive // package name matches directory structure
package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// extractHTTPToken pulls a credential from the request: a Bearer token from the
// Authorization header, falling back to the X-API-Key header. Returns "" when
// neither is present.
func extractHTTPToken(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && after != "" {
		return after
	}
	return r.Header.Get("X-API-Key")
}

// quoteEscape escapes a value for embedding in an RFC 7235 quoted-string:
// backslash and double-quote become quoted-pairs. resourceMetadataURL is
// operator config, not attacker-controlled, but escaping keeps a stray quote in
// an issuer URL from truncating the WWW-Authenticate parameter a client parses.
func quoteEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// bearerChallenge builds the WWW-Authenticate header value that drives the MCP
// OAuth discovery/re-auth flow.
//
// Per the MCP authorization spec and RFC 9728, resourceMetadataURL points MCP
// clients at the /.well-known/oauth-protected-resource endpoint. Per RFC 6750
// section 3, errCode (when non-empty, e.g. "invalid_token") is emitted so a
// client can distinguish an absent credential from a present-but-rejected one.
// Pass errCode "" for the absent-credential case.
func bearerChallenge(resourceMetadataURL, errCode string) string {
	var params []string
	if resourceMetadataURL != "" {
		params = append(params, `resource_metadata="`+quoteEscape(resourceMetadataURL)+`"`)
	}
	if errCode != "" {
		params = append(params, `error="`+errCode+`"`)
	}
	if len(params) == 0 {
		return "Bearer"
	}
	return "Bearer " + strings.Join(params, ", ")
}

// AuthMiddleware extracts authentication tokens from HTTP headers and adds them to the request context.
// This middleware should be applied to SSE handlers to enable HTTP-level authentication.
func AuthMiddleware(requireAuth bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			token := extractHTTPToken(r)

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
// When authenticator is non-nil, a present credential is validated at the HTTP
// layer: an expired/revoked/unknown token is rejected with the same HTTP 401 +
// WWW-Authenticate, adding RFC 6750's error="invalid_token" (issue #926). This
// is the conformance surface MCP clients key their re-auth flow off — a client
// holding an expired token must see a 401, not an in-band protocol error on a
// 200 response. The gate calls the same authenticator the protocol middleware
// uses, so a valid API key still passes (it is authenticated here) while an
// invalid credential is rejected. MCPToolCallMiddleware re-validates as defense
// in depth and is what builds the PlatformContext identity; this HTTP check does
// not replace it.
//
// When authenticator is nil the gate is presence-only (the platform is nil in
// transport-level tests).
func MCPAuthGateway(authenticator middleware.Authenticator, resourceMetadataURL string) func(http.Handler) http.Handler {
	return oauthGate(authenticator, resourceMetadataURL, "Unauthorized", "Unauthorized")
}

// RequireAuth returns middleware that requires authentication.
func RequireAuth() func(http.Handler) http.Handler {
	return AuthMiddleware(true)
}

// RequireAuthWithOAuth returns middleware that requires authentication and
// includes the WWW-Authenticate header with resource metadata URL in 401
// responses, enabling OAuth discovery for MCP clients. It is the SSE
// counterpart to MCPAuthGateway and shares its validation semantics: when
// authenticator is non-nil, a present-but-invalid token receives HTTP 401 +
// WWW-Authenticate with error="invalid_token" (issue #926) rather than passing
// the HTTP layer to fail in-band. When authenticator is nil the gate is
// presence-only.
func RequireAuthWithOAuth(authenticator middleware.Authenticator, resourceMetadataURL string) func(http.Handler) http.Handler {
	return oauthGate(authenticator, resourceMetadataURL,
		"Unauthorized: missing authentication token",
		"Unauthorized: invalid authentication token")
}

// oauthGate is the shared implementation behind MCPAuthGateway (streamable HTTP)
// and RequireAuthWithOAuth (SSE): the two MCP transports gate identically, so
// the logic lives in one place. It extracts a credential, and on failure returns
// HTTP 401 with the OAuth-discovery WWW-Authenticate header — with RFC 6750's
// error="invalid_token" when a token was present but rejected, and without it
// when none was supplied. absentMsg and invalidMsg are the respective 401 body
// strings, which differ only in wording between the two transports.
//
// authenticator, when non-nil, validates a present credential so an expired or
// otherwise invalid token is rejected at the HTTP layer (issue #926) — the
// signal MCP clients key their OAuth re-auth flow off. It is the same entry
// point the protocol middleware uses (a valid token — JWT or API key — passes;
// an invalid one is rejected). The protocol layer re-validates as defense in
// depth, so validation runs at both layers by design.
func oauthGate(authenticator middleware.Authenticator, resourceMetadataURL, absentMsg, invalidMsg string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractHTTPToken(r)
			if token == "" {
				w.Header().Set("WWW-Authenticate", bearerChallenge(resourceMetadataURL, ""))
				http.Error(w, absentMsg, http.StatusUnauthorized)
				return
			}

			// Bridge the token to context so the MCP connection inherits it.
			// For Streamable HTTP the SDK creates a long-lived connection from
			// the initialize request's context; placing the token here ensures
			// it is available for all subsequent requests on that connection.
			ctx := auth.WithToken(r.Context(), token)

			if authenticator != nil {
				if _, err := authenticator.Authenticate(ctx); err != nil && !errors.Is(err, middleware.ErrValidationUnavailable) {
					// Fail closed on a definitive rejection (expired/invalid/unknown
					// token). Fail OPEN on ErrValidationUnavailable — a transient
					// dependency failure (e.g. OIDC JWKS unreachable) means validity
					// is undetermined, so pass through to the protocol layer rather
					// than drop a possibly-valid client. Access is not granted: during
					// the same outage the protocol layer cannot validate either, so a
					// tool call is still rejected in-band.
					w.Header().Set("WWW-Authenticate", bearerChallenge(resourceMetadataURL, "invalid_token"))
					http.Error(w, invalidMsg, http.StatusUnauthorized)
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth returns middleware that allows anonymous requests.
func OptionalAuth() func(http.Handler) http.Handler {
	return AuthMiddleware(false)
}
