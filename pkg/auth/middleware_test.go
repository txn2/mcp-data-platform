package auth

import (
	"context"
	"fmt"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const testAnonymousID = "anonymous"

// mockAuthenticator is a mock for testing.
type mockAuthenticator struct {
	userInfo *middleware.UserInfo
	err      error
}

func (m *mockAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return m.userInfo, m.err
}

func TestChainedAuthenticator(t *testing.T) {
	t.Run("first authenticator succeeds", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			userInfo: &middleware.UserInfo{UserID: "user1", AuthType: "type1"},
		}
		auth2 := &mockAuthenticator{
			userInfo: &middleware.UserInfo{UserID: "user2", AuthType: "type2"},
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{}, auth1, auth2)

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != "user1" {
			t.Errorf("UserID = %q, want 'user1'", userInfo.UserID)
		}
	})

	t.Run("first fails second succeeds", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			err: fmt.Errorf("auth1 failed"),
		}
		auth2 := &mockAuthenticator{
			userInfo: &middleware.UserInfo{UserID: "user2", AuthType: "type2"},
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{}, auth1, auth2)

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != "user2" {
			t.Errorf("UserID = %q, want 'user2'", userInfo.UserID)
		}
	})

	t.Run("all fail returns last error", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			err: fmt.Errorf("auth1 failed"),
		}
		auth2 := &mockAuthenticator{
			err: fmt.Errorf("auth2 failed"),
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{}, auth1, auth2)

		_, err := chained.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error")
		}
		if err.Error() != "auth2 failed" {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("all fail with allow anonymous", func(t *testing.T) {
		auth1 := &mockAuthenticator{
			err: fmt.Errorf("auth1 failed"),
		}

		chained := NewChainedAuthenticator(ChainedAuthConfig{AllowAnonymous: true}, auth1)

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != testAnonymousID {
			t.Errorf("UserID = %q, want 'anonymous'", userInfo.UserID)
		}
		if userInfo.AuthType != testAnonymousID {
			t.Errorf("AuthType = %q", userInfo.AuthType)
		}
	})
}

func TestChainedAuthenticator_EmptyChain(t *testing.T) {
	t.Run("empty chain fails", func(t *testing.T) {
		chained := NewChainedAuthenticator(ChainedAuthConfig{})

		_, err := chained.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error for empty chain")
		}
	})

	t.Run("empty chain with allow anonymous", func(t *testing.T) {
		chained := NewChainedAuthenticator(ChainedAuthConfig{AllowAnonymous: true})

		userInfo, err := chained.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != testAnonymousID {
			t.Errorf("UserID = %q", userInfo.UserID)
		}
	})
}

func TestBearerTokenExtractor(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		extractor := &BearerTokenExtractor{}
		_, err := extractor.Extract(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("token without Bearer prefix", func(t *testing.T) {
		extractor := &BearerTokenExtractor{}
		ctx := WithToken(context.Background(), "mytoken")

		token, err := extractor.Extract(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "mytoken" {
			t.Errorf("token = %q", token)
		}
	})

	t.Run("token with Bearer prefix", func(t *testing.T) {
		extractor := &BearerTokenExtractor{}
		ctx := WithToken(context.Background(), "Bearer mytoken")

		token, err := extractor.Extract(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "mytoken" {
			t.Errorf("token = %q", token)
		}
	})
}

func TestAPIKeyExtractor(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		extractor := &APIKeyExtractor{}
		_, err := extractor.Extract(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("token present", func(t *testing.T) {
		extractor := &APIKeyExtractor{
			HeaderName: "X-API-Key",
		}
		ctx := WithToken(context.Background(), "api-key-123")

		token, err := extractor.Extract(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "api-key-123" {
			t.Errorf("token = %q", token)
		}
	})
}

func TestChainedAuthConfig(t *testing.T) {
	cfg := ChainedAuthConfig{
		AllowAnonymous: true,
	}
	if !cfg.AllowAnonymous {
		t.Error("AllowAnonymous = false")
	}
}

// Verify interface compliance.
var (
	_ middleware.Authenticator = (*mockAuthenticator)(nil)
	_ middleware.Authenticator = (*ChainedAuthenticator)(nil)
	_ TokenExtractor           = (*BearerTokenExtractor)(nil)
	_ TokenExtractor           = (*APIKeyExtractor)(nil)
)
