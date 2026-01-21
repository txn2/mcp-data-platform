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

func TestOIDCAuthenticator_validateClaims(t *testing.T) {
	t.Run("invalid issuer", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer: "https://issuer.example.com",
			// Note: not skipping issuer verification
		})
		claims := map[string]any{
			"sub": "user123",
			"iss": "https://wrong-issuer.com",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for invalid issuer")
		}
	})

	t.Run("invalid audience", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                 "https://issuer.example.com",
			Audience:               "my-audience",
			SkipIssuerVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
			"aud": "wrong-audience",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for invalid audience")
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
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for expired token")
		}
	})

	t.Run("valid claims", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                 "https://issuer.example.com",
			SkipIssuerVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing exp is ok", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                 "https://issuer.example.com",
			SkipIssuerVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
		}
		err := auth.validateClaims(claims)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestOIDCAuthenticator_parseAndValidateToken(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                 "https://issuer.example.com",
		SkipIssuerVerification: true,
	})

	t.Run("only two parts", func(t *testing.T) {
		_, err := auth.parseAndValidateToken("header.payload")
		if err == nil {
			t.Error("expected error for JWT with only two parts")
		}
	})

	t.Run("invalid base64 payload", func(t *testing.T) {
		_, err := auth.parseAndValidateToken("header.!!!invalid-base64!!!.sig")
		if err == nil {
			t.Error("expected error for invalid base64")
		}
	})

	t.Run("invalid JSON payload", func(t *testing.T) {
		payload := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
		_, err := auth.parseAndValidateToken("header." + payload + ".sig")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestOIDCAuthenticator_FetchJWKS_InvalidDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`invalid-json`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer: server.URL,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for invalid discovery JSON")
	}
}

func TestOIDCAuthenticator_FetchJWKS_MissingJWKSURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`)) // No jwks_uri
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer: server.URL,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for missing jwks_uri")
	}
}

func TestOIDCAuthenticator_FetchJWKS_JWKSFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			http.Error(w, "Internal Server Error", 500)
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer: server.URL,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for JWKS fetch failure")
	}
}

func TestOIDCAuthenticator_Authenticate_WithRoles(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                 "https://issuer.example.com",
		SkipIssuerVerification: true,
		RoleClaimPath:          "roles",
		RolePrefix:             "app_",
	})

	claims := map[string]any{
		"sub":   "user123",
		"roles": []any{"app_admin", "other_role", "app_user"},
		"exp":   float64(time.Now().Add(time.Hour).Unix()),
	}
	token := createTestJWT(claims)

	ctx := WithToken(context.Background(), token)
	userInfo, err := auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should filter to only app_ prefixed roles
	if len(userInfo.Roles) != 2 {
		t.Errorf("expected 2 filtered roles, got %d: %v", len(userInfo.Roles), userInfo.Roles)
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidJWKSJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`invalid-json`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer: server.URL,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for invalid JWKS JSON")
	}
}

func TestOIDCAuthenticator_checkAudience_NoAudienceRequired(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer: "https://issuer.example.com",
		// No audience configured - empty string
	})

	// When audience in config is empty, check only passes if aud in claims is also empty
	claims := map[string]any{
		"aud": "",
	}
	if !auth.checkAudience(claims) {
		t.Error("expected audience check to pass when both are empty")
	}

	// Non-empty aud should fail
	claimsNonEmpty := map[string]any{
		"aud": "some-audience",
	}
	if auth.checkAudience(claimsNonEmpty) {
		t.Error("expected audience check to fail when aud is set but config is empty")
	}
}

func TestOIDCAuthenticator_FetchJWKS_JWKSURIEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": ""}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer: server.URL,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for empty jwks_uri")
	}
}
