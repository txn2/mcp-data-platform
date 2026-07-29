package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/httpserver/httpauth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// outageAuthenticator models an identity provider the platform cannot reach:
// every validation attempt returns a wrapped ErrValidationUnavailable, exactly
// as pkg/auth/oidc.go does when the JWKS cache is expired and the refresh fails.
type outageAuthenticator struct{ calls atomic.Int32 }

func (a *outageAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	a.calls.Add(1)
	return nil, fmt.Errorf("refreshing jwks: %w", middleware.ErrValidationUnavailable)
}

// rejectingAuthenticator models a definitive credential rejection, the case the
// edge must still fail closed on.
type rejectingAuthenticator struct{}

func (rejectingAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return nil, errors.New("token signature invalid")
}

// outagePersonaAuthorizer grants everything, so any refusal the test observes
// comes from the authentication half rather than persona filtering.
func outagePersonaAuthorizer(t *testing.T) middleware.Authorizer {
	t.Helper()
	registry := persona.NewRegistry()
	if err := registry.Register(&persona.Persona{
		Name:  "admin",
		Roles: []string{"dp_admin"},
		Tools: persona.ToolRules{Allow: []string{"*"}},
	}); err != nil {
		t.Fatalf("registering persona: %v", err)
	}
	return persona.NewAuthorizer(registry, &persona.OIDCRoleMapper{
		ClaimPath:      "realm_access.roles",
		RolePrefix:     "dp_",
		PersonaMapping: map[string]string{"dp_admin": "admin"},
		Registry:       registry,
	})
}

// outageServer wires the production shape — MCP server with the tool-call
// middleware, the streamable HTTP handler, and the HTTP auth gateway in front —
// around one authenticator used by both layers, as the composition root does.
// It returns the test server and a flag reporting whether the tool ever ran.
func outageServer(t *testing.T, authenticator middleware.Authenticator) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	var toolRan atomic.Bool

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, args struct{ Message string }) (*mcp.CallToolResult, any, error) {
			toolRan.Store(true)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}}}, nil, nil
		})
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		authenticator, outagePersonaAuthorizer(t), nil,
		middleware.ToolCallConfig{Transport: transportHTTP, AdminPersona: "admin"},
	))

	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	handler := httpauth.MCPAuthGateway(authenticator, "https://mcp.test/.well-known/oauth-protected-resource")(streamHandler)

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	return httpServer, &toolRan
}

// TestStreamableHTTP_ValidationUnavailable_EdgePassesThroughProtocolRefuses pins
// the recorded decision for an identity-provider outage (issue #1073): the HTTP
// edge passes a credential it cannot validate through, and the protocol layer
// refuses the tool call in band as retryable. The two halves are one control —
// a change to either that is not matched by the other turns a deliberate
// fail-open into a real gap, and this test is what fails when that happens.
func TestStreamableHTTP_ValidationUnavailable_EdgePassesThroughProtocolRefuses(t *testing.T) {
	ctx := context.Background()
	authenticator := &outageAuthenticator{}
	httpServer, toolRan := outageServer(t, authenticator)

	// Half one: the HTTP edge does not answer 401 during the outage. Asserted on
	// the raw response, before any MCP client machinery can obscure the status.
	resp := postInitialize(t, httpServer.URL, "token-that-cannot-be-validated")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("HTTP edge answered 401 during a validation outage; the recorded decision is pass-through (see docs/security/threat-model.md)")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", resp.StatusCode)
	}
	if authenticator.calls.Load() == 0 {
		t.Fatal("edge never called the authenticator; the gate is not validating at all")
	}

	// Half two: the protocol layer refuses the tool call as retryable.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
		HTTPClient: &http.Client{
			Transport: &authRoundTripper{token: "token-that-cannot-be-validated", base: http.DefaultTransport},
		},
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
	if !result.IsError {
		t.Fatal("tool call succeeded while credentials could not be validated")
	}
	if toolRan.Load() {
		t.Fatal("tool handler executed while credentials could not be validated")
	}

	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf(fmtWantTextContent, result.Content[0])
	}
	if !strings.Contains(tc.Text, "authentication temporarily unavailable") {
		t.Errorf("refusal text = %q, want it to name the transient condition", tc.Text)
	}

	// The refusal must be the retryable category, not an identity problem: a
	// client that reads this as bad credentials starts a re-auth flow that
	// cannot succeed until the identity provider is back.
	sc, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a map carrying the error contract", result.StructuredContent)
	}
	e, ok := sc["error"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent has no error object: %v", sc)
	}
	if got := e["code"]; got != middleware.CodeFeatureUnavailable {
		t.Errorf("error code = %v, want %s", got, middleware.CodeFeatureUnavailable)
	}
	if got := e["category"]; got != middleware.ErrCategoryUnavailable {
		t.Errorf("error category = %v, want %s", got, middleware.ErrCategoryUnavailable)
	}
}

// TestStreamableHTTP_DefinitiveRejection_EdgeFailsClosed is the other half of
// the same decision: pass-through applies only to an undetermined verdict. A
// credential the platform can definitively reject is still refused at the edge
// with 401 + error="invalid_token", the signal MCP clients key re-auth off.
func TestStreamableHTTP_DefinitiveRejection_EdgeFailsClosed(t *testing.T) {
	httpServer, _ := outageServer(t, rejectingAuthenticator{})

	resp := postInitialize(t, httpServer.URL, "definitively-bad-token")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("initialize status = %d, want 401 for a definitively invalid token", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `error="invalid_token"`) {
		t.Errorf("WWW-Authenticate = %q, want it to carry error=\"invalid_token\"", got)
	}

	// The client half of the same boundary: the session never establishes, so
	// the caller sees a transport-level refusal rather than the in-band
	// retryable error an outage produces.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
		HTTPClient: &http.Client{
			Transport: &authRoundTripper{token: "definitively-bad-token", base: http.DefaultTransport},
		},
	}, nil)
	if err == nil {
		_ = session.Close()
		t.Fatal("MCP session established behind a definitively invalid token")
	}
}
