package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	httpauth "github.com/txn2/mcp-data-platform/pkg/http"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

const (
	wantEchoReply      = "echo: hello"
	fmtConnectFailed   = "Connect failed: %v"
	fmtCallToolFailed  = "CallTool failed: %v"
	fmtWantTextContent = "expected TextContent, got %T"
	fmtGotWant         = "got %q, want %q"
)

// authRoundTripper adds an Authorization header to all outgoing requests.
type authRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}
	return resp, nil
}

// TestStreamableHTTP_ToolCall_Bare tests a basic tool call through the
// Streamable HTTP transport with NO middleware. This is the baseline.
func TestStreamableHTTP_ToolCall_Bare(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct{ Message string }) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
		}, nil, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf(fmtConnectFailed, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"Message": "hello"},
	})
	if err != nil {
		t.Fatalf(fmtCallToolFailed, err)
	}

	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf(fmtWantTextContent, result.Content[0])
	}
	if tc.Text != wantEchoReply {
		t.Errorf(fmtGotWant, tc.Text, wantEchoReply)
	}
}

// TestStreamableHTTP_ToolCall_WithAuthGateway tests tool calls through
// StreamableHTTPHandler wrapped with MCPAuthGateway (the v0.13.2 setup).
func TestStreamableHTTP_ToolCall_WithAuthGateway(t *testing.T) {
	ctx := context.Background()
	apiKey := "test-key-12345"

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct{ Message string }) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
		}, nil, nil
	})

	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	// Wrap with MCPAuthGateway (what v0.13.2 uses)
	handler := httpauth.MCPAuthGateway("")(streamHandler)

	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	// Client with Authorization header
	httpClient := &http.Client{
		Transport: &authRoundTripper{token: apiKey, base: http.DefaultTransport},
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf(fmtConnectFailed, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"Message": "hello"},
	})
	if err != nil {
		t.Fatalf(fmtCallToolFailed, err)
	}

	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf(fmtWantTextContent, result.Content[0])
	}
	if tc.Text != wantEchoReply {
		t.Errorf(fmtGotWant, tc.Text, wantEchoReply)
	}
}

// TestStreamableHTTP_ToolCall_WithFullMiddleware tests tool calls with the
// full production middleware stack: MCPAuthGateway + MCPToolCallMiddleware
// (auth/authz) + MCPAuditMiddleware.
func TestStreamableHTTP_ToolCall_WithFullMiddleware(t *testing.T) {
	ctx := context.Background()
	apiKey := "test-key-12345"

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct{ Message string }) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
		}, nil, nil
	})

	// Set up authenticator (API key)
	authenticator := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{
		Keys: []auth.APIKey{{Key: apiKey, Name: "test", Roles: []string{"admin"}}},
	})

	// Set up authorizer (allow all for admin)
	authorizer := middleware.AllowAllAuthorizer()

	// Add middleware in innermost-first order (last added = outermost = runs first)
	// The production order is: enrichment → rules → audit → auth/authz → apps metadata
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, nil, "http"))

	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	handler := httpauth.MCPAuthGateway("")(streamHandler)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	httpClient := &http.Client{
		Transport: &authRoundTripper{token: apiKey, base: http.DefaultTransport},
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf(fmtConnectFailed, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"Message": "hello"},
	})
	if err != nil {
		t.Fatalf(fmtCallToolFailed, err)
	}

	if result.IsError {
		tc, _ := result.Content[0].(*mcp.TextContent)
		t.Fatalf("tool returned error: %s", tc.Text)
	}

	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf(fmtWantTextContent, result.Content[0])
	}
	if tc.Text != wantEchoReply {
		t.Errorf(fmtGotWant, tc.Text, wantEchoReply)
	}
}

// makeTestJWT creates an HMAC-signed JWT with the given nested claims.
// The JWT structure matches what our OAuth server produces:
//
//	{ "sub": userID, "iss": issuer, "claims": { ...keycloakClaims } }
func makeTestJWT(t *testing.T, signingKey []byte, issuer, userID string, keycloakClaims map[string]any) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": userID,
		"aud": "test-client",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	if keycloakClaims != nil {
		claims["claims"] = keycloakClaims
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(signingKey)
	if err != nil {
		t.Fatalf("signing JWT: %v", err)
	}
	return signed
}

// buildProductionMiddleware sets up the full production-like middleware chain:
// OAuth JWT auth + Authorizer with the same config as production.
func buildProductionMiddleware(t *testing.T, signingKey []byte, issuer string) (middleware.Authenticator, middleware.Authorizer) {
	t.Helper()

	// Create OAuth JWT authenticator (same as production)
	oauthAuth, err := auth.NewOAuthJWTAuthenticator(auth.OAuthJWTConfig{
		Issuer:        issuer,
		SigningKey:    signingKey,
		RoleClaimPath: "realm_access.roles",
		RolePrefix:    "dp_",
	})
	if err != nil {
		t.Fatalf("creating authenticator: %v", err)
	}

	// Create persona registry matching production config
	registry := persona.NewRegistry()
	_ = registry.Register(&persona.Persona{
		Name:  "admin",
		Roles: []string{"dp_admin"},
		Tools: persona.ToolRules{Allow: []string{"*"}},
	})
	_ = registry.Register(&persona.Persona{
		Name:  "analyst",
		Roles: []string{"dp_analyst"},
		Tools: persona.ToolRules{
			Allow: []string{"trino_*", "datahub_*", "s3_list_*"},
			Deny:  []string{"*_delete_*"},
		},
	})
	// Default persona: denies all (same as production)
	_ = registry.Register(&persona.Persona{
		Name:  "default",
		Tools: persona.ToolRules{Allow: []string{}, Deny: []string{"*"}},
	})
	registry.SetDefault("default")

	// Create OIDCRoleMapper + Authorizer (same as production)
	mapper := &persona.OIDCRoleMapper{
		ClaimPath:  "realm_access.roles",
		RolePrefix: "dp_",
		PersonaMapping: map[string]string{
			"dp_admin":   "admin",
			"dp_analyst": "analyst",
		},
		Registry: registry,
	}
	authorizer := persona.NewAuthorizer(registry, mapper)

	// Chain authenticators (same as production)
	chainedAuth := auth.NewChainedAuthenticator(
		auth.ChainedAuthConfig{AllowAnonymous: false},
		oauthAuth,
	)

	return chainedAuth, authorizer
}

// TestStreamableHTTP_OAuthJWT_WithRoles tests the full production flow:
// OAuth JWT with dp_admin role → Authorizer → tool call succeeds.
func TestStreamableHTTP_OAuthJWT_WithRoles(t *testing.T) {
	ctx := context.Background()
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	issuer := "https://mcp.test/oauth"

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct{ Message string }) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
		}, nil, nil
	})

	authenticator, authorizer := buildProductionMiddleware(t, signingKey, issuer)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, nil, "http"))

	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	handler := httpauth.MCPAuthGateway("")(streamHandler)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	// JWT with dp_admin role (like a Keycloak user WITH the right role)
	token := makeTestJWT(t, signingKey, issuer, "user-123", map[string]any{
		"email": "admin@example.com",
		"realm_access": map[string]any{
			"roles": []any{"dp_admin", "user", "offline_access"},
		},
	})

	httpClient := &http.Client{
		Transport: &authRoundTripper{token: token, base: http.DefaultTransport},
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf(fmtConnectFailed, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"Message": "hello"},
	})
	if err != nil {
		t.Fatalf(fmtCallToolFailed, err)
	}

	if result.IsError {
		tc, _ := result.Content[0].(*mcp.TextContent)
		t.Fatalf("tool returned error: %s", tc.Text)
	}

	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf(fmtWantTextContent, result.Content[0])
	}
	if tc.Text != wantEchoReply {
		t.Errorf(fmtGotWant, tc.Text, wantEchoReply)
	}
}

// TestStreamableHTTP_OAuthJWT_NoRoles_DeniedByPersona reproduces the
// production bug: OAuth JWT is VALID (auth succeeds) but the Keycloak
// user has NO dp_* roles, so Authorizer falls back to the default
// persona which denies all tools. Claude.ai shows "Tool execution failed".
func TestStreamableHTTP_OAuthJWT_NoRoles_DeniedByPersona(t *testing.T) {
	ctx := context.Background()
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	issuer := "https://mcp.test/oauth"

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct{ Message string }) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
		}, nil, nil
	})

	authenticator, authorizer := buildProductionMiddleware(t, signingKey, issuer)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(authenticator, authorizer, nil, "http"))

	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	handler := httpauth.MCPAuthGateway("")(streamHandler)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	// JWT with NO dp_* roles (typical Keycloak user without platform roles)
	// Auth succeeds (valid JWT) but authz DENIES because no persona matches.
	token := makeTestJWT(t, signingKey, issuer, "user-456", map[string]any{
		"email": "newuser@example.com",
		"realm_access": map[string]any{
			"roles": []any{"user", "offline_access", "uma_authorization"},
		},
	})

	httpClient := &http.Client{
		Transport: &authRoundTripper{token: token, base: http.DefaultTransport},
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf(fmtConnectFailed, err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"Message": "hello"},
	})
	if err != nil {
		t.Fatalf(fmtCallToolFailed, err)
	}

	// The tool call should return an error result (not a transport error)
	if !result.IsError {
		t.Fatal("expected tool error (persona denial), but got success")
	}

	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf(fmtWantTextContent, result.Content[0])
	}

	// Verify the EXACT error message that production returns
	if !strings.Contains(tc.Text, "not authorized") {
		t.Errorf("expected 'not authorized' in error, got: %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "persona: default") {
		t.Errorf("expected 'persona: default' in error, got: %q", tc.Text)
	}
	t.Logf("Confirmed denial message: %s", tc.Text)
}
