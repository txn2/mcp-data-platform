package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpTestAuthenticator implements Authenticator for MCP middleware testing.
type mcpTestAuthenticator struct {
	userInfo *UserInfo
	err      error
}

func (m *mcpTestAuthenticator) Authenticate(_ context.Context) (*UserInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.userInfo, nil
}

// mcpTestAuthorizer implements Authorizer for MCP middleware testing.
type mcpTestAuthorizer struct {
	authorized  bool
	personaName string
	reason      string
}

func (m *mcpTestAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (bool, string, string) {
	return m.authorized, m.personaName, m.reason
}

// mcpTestToolkitLookup implements ToolkitLookup for MCP middleware testing.
type mcpTestToolkitLookup struct {
	kind       string
	name       string
	connection string
	found      bool
}

func (m *mcpTestToolkitLookup) GetToolkitForTool(_ string) (string, string, string, bool) {
	return m.kind, m.name, m.connection, m.found
}

// mcpTestRequest wraps ServerRequest for testing
type mcpTestRequest struct {
	mcp.ServerRequest[*mcp.CallToolParamsRaw]
}

func newMCPTestRequest(toolName string) *mcpTestRequest {
	return &mcpTestRequest{
		ServerRequest: mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: &mcp.CallToolParamsRaw{
				Name: toolName,
			},
		},
	}
}

func TestMCPToolCallMiddleware_AuthenticationFailure(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		err: context.DeadlineExceeded,
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called on auth failure")
		return nil, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest("test_tool")

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should be an error result
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", result)
	}
	if !toolResult.IsError {
		t.Error("expected IsError to be true")
	}
}

func TestMCPToolCallMiddleware_AuthorizationFailure(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: "user1",
			Roles:  []string{"viewer"},
		},
	}
	authorizer := &mcpTestAuthorizer{
		authorized:  false,
		personaName: "viewer",
		reason:      "tool not allowed for persona",
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called on authz failure")
		return nil, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest("admin_tool")

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", result)
	}
	if !toolResult.IsError {
		t.Error("expected IsError to be true")
	}

	// Check error message contains reason
	if len(toolResult.Content) == 0 {
		t.Fatal("expected content in result")
	}
	textContent, ok := toolResult.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", toolResult.Content[0])
	}
	if textContent.Text == "" {
		t.Error("expected error message in content")
	}
}

func TestMCPToolCallMiddleware_Success(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: "user1",
			Email:  "user1@example.com",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &mcpTestAuthorizer{authorized: true, personaName: "analyst"}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "success"},
		},
	}

	nextCalled := false
	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true

		// Verify platform context was set
		pc := GetPlatformContext(ctx)
		if pc == nil {
			t.Error("expected platform context to be set")
			return expectedResult, nil
		}
		if pc.UserID != "user1" {
			t.Errorf("expected UserID 'user1', got %q", pc.UserID)
		}
		if pc.ToolName != "test_tool" {
			t.Errorf("expected ToolName 'test_tool', got %q", pc.ToolName)
		}
		if !pc.Authorized {
			t.Error("expected Authorized to be true")
		}

		return expectedResult, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest("test_tool")

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !nextCalled {
		t.Error("expected next handler to be called")
	}

	if result != expectedResult {
		t.Error("expected result to be passed through")
	}
}

func TestMCPToolCallMiddleware_NonToolsCallPassthrough(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		err: context.DeadlineExceeded, // Would fail if called
	}
	authorizer := &mcpTestAuthorizer{authorized: false}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "passthrough"},
		},
	}

	nextCalled := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return expectedResult, nil
	}

	handler := middleware(next)

	// Use a different method than tools/call - pass any request
	req := newMCPTestRequest("any")

	result, err := handler(context.Background(), "resources/read", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !nextCalled {
		t.Error("expected next handler to be called for non-tools/call")
	}

	if result != expectedResult {
		t.Error("expected result to be passed through")
	}
}

func TestMCPToolCallMiddleware_MissingToolName(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{UserID: "user1"},
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called with missing tool name")
		return nil, nil
	}

	handler := middleware(next)

	// Empty tool name
	req := newMCPTestRequest("")

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", result)
	}
	if !toolResult.IsError {
		t.Error("expected IsError to be true for missing tool name")
	}
}

func TestMCPToolCallMiddleware_NilParams(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{UserID: "user1"},
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called with nil params")
		return nil, nil
	}

	handler := middleware(next)

	// Create a ServerRequest with nil Params
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: nil,
	}

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", result)
	}
	if !toolResult.IsError {
		t.Error("expected IsError to be true for nil params")
	}
}

func TestMCPToolCallMiddleware_WrongParamsType(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{UserID: "user1"},
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called with wrong params type")
		return nil, nil
	}

	handler := middleware(next)

	// Create a ServerRequest with a different params type (ListToolsParams instead of CallToolParamsRaw)
	req := &mcp.ServerRequest[*mcp.ListToolsParams]{
		Params: &mcp.ListToolsParams{},
	}

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", result)
	}
	if !toolResult.IsError {
		t.Error("expected IsError to be true for wrong params type")
	}
}

func TestMCPToolCallMiddleware_ToolkitLookup(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: "user1",
			Email:  "user1@example.com",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &mcpTestAuthorizer{
		authorized:  true,
		personaName: "analyst",
	}
	toolkitLookup := &mcpTestToolkitLookup{
		kind:       "trino",
		name:       "production",
		connection: "prod-trino",
		found:      true,
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "success"},
		},
	}

	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		// Verify platform context has all fields populated
		pc := GetPlatformContext(ctx)
		if pc == nil {
			t.Fatal("expected platform context to be set")
		}
		if pc.ToolName != "trino_query" {
			t.Errorf("expected ToolName 'trino_query', got %q", pc.ToolName)
		}
		if pc.ToolkitKind != "trino" {
			t.Errorf("expected ToolkitKind 'trino', got %q", pc.ToolkitKind)
		}
		if pc.ToolkitName != "production" {
			t.Errorf("expected ToolkitName 'production', got %q", pc.ToolkitName)
		}
		if pc.Connection != "prod-trino" {
			t.Errorf("expected Connection 'prod-trino', got %q", pc.Connection)
		}
		if pc.PersonaName != "analyst" {
			t.Errorf("expected PersonaName 'analyst', got %q", pc.PersonaName)
		}
		if pc.UserID != "user1" {
			t.Errorf("expected UserID 'user1', got %q", pc.UserID)
		}

		return expectedResult, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest("trino_query")

	result, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != expectedResult {
		t.Error("expected result to be passed through")
	}
}

func TestMCPToolCallMiddleware_ToolkitLookupNotFound(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: "user1",
			Roles:  []string{"analyst"},
		},
	}
	authorizer := &mcpTestAuthorizer{
		authorized:  true,
		personaName: "analyst",
	}
	toolkitLookup := &mcpTestToolkitLookup{
		found: false, // Tool not found in any toolkit
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "success"},
		},
	}

	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		// Verify platform context - toolkit fields should be empty
		pc := GetPlatformContext(ctx)
		if pc == nil {
			t.Fatal("expected platform context to be set")
		}
		if pc.ToolkitKind != "" {
			t.Errorf("expected empty ToolkitKind, got %q", pc.ToolkitKind)
		}
		if pc.ToolkitName != "" {
			t.Errorf("expected empty ToolkitName, got %q", pc.ToolkitName)
		}
		if pc.Connection != "" {
			t.Errorf("expected empty Connection, got %q", pc.Connection)
		}
		// PersonaName should still be populated
		if pc.PersonaName != "analyst" {
			t.Errorf("expected PersonaName 'analyst', got %q", pc.PersonaName)
		}

		return expectedResult, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest("unknown_tool")

	_, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractBearerOrAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name:   "bearer token",
			header: http.Header{"Authorization": {"Bearer my-token"}},
			want:   "my-token",
		},
		{
			name:   "api key",
			header: http.Header{"X-Api-Key": {"api-key-123"}},
			want:   "api-key-123",
		},
		{
			name: "bearer preferred over api key",
			header: http.Header{
				"Authorization": {"Bearer bearer-token"},
				"X-Api-Key":     {"api-key"},
			},
			want: "bearer-token",
		},
		{
			name:   "no auth headers",
			header: http.Header{"Content-Type": {"application/json"}},
			want:   "",
		},
		{
			name:   "non-bearer authorization",
			header: http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}},
			want:   "",
		},
		{
			name:   "empty header",
			header: http.Header{},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBearerOrAPIKey(tt.header)
			if got != tt.want {
				t.Errorf("extractBearerOrAPIKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

// newMCPTestRequestWithExtra creates a test request with RequestExtra headers.
func newMCPTestRequestWithExtra(toolName string, headers http.Header) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{
			Name: toolName,
		},
		Extra: &mcp.RequestExtra{
			Header: headers,
		},
	}
}

func TestMCPToolCallMiddleware_AuthBridgeFromRequestExtra(t *testing.T) {
	// tokenCapturingAuthenticator captures the token from context during Authenticate.
	type tokenCapture struct {
		capturedToken string
	}
	capture := &tokenCapture{}

	authenticator := &mockAuthenticator{
		authenticateFunc: func(ctx context.Context) (*UserInfo, error) {
			capture.capturedToken = GetToken(ctx)
			return &UserInfo{
				UserID:   "api-user",
				Roles:    []string{"admin"},
				AuthType: "apikey",
			}, nil
		},
	}
	authorizer := &mcpTestAuthorizer{authorized: true, personaName: "admin"}

	mw := MCPToolCallMiddleware(authenticator, authorizer, nil)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	}

	handler := mw(next)

	t.Run("bearer token from RequestExtra", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequestWithExtra("test_tool", http.Header{
			"Authorization": {"Bearer streamable-token"},
		})

		_, err := handler(context.Background(), "tools/call", req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capture.capturedToken != "streamable-token" {
			t.Errorf("expected captured token 'streamable-token', got %q", capture.capturedToken)
		}
	})

	t.Run("api key from RequestExtra", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequestWithExtra("test_tool", http.Header{
			"X-Api-Key": {"my-api-key"},
		})

		_, err := handler(context.Background(), "tools/call", req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capture.capturedToken != "my-api-key" {
			t.Errorf("expected captured token 'my-api-key', got %q", capture.capturedToken)
		}
	})

	t.Run("existing context token takes precedence", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequestWithExtra("test_tool", http.Header{
			"Authorization": {"Bearer extra-token"},
		})

		// Pre-set token in context (as SSE HTTP middleware would do)
		ctx := WithToken(context.Background(), "sse-token")

		_, err := handler(ctx, "tools/call", req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if capture.capturedToken != "sse-token" {
			t.Errorf("expected context token 'sse-token' to take precedence, got %q", capture.capturedToken)
		}
	})

	t.Run("nil extra does not panic", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequest("test_tool")

		_, err := handler(context.Background(), "tools/call", req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Token should be empty since there's no Extra and no context token
		if capture.capturedToken != "" {
			t.Errorf("expected empty token, got %q", capture.capturedToken)
		}
	})
}
