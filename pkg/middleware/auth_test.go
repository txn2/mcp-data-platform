package middleware

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockAuthenticator implements Authenticator for testing.
type mockAuthenticator struct {
	authenticateFunc func(ctx context.Context) (*UserInfo, error)
}

func (m *mockAuthenticator) Authenticate(ctx context.Context) (*UserInfo, error) {
	if m.authenticateFunc != nil {
		return m.authenticateFunc(ctx)
	}
	return nil, nil
}

func TestNoopAuthenticator(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		auth := &NoopAuthenticator{}
		info, err := auth.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UserID != "anonymous" {
			t.Errorf("expected UserID 'anonymous', got %q", info.UserID)
		}
		if info.Email != "anonymous@localhost" {
			t.Errorf("expected Email 'anonymous@localhost', got %q", info.Email)
		}
		if info.AuthType != "noop" {
			t.Errorf("expected AuthType 'noop', got %q", info.AuthType)
		}
	})

	t.Run("custom values", func(t *testing.T) {
		auth := &NoopAuthenticator{
			DefaultUserID: "testuser",
			DefaultRoles:  []string{"viewer", "editor"},
		}
		info, err := auth.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UserID != "testuser" {
			t.Errorf("expected UserID 'testuser', got %q", info.UserID)
		}
		if len(info.Roles) != 2 {
			t.Errorf("expected 2 roles, got %d", len(info.Roles))
		}
	})
}

func TestNewToolResultError(t *testing.T) {
	result := NewToolResultError("test error")
	if !result.IsError {
		t.Error("expected IsError to be true")
	}
	if len(result.Content) != 1 {
		t.Fatal("expected 1 content item")
	}
	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if textContent.Text != "test error" {
		t.Errorf("expected 'test error', got %q", textContent.Text)
	}
}

func TestNewToolResultText(t *testing.T) {
	result := NewToolResultText("test text")
	if result.IsError {
		t.Error("expected IsError to be false")
	}
	if len(result.Content) != 1 {
		t.Fatal("expected 1 content item")
	}
	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if textContent.Text != "test text" {
		t.Errorf("expected 'test text', got %q", textContent.Text)
	}
}

func TestUserInfo(t *testing.T) {
	info := UserInfo{
		UserID:   "user123",
		Email:    "user@example.com",
		Claims:   map[string]any{"role": "admin"},
		Roles:    []string{"admin", "viewer"},
		AuthType: "oidc",
	}

	if info.UserID != "user123" {
		t.Errorf("unexpected UserID: %s", info.UserID)
	}
	if info.Email != "user@example.com" {
		t.Errorf("unexpected Email: %s", info.Email)
	}
	if len(info.Roles) != 2 {
		t.Errorf("unexpected Roles count: %d", len(info.Roles))
	}
}

// Verify interface compliance.
var _ Authenticator = (*NoopAuthenticator)(nil)
var _ Authenticator = (*mockAuthenticator)(nil)
