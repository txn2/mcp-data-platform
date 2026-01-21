package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewOIDCAuthenticator(t *testing.T) {
	t.Run("requires issuer", func(t *testing.T) {
		_, err := NewOIDCAuthenticator(OIDCConfig{})
		if err == nil {
			t.Error("expected error for missing issuer")
		}
	})

	t.Run("creates authenticator", func(t *testing.T) {
		auth, err := NewOIDCAuthenticator(OIDCConfig{
			Issuer:        "https://issuer.example.com",
			ClientID:      "client-id",
			Audience:      "audience",
			RoleClaimPath: "roles",
			RolePrefix:    "app_",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if auth == nil {
			t.Error("expected non-nil authenticator")
		}
	})
}

func TestOIDCAuthenticator_Authenticate(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer: "https://issuer.example.com",
		})

		_, err := auth.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("valid token", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                 "https://issuer.example.com",
			SkipIssuerVerification: true,
		})

		// Create a valid JWT payload
		claims := map[string]any{
			"sub":   "user123",
			"email": "user@example.com",
			"name":  "Test User",
			"exp":   float64(time.Now().Add(time.Hour).Unix()),
		}
		token := createTestJWT(claims)

		ctx := WithToken(context.Background(), token)
		userInfo, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if userInfo.UserID != "user123" {
			t.Errorf("UserID = %q, want 'user123'", userInfo.UserID)
		}
		if userInfo.Email != "user@example.com" {
			t.Errorf("Email = %q", userInfo.Email)
		}
		if userInfo.AuthType != "oidc" {
			t.Errorf("AuthType = %q", userInfo.AuthType)
		}
	})

	t.Run("invalid JWT format", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer: "https://issuer.example.com",
		})

		ctx := WithToken(context.Background(), "not-a-jwt")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for invalid JWT format")
		}
	})

	t.Run("invalid issuer", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer: "https://issuer.example.com",
		})

		claims := map[string]any{
			"sub": "user123",
			"iss": "https://wrong-issuer.com",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		token := createTestJWT(claims)

		ctx := WithToken(context.Background(), token)
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for wrong issuer")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                 "https://issuer.example.com",
			SkipIssuerVerification: true,
		})

		claims := map[string]any{
			"sub": "user123",
			"exp": float64(time.Now().Add(-time.Hour).Unix()),
		}
		token := createTestJWT(claims)

		ctx := WithToken(context.Background(), token)
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for expired token")
		}
	})
}

func TestOIDCAuthenticator_checkAudience(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:   "https://issuer.example.com",
		Audience: "my-audience",
	})

	t.Run("string audience matches", func(t *testing.T) {
		claims := map[string]any{
			"aud": "my-audience",
		}
		if !auth.checkAudience(claims) {
			t.Error("expected audience to match")
		}
	})

	t.Run("string audience does not match", func(t *testing.T) {
		claims := map[string]any{
			"aud": "wrong-audience",
		}
		if auth.checkAudience(claims) {
			t.Error("expected audience to not match")
		}
	})

	t.Run("array audience matches", func(t *testing.T) {
		claims := map[string]any{
			"aud": []any{"other", "my-audience"},
		}
		if !auth.checkAudience(claims) {
			t.Error("expected audience to match in array")
		}
	})

	t.Run("array audience does not match", func(t *testing.T) {
		claims := map[string]any{
			"aud": []any{"other", "another"},
		}
		if auth.checkAudience(claims) {
			t.Error("expected audience to not match in array")
		}
	})

	t.Run("missing audience", func(t *testing.T) {
		claims := map[string]any{}
		if auth.checkAudience(claims) {
			t.Error("expected audience check to fail for missing aud")
		}
	})
}

func TestOIDCAuthenticator_FetchJWKS(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		// Create mock OIDC server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
			case "/jwks":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"keys": []}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer: server.URL,
		})

		err := auth.FetchJWKS(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if auth.jwks == nil {
			t.Error("jwks should be cached")
		}
	})

	t.Run("discovery not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer server.Close()

		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer: server.URL,
		})

		err := auth.FetchJWKS(context.Background())
		if err == nil {
			t.Error("expected error for 404 response")
		}
	})
}

func TestOIDCConfig(t *testing.T) {
	cfg := OIDCConfig{
		Issuer:                 "https://issuer.example.com",
		ClientID:               "client-id",
		Audience:               "audience",
		RoleClaimPath:          "realm_access.roles",
		RolePrefix:             "app_",
		SkipIssuerVerification: true,
	}

	if cfg.Issuer != "https://issuer.example.com" {
		t.Errorf("Issuer = %q", cfg.Issuer)
	}
	if cfg.ClientID != "client-id" {
		t.Errorf("ClientID = %q", cfg.ClientID)
	}
	if !cfg.SkipIssuerVerification {
		t.Error("SkipIssuerVerification = false")
	}
}

// createTestJWT creates a test JWT token (not cryptographically signed).
func createTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + payloadB64 + ".signature"
}
