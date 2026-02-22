package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Test constants to avoid repeated string literals.
const (
	mcpTestUserID     = "user1"
	mcpTestEmail      = "user1@example.com"
	mcpTestPersona    = "analyst"
	mcpTestToolName   = "test_tool"
	mcpTestMethod     = "tools/call"
	mcpTestStdio      = "stdio"
	mcpTestErrFmt     = "unexpected error: %v"
	mcpTestResultFmt  = "expected CallToolResult, got %T"
	mcpTestPCExpected = "expected platform context to be set"
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

func (m *mcpTestAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _ string) (authorized bool, persona, reason string) {
	return m.authorized, m.personaName, m.reason
}

// mcpTestToolkitLookup implements ToolkitLookup for MCP middleware testing.
type mcpTestToolkitLookup struct {
	kind       string
	name       string
	connection string
	found      bool
}

func (m *mcpTestToolkitLookup) GetToolkitForTool(_ string) registry.ToolkitMatch {
	return registry.ToolkitMatch{Kind: m.kind, Name: m.name, Connection: m.connection, Found: m.found}
}

// mcpTestRequest wraps ServerRequest for testing.
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

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called on auth failure")
		return nil, nil //nolint:nilnil // unreachable after t.Fatal
	}

	handler := middleware(next)
	req := newMCPTestRequest(mcpTestToolName)

	result, err := handler(context.Background(), mcpTestMethod, req)
	if err != nil {
		t.Fatalf(mcpTestErrFmt, err)
	}

	// Result should be an error result
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf(mcpTestResultFmt, result)
	}
	if !toolResult.IsError {
		t.Error("expected IsError to be true")
	}
}

func TestMCPToolCallMiddleware_AuthorizationFailure(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: mcpTestUserID,
			Roles:  []string{"viewer"},
		},
	}
	authorizer := &mcpTestAuthorizer{
		authorized:  false,
		personaName: "viewer",
		reason:      "tool not allowed for persona",
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called on authz failure")
		return nil, nil //nolint:nilnil // unreachable after t.Fatal
	}

	handler := middleware(next)
	req := newMCPTestRequest("admin_tool")

	result, err := handler(context.Background(), mcpTestMethod, req)
	if err != nil {
		t.Fatalf(mcpTestErrFmt, err)
	}

	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf(mcpTestResultFmt, result)
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
			UserID: mcpTestUserID,
			Email:  mcpTestEmail,
			Roles:  []string{mcpTestPersona},
		},
	}
	authorizer := &mcpTestAuthorizer{authorized: true, personaName: mcpTestPersona}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

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
			t.Error(mcpTestPCExpected)
			return expectedResult, nil
		}
		if pc.UserID != mcpTestUserID {
			t.Errorf("expected UserID %q, got %q", mcpTestUserID, pc.UserID)
		}
		if pc.ToolName != mcpTestToolName {
			t.Errorf("expected ToolName %q, got %q", mcpTestToolName, pc.ToolName)
		}
		if !pc.Authorized {
			t.Error("expected Authorized to be true")
		}
		if pc.Transport != mcpTestStdio {
			t.Errorf("expected Transport %q, got %q", mcpTestStdio, pc.Transport)
		}
		if pc.Source != "mcp" {
			t.Errorf("expected Source %q, got %q", "mcp", pc.Source)
		}

		return expectedResult, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest(mcpTestToolName)

	result, err := handler(context.Background(), mcpTestMethod, req)
	if err != nil {
		t.Fatalf(mcpTestErrFmt, err)
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

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

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
		userInfo: &UserInfo{UserID: mcpTestUserID},
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called with missing tool name")
		return nil, nil //nolint:nilnil // unreachable after t.Fatal
	}

	handler := middleware(next)

	// Empty tool name
	req := newMCPTestRequest("")

	result, err := handler(context.Background(), mcpTestMethod, req)
	if result != nil {
		t.Fatal("expected nil result for invalid params")
	}
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) {
		t.Fatalf("expected *jsonrpc.Error, got %T", err)
	}
	if wireErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams (%d), got %d", jsonrpc.CodeInvalidParams, wireErr.Code)
	}
}

func TestMCPToolCallMiddleware_NilParams(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{UserID: mcpTestUserID},
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called with nil params")
		return nil, nil //nolint:nilnil // unreachable after t.Fatal
	}

	handler := middleware(next)

	// Create a ServerRequest with nil Params
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: nil,
	}

	result, err := handler(context.Background(), mcpTestMethod, req)
	if result != nil {
		t.Fatal("expected nil result for invalid params")
	}
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) {
		t.Fatalf("expected *jsonrpc.Error, got %T", err)
	}
	if wireErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams (%d), got %d", jsonrpc.CodeInvalidParams, wireErr.Code)
	}
}

func TestMCPToolCallMiddleware_WrongParamsType(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{UserID: mcpTestUserID},
	}
	authorizer := &mcpTestAuthorizer{authorized: true}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Fatal("next should not be called with wrong params type")
		return nil, nil //nolint:nilnil // unreachable after t.Fatal
	}

	handler := middleware(next)

	// Create a ServerRequest with a different params type (ListToolsParams instead of CallToolParamsRaw)
	req := &mcp.ServerRequest[*mcp.ListToolsParams]{
		Params: &mcp.ListToolsParams{},
	}

	result, err := handler(context.Background(), mcpTestMethod, req)
	if result != nil {
		t.Fatal("expected nil result for invalid params")
	}
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) {
		t.Fatalf("expected *jsonrpc.Error, got %T", err)
	}
	if wireErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams (%d), got %d", jsonrpc.CodeInvalidParams, wireErr.Code)
	}
}

func TestMCPToolCallMiddleware_ToolkitLookup(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: mcpTestUserID,
			Email:  mcpTestEmail,
			Roles:  []string{mcpTestPersona},
		},
	}
	authorizer := &mcpTestAuthorizer{
		authorized:  true,
		personaName: mcpTestPersona,
	}
	toolkitLookup := &mcpTestToolkitLookup{
		kind:       "trino",
		name:       "production",
		connection: "prod-trino",
		found:      true,
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, mcpTestStdio)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "success"},
		},
	}

	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		// Verify platform context has all fields populated
		pc := GetPlatformContext(ctx)
		if pc == nil {
			t.Fatal(mcpTestPCExpected)
		}
		if pc.ToolName != testAuditToolName {
			t.Errorf("expected ToolName %q, got %q", testAuditToolName, pc.ToolName)
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
		if pc.PersonaName != mcpTestPersona {
			t.Errorf("expected PersonaName %q, got %q", mcpTestPersona, pc.PersonaName)
		}
		if pc.UserID != mcpTestUserID {
			t.Errorf("expected UserID %q, got %q", mcpTestUserID, pc.UserID)
		}

		return expectedResult, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest(testAuditToolName)

	result, err := handler(context.Background(), mcpTestMethod, req)
	if err != nil {
		t.Fatalf(mcpTestErrFmt, err)
	}

	if result != expectedResult {
		t.Error("expected result to be passed through")
	}
}

func TestMCPToolCallMiddleware_ToolkitLookupNotFound(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: mcpTestUserID,
			Roles:  []string{mcpTestPersona},
		},
	}
	authorizer := &mcpTestAuthorizer{
		authorized:  true,
		personaName: mcpTestPersona,
	}
	toolkitLookup := &mcpTestToolkitLookup{
		found: false, // Tool not found in any toolkit
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, mcpTestStdio)

	expectedResult := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "success"},
		},
	}

	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		// Verify platform context - toolkit fields should be empty
		pc := GetPlatformContext(ctx)
		if pc == nil {
			t.Fatal(mcpTestPCExpected)
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
		if pc.PersonaName != mcpTestPersona {
			t.Errorf("expected PersonaName %q, got %q", mcpTestPersona, pc.PersonaName)
		}

		return expectedResult, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest("unknown_tool")

	_, err := handler(context.Background(), mcpTestMethod, req)
	if err != nil {
		t.Fatalf(mcpTestErrFmt, err)
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

func TestExtractSessionID(t *testing.T) {
	t.Run("nil request returns stdio", func(t *testing.T) {
		result := extractSessionID(nil)
		if result != mcpTestStdio {
			t.Errorf("extractSessionID(nil) = %q, want %q", result, mcpTestStdio)
		}
	})

	t.Run("request without session returns stdio", func(t *testing.T) {
		req := newMCPTestRequest(mcpTestToolName)
		result := extractSessionID(req)
		if result != mcpTestStdio {
			t.Errorf("extractSessionID() = %q, want %q", result, mcpTestStdio)
		}
	})
}

func TestMCPToolCallMiddleware_SessionIDPopulated(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: mcpTestUserID,
			Roles:  []string{mcpTestPersona},
		},
	}
	authorizer := &mcpTestAuthorizer{authorized: true, personaName: mcpTestPersona}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		pc := GetPlatformContext(ctx)
		if pc == nil {
			t.Fatal(mcpTestPCExpected)
		}
		// For a test request without session, should default to "stdio"
		if pc.SessionID != mcpTestStdio {
			t.Errorf("expected SessionID %q, got %q", mcpTestStdio, pc.SessionID)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	}

	handler := middleware(next)
	req := newMCPTestRequest(mcpTestToolName)

	_, err := handler(context.Background(), mcpTestMethod, req)
	if err != nil {
		t.Fatalf(mcpTestErrFmt, err)
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

	mw := MCPToolCallMiddleware(authenticator, authorizer, nil, mcpTestStdio)

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil
	}

	handler := mw(next)

	t.Run("bearer token from RequestExtra", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequestWithExtra(mcpTestToolName, http.Header{
			"Authorization": {"Bearer streamable-token"},
		})

		_, err := handler(context.Background(), mcpTestMethod, req)
		if err != nil {
			t.Fatalf(mcpTestErrFmt, err)
		}

		if capture.capturedToken != "streamable-token" {
			t.Errorf("expected captured token 'streamable-token', got %q", capture.capturedToken)
		}
	})

	t.Run("api key from RequestExtra", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequestWithExtra(mcpTestToolName, http.Header{
			"X-Api-Key": {"my-api-key"},
		})

		_, err := handler(context.Background(), mcpTestMethod, req)
		if err != nil {
			t.Fatalf(mcpTestErrFmt, err)
		}

		if capture.capturedToken != "my-api-key" {
			t.Errorf("expected captured token 'my-api-key', got %q", capture.capturedToken)
		}
	})

	t.Run("existing context token takes precedence", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequestWithExtra(mcpTestToolName, http.Header{
			"Authorization": {"Bearer extra-token"},
		})

		// Pre-set token in context (as SSE HTTP middleware would do)
		ctx := WithToken(context.Background(), "sse-token")

		_, err := handler(ctx, mcpTestMethod, req)
		if err != nil {
			t.Fatalf(mcpTestErrFmt, err)
		}

		if capture.capturedToken != "sse-token" {
			t.Errorf("expected context token 'sse-token' to take precedence, got %q", capture.capturedToken)
		}
	})

	t.Run("nil extra does not panic", func(t *testing.T) {
		capture.capturedToken = ""
		req := newMCPTestRequest(mcpTestToolName)

		_, err := handler(context.Background(), mcpTestMethod, req)
		if err != nil {
			t.Fatalf(mcpTestErrFmt, err)
		}
		// Token should be empty since there's no Extra and no context token
		if capture.capturedToken != "" {
			t.Errorf("expected empty token, got %q", capture.capturedToken)
		}
	})
}

func TestExtractServerSession(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		ss := extractServerSession(nil)
		if ss != nil {
			t.Error("expected nil for nil request")
		}
	})

	t.Run("request without session", func(t *testing.T) {
		req := newMCPTestRequest(mcpTestToolName)
		ss := extractServerSession(req)
		if ss != nil {
			t.Error("expected nil for request without real session")
		}
	})
}

func TestExtractProgressToken(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		pt := extractProgressToken(nil)
		if pt != nil {
			t.Errorf("expected nil, got %v", pt)
		}
	})

	t.Run("request without progress token", func(t *testing.T) {
		req := newMCPTestRequest(mcpTestToolName)
		pt := extractProgressToken(req)
		if pt != nil {
			t.Errorf("expected nil, got %v", pt)
		}
	})

	t.Run("request with progress token", func(t *testing.T) {
		params := &mcp.CallToolParamsRaw{
			Name: mcpTestToolName,
		}
		// Initialize meta map before setting progress token.
		params.SetMeta(map[string]any{})
		params.SetProgressToken("tok-abc")
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: params,
		}
		pt := extractProgressToken(req)
		if pt != "tok-abc" {
			t.Errorf("expected 'tok-abc', got %v", pt)
		}
	})

	t.Run("nil params", func(t *testing.T) {
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: nil,
		}
		pt := extractProgressToken(req)
		if pt != nil {
			t.Errorf("expected nil, got %v", pt)
		}
	})
}

func TestPlatformError(t *testing.T) {
	t.Run("implements error interface", func(t *testing.T) {
		err := &PlatformError{Category: ErrCategoryAuth, Message: "auth failed"}
		if err.Error() != "auth failed" {
			t.Errorf("Error() = %q, want %q", err.Error(), "auth failed")
		}
	})

	t.Run("ErrorCategory extracts category", func(t *testing.T) {
		err := &PlatformError{Category: ErrCategoryAuthz, Message: "denied"}
		if got := ErrorCategory(err); got != ErrCategoryAuthz {
			t.Errorf("ErrorCategory = %q, want %q", got, ErrCategoryAuthz)
		}
	})

	t.Run("ErrorCategory returns empty for plain error", func(t *testing.T) {
		err := errors.New("plain error")
		if got := ErrorCategory(err); got != "" {
			t.Errorf("ErrorCategory = %q, want empty", got)
		}
	})

	t.Run("ErrorCategory returns empty for nil", func(t *testing.T) {
		if got := ErrorCategory(nil); got != "" {
			t.Errorf("ErrorCategory = %q, want empty", got)
		}
	})
}

func TestCreateCategorizedErrorResult(t *testing.T) {
	result := createCategorizedErrorResult(ErrCategoryAuth, "auth failed")
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		t.Fatal("result is not *mcp.CallToolResult")
	}
	if !callResult.IsError {
		t.Error("expected IsError to be true")
	}

	// Verify the error category is embedded
	err := callResult.GetError()
	if err == nil {
		t.Fatal("GetError() returned nil")
	}
	if got := ErrorCategory(err); got != ErrCategoryAuth {
		t.Errorf("ErrorCategory = %q, want %q", got, ErrCategoryAuth)
	}
}

func TestNewInvalidParamsError(t *testing.T) {
	err := newInvalidParamsError("missing tool name")

	if err.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("Code = %d, want %d", err.Code, jsonrpc.CodeInvalidParams)
	}
	if err.Message != "missing tool name" {
		t.Errorf("Message = %q, want %q", err.Message, "missing tool name")
	}
	// Verify it satisfies the error interface
	var goErr error = err
	if goErr.Error() == "" {
		t.Error("expected non-empty Error() string")
	}
}

func TestExtractConnectionArg(t *testing.T) {
	tests := []struct {
		name string
		req  mcp.Request
		want string
	}{
		{
			name: "nil request",
			req:  nil,
			want: "",
		},
		{
			name: "no arguments",
			req:  newMCPTestRequest(mcpTestToolName),
			want: "",
		},
		{
			name: "connection present",
			req: &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
				Params: &mcp.CallToolParamsRaw{
					Name:      mcpTestToolName,
					Arguments: json.RawMessage(`{"connection":"warehouse","sql":"SELECT 1"}`),
				},
			},
			want: "warehouse",
		},
		{
			name: "connection absent in args",
			req: &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
				Params: &mcp.CallToolParamsRaw{
					Name:      mcpTestToolName,
					Arguments: json.RawMessage(`{"sql":"SELECT 1"}`),
				},
			},
			want: "",
		},
		{
			name: "malformed JSON arguments",
			req: &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
				Params: &mcp.CallToolParamsRaw{
					Name:      mcpTestToolName,
					Arguments: json.RawMessage(`{invalid`),
				},
			},
			want: "",
		},
		{
			name: "connection is non-string",
			req: &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
				Params: &mcp.CallToolParamsRaw{
					Name:      mcpTestToolName,
					Arguments: json.RawMessage(`{"connection":42}`),
				},
			},
			want: "",
		},
		{
			name: "nil params",
			req: &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
				Params: nil,
			},
			want: "",
		},
		{
			name: "wrong params type",
			req: &mcp.ServerRequest[*mcp.ListToolsParams]{
				Params: &mcp.ListToolsParams{},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractConnectionArg(tt.req)
			if got != tt.want {
				t.Errorf("extractConnectionArg() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMCPToolCallMiddleware_ConnectionOverride(t *testing.T) {
	authenticator := &mcpTestAuthenticator{
		userInfo: &UserInfo{
			UserID: mcpTestUserID,
			Roles:  []string{mcpTestPersona},
		},
	}
	authorizer := &mcpTestAuthorizer{authorized: true, personaName: mcpTestPersona}
	toolkitLookup := &mcpTestToolkitLookup{
		kind:       "trino",
		name:       "default-trino",
		connection: "default-trino",
		found:      true,
	}

	middleware := MCPToolCallMiddleware(authenticator, authorizer, toolkitLookup, mcpTestStdio)

	t.Run("connection arg overrides toolkit default", func(t *testing.T) {
		next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			pc := GetPlatformContext(ctx)
			if pc == nil {
				t.Fatal(mcpTestPCExpected)
			}
			if pc.Connection != "elasticsearch" {
				t.Errorf("Connection = %q, want 'elasticsearch'", pc.Connection)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
			}, nil
		}

		handler := middleware(next)
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: &mcp.CallToolParamsRaw{
				Name:      testAuditToolName,
				Arguments: json.RawMessage(`{"connection":"elasticsearch","sql":"SELECT 1"}`),
			},
		}

		_, err := handler(context.Background(), mcpTestMethod, req)
		if err != nil {
			t.Fatalf(mcpTestErrFmt, err)
		}
	})

	t.Run("no connection arg keeps toolkit default", func(t *testing.T) {
		next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			pc := GetPlatformContext(ctx)
			if pc == nil {
				t.Fatal(mcpTestPCExpected)
			}
			if pc.Connection != "default-trino" {
				t.Errorf("Connection = %q, want 'default-trino'", pc.Connection)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
			}, nil
		}

		handler := middleware(next)
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
			Params: &mcp.CallToolParamsRaw{
				Name:      testAuditToolName,
				Arguments: json.RawMessage(`{"sql":"SELECT 1"}`),
			},
		}

		_, err := handler(context.Background(), mcpTestMethod, req)
		if err != nil {
			t.Fatalf(mcpTestErrFmt, err)
		}
	})
}
