package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestOAuthJWTAuthenticator_Authenticate(t *testing.T) {
	signingKey := []byte("test-signing-key-at-least-32-bytes-long")
	issuer := "https://oauth.example.com"

	authenticator, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
		Issuer:        issuer,
		SigningKey:    signingKey,
		RoleClaimPath: "realm_access.roles",
		RolePrefix:    "dp_",
	})
	if err != nil {
		t.Fatalf("failed to create authenticator: %v", err)
	}

	t.Run("valid token with roles", func(t *testing.T) {
		// Create a valid JWT
		now := time.Now()
		claims := jwt.MapClaims{
			"iss":   issuer,
			"sub":   "user-123",
			"aud":   "client-id",
			"exp":   now.Add(time.Hour).Unix(),
			"iat":   now.Unix(),
			"nbf":   now.Unix(),
			"scope": "openid profile",
			"claims": map[string]any{
				"email": "user@example.com",
				"realm_access": map[string]any{
					"roles": []any{"dp_analyst", "dp_viewer", "other_role"},
				},
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(signingKey)
		if err != nil {
			t.Fatalf("failed to sign token: %v", err)
		}

		ctx := WithToken(context.Background(), tokenString)
		userInfo, err := authenticator.Authenticate(ctx)
		if err != nil {
			t.Fatalf("authentication failed: %v", err)
		}

		if userInfo.UserID != "user-123" {
			t.Errorf("expected UserID 'user-123', got %q", userInfo.UserID)
		}
		if userInfo.Email != "user@example.com" {
			t.Errorf("expected Email 'user@example.com', got %q", userInfo.Email)
		}
		if userInfo.AuthType != "oauth" {
			t.Errorf("expected AuthType 'oauth', got %q", userInfo.AuthType)
		}
		// Should only have dp_ prefixed roles
		expectedRoles := 2
		if len(userInfo.Roles) != expectedRoles {
			t.Errorf("expected %d roles, got %d: %v", expectedRoles, len(userInfo.Roles), userInfo.Roles)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": issuer,
			"sub": "user-123",
			"exp": now.Add(-time.Hour).Unix(), // Expired
			"iat": now.Add(-2 * time.Hour).Unix(),
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, _ := token.SignedString(signingKey)

		ctx := WithToken(context.Background(), tokenString)
		_, err := authenticator.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for expired token")
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": "https://wrong-issuer.com",
			"sub": "user-123",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, _ := token.SignedString(signingKey)

		ctx := WithToken(context.Background(), tokenString)
		_, err := authenticator.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for wrong issuer")
		}
	})

	t.Run("wrong signing key", func(t *testing.T) {
		wrongKey := []byte("wrong-signing-key-at-least-32-bytes")
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": issuer,
			"sub": "user-123",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, _ := token.SignedString(wrongKey)

		ctx := WithToken(context.Background(), tokenString)
		_, err := authenticator.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for wrong signing key")
		}
	})

	t.Run("no token", func(t *testing.T) {
		_, err := authenticator.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("invalid token format", func(t *testing.T) {
		ctx := WithToken(context.Background(), "not-a-valid-jwt")
		_, err := authenticator.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for invalid token format")
		}
	})

	t.Run("missing sub claim", func(t *testing.T) {
		now := time.Now()
		claims := jwt.MapClaims{
			"iss": issuer,
			// Missing "sub"
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(),
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, _ := token.SignedString(signingKey)

		ctx := WithToken(context.Background(), tokenString)
		_, err := authenticator.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for missing sub claim")
		}
	})
}

func TestNewOAuthJWTAuthenticator_Validation(t *testing.T) {
	t.Run("missing issuer", func(t *testing.T) {
		_, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
			SigningKey: []byte("test-key-at-least-32-bytes-long"),
		})
		if err == nil {
			t.Error("expected error for missing issuer")
		}
	})

	t.Run("missing signing key", func(t *testing.T) {
		_, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
			Issuer: "https://example.com",
		})
		if err == nil {
			t.Error("expected error for missing signing key")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		_, err := NewOAuthJWTAuthenticator(OAuthJWTConfig{
			Issuer:     "https://example.com",
			SigningKey: []byte("test-key-at-least-32-bytes-long"),
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
