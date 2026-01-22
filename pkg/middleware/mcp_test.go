package middleware

import (
	"context"
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
	authorized bool
	reason     string
}

func (m *mcpTestAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (bool, string) {
	return m.authorized, m.reason
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

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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
		authorized: false,
		reason:     "tool not allowed for persona",
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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

	middleware := MCPToolCallMiddleware(authenticator, authorizer)

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
