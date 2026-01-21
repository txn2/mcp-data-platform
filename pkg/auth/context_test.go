package auth

import (
	"context"
	"testing"
)

func TestUserContext_HasRole(t *testing.T) {
	uc := &UserContext{
		Roles: []string{"analyst", "admin"},
	}

	if !uc.HasRole("analyst") {
		t.Error("HasRole(analyst) = false, want true")
	}
	if !uc.HasRole("admin") {
		t.Error("HasRole(admin) = false, want true")
	}
	if uc.HasRole("executive") {
		t.Error("HasRole(executive) = true, want false")
	}
}

func TestUserContext_HasAnyRole(t *testing.T) {
	uc := &UserContext{
		Roles: []string{"analyst"},
	}

	if !uc.HasAnyRole("admin", "analyst") {
		t.Error("HasAnyRole() = false, want true")
	}
	if uc.HasAnyRole("admin", "executive") {
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
