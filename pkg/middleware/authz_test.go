package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockAuthorizer implements Authorizer for testing.
type mockAuthorizer struct {
	isAuthorizedFunc func(ctx context.Context, userID string, roles []string, toolName string) (bool, string)
}

func (m *mockAuthorizer) IsAuthorized(ctx context.Context, userID string, roles []string, toolName string) (bool, string) {
	if m.isAuthorizedFunc != nil {
		return m.isAuthorizedFunc(ctx, userID, roles, toolName)
	}
	return true, ""
}

func TestAuthzMiddleware(t *testing.T) {
	t.Run("no platform context", func(t *testing.T) {
		authz := &mockAuthorizer{}
		middleware := AuthzMiddleware(authz)
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return NewToolResultText("success"), nil
		})

		result, err := handler(context.Background(), mcp.CallToolRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected success result")
		}
	})

	t.Run("authorization success", func(t *testing.T) {
		authz := &mockAuthorizer{
			isAuthorizedFunc: func(_ context.Context, _ string, _ []string, _ string) (bool, string) {
				return true, ""
			},
		}

		middleware := AuthzMiddleware(authz)
		var capturedPC *PlatformContext
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			capturedPC = GetPlatformContext(ctx)
			return NewToolResultText("success"), nil
		})

		pc := &PlatformContext{
			UserID:   "user123",
			Roles:    []string{"admin"},
			ToolName: "test_tool",
		}
		ctx := WithPlatformContext(context.Background(), pc)
		result, err := handler(ctx, mcp.CallToolRequest{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected success result")
		}
		if !capturedPC.Authorized {
			t.Error("expected Authorized to be true")
		}
	})

	t.Run("authorization failure", func(t *testing.T) {
		authz := &mockAuthorizer{
			isAuthorizedFunc: func(_ context.Context, _ string, _ []string, _ string) (bool, string) {
				return false, "insufficient permissions"
			},
		}

		middleware := AuthzMiddleware(authz)
		handler := middleware(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Error("handler should not be called on authz failure")
			return nil, nil
		})

		pc := &PlatformContext{
			UserID:   "user123",
			Roles:    []string{"viewer"},
			ToolName: "admin_tool",
		}
		ctx := WithPlatformContext(context.Background(), pc)
		result, err := handler(ctx, mcp.CallToolRequest{})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result")
		}
		if pc.Authorized {
			t.Error("expected Authorized to be false")
		}
		if pc.AuthzError != "insufficient permissions" {
			t.Errorf("expected AuthzError 'insufficient permissions', got %q", pc.AuthzError)
		}
	})
}

func TestNoopAuthorizer(t *testing.T) {
	authz := &NoopAuthorizer{}
	authorized, reason := authz.IsAuthorized(context.Background(), "user", []string{"role"}, "tool")
	if !authorized {
		t.Error("expected authorized to be true")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestAllowAllAuthorizer(t *testing.T) {
	authz := AllowAllAuthorizer()
	authorized, reason := authz.IsAuthorized(context.Background(), "anyuser", []string{"anyrole"}, "anytool")
	if !authorized {
		t.Error("expected authorized to be true")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

// Verify interface compliance.
var _ Authorizer = (*NoopAuthorizer)(nil)
var _ Authorizer = (*mockAuthorizer)(nil)
