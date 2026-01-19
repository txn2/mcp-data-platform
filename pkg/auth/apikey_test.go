package auth

import (
	"context"
	"testing"
)

func TestAPIKeyAuthenticator(t *testing.T) {
	cfg := APIKeyConfig{
		Keys: []APIKey{
			{Key: "test-key-1", Name: "admin", Roles: []string{"admin"}},
			{Key: "test-key-2", Name: "analyst", Roles: []string{"analyst"}},
		},
	}
	auth := NewAPIKeyAuthenticator(cfg)

	t.Run("valid key", func(t *testing.T) {
		ctx := WithToken(context.Background(), "test-key-1")
		userInfo, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if userInfo.AuthType != "apikey" {
			t.Errorf("AuthType = %q, want %q", userInfo.AuthType, "apikey")
		}
		if len(userInfo.Roles) != 1 || userInfo.Roles[0] != "admin" {
			t.Errorf("Roles = %v, want [admin]", userInfo.Roles)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		ctx := WithToken(context.Background(), "invalid-key")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() expected error for invalid key")
		}
	})

	t.Run("no key", func(t *testing.T) {
		ctx := context.Background()
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() expected error for missing key")
		}
	})

	t.Run("AddKey", func(t *testing.T) {
		auth.AddKey(APIKey{Key: "new-key", Name: "new", Roles: []string{"new"}})
		ctx := WithToken(context.Background(), "new-key")
		userInfo, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		if userInfo.Roles[0] != "new" {
			t.Errorf("Roles = %v, want [new]", userInfo.Roles)
		}
	})

	t.Run("RemoveKey", func(t *testing.T) {
		auth.RemoveKey("new-key")
		ctx := WithToken(context.Background(), "new-key")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("Authenticate() expected error after key removal")
		}
	})
}
