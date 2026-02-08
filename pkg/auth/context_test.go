package auth

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	testRoleAnalyst   = "analyst"
	testRoleAdmin     = "admin"
	testRoleExecutive = "executive"
)

func TestUserContext_HasRole(t *testing.T) {
	uc := &UserContext{
		Roles: []string{testRoleAnalyst, testRoleAdmin},
	}

	if !uc.HasRole(testRoleAnalyst) {
		t.Error("HasRole(analyst) = false, want true")
	}
	if !uc.HasRole(testRoleAdmin) {
		t.Error("HasRole(admin) = false, want true")
	}
	if uc.HasRole(testRoleExecutive) {
		t.Error("HasRole(executive) = true, want false")
	}
}

func TestUserContext_HasAnyRole(t *testing.T) {
	uc := &UserContext{
		Roles: []string{testRoleAnalyst},
	}

	if !uc.HasAnyRole(testRoleAdmin, testRoleAnalyst) {
		t.Error("HasAnyRole() = false, want true")
	}
	if uc.HasAnyRole(testRoleAdmin, testRoleExecutive) {
		t.Error("HasAnyRole() = true, want false")
	}
}

func TestUserContext_InGroup(t *testing.T) {
	uc := &UserContext{
		Groups: []string{"data-team", "analytics"},
	}

	if !uc.InGroup("data-team") {
		t.Error("InGroup(data-team) = false, want true")
	}
	if uc.InGroup("admin") {
		t.Error("InGroup(admin) = true, want false")
	}
}

func TestWithUserContext(t *testing.T) {
	uc := &UserContext{UserID: "user123"}
	ctx := WithUserContext(context.Background(), uc)

	got := GetUserContext(ctx)
	if got == nil {
		t.Fatal("GetUserContext() returned nil")
	}
	if got.UserID != "user123" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user123")
	}
}

func TestGetUserContext_NotSet(t *testing.T) {
	ctx := context.Background()
	got := GetUserContext(ctx)
	if got != nil {
		t.Error("GetUserContext() expected nil for context without user")
	}
}

func TestWithToken(t *testing.T) {
	ctx := WithToken(context.Background(), "test-token")
	got := GetToken(ctx)
	if got != "test-token" {
		t.Errorf("GetToken() = %q, want %q", got, "test-token")
	}
}

func TestGetToken_NotSet(t *testing.T) {
	ctx := context.Background()
	got := GetToken(ctx)
	if got != "" {
		t.Errorf("GetToken() = %q, want empty string", got)
	}
}

func TestTokenInterop_AuthAndMiddleware(t *testing.T) {
	// Token set via auth.WithToken must be readable via middleware.GetToken,
	// ensuring the SSE HTTP middleware and MCP middleware share the same key.
	ctx := WithToken(context.Background(), "cross-package-token")

	if got := middleware.GetToken(ctx); got != "cross-package-token" {
		t.Errorf("middleware.GetToken() = %q, want %q", got, "cross-package-token")
	}

	// And the reverse: token set via middleware must be readable via auth.
	ctx2 := middleware.WithToken(context.Background(), "middleware-token")
	if got := GetToken(ctx2); got != "middleware-token" {
		t.Errorf("auth.GetToken() = %q, want %q", got, "middleware-token")
	}
}
