package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestNewOIDCAuthenticator(t *testing.T) {
	t.Run("requires issuer", func(t *testing.T) {
		_, err := NewOIDCAuthenticator(OIDCConfig{
			SkipSignatureVerification: true,
		})
		if err == nil {
			t.Error("expected error for missing issuer")
		}
	})

	t.Run("creates authenticator with skip signature verification", func(t *testing.T) {
		auth, err := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			ClientID:                  "client-id",
			Audience:                  "audience",
			RoleClaimPath:             "roles",
			RolePrefix:                "app_",
			SkipSignatureVerification: true,
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
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})

		_, err := auth.Authenticate(context.Background())
		if err == nil {
			t.Error("expected error for missing token")
		}
	})

	t.Run("valid token", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
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
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})

		ctx := WithToken(context.Background(), "not-a-jwt")
		_, err := auth.Authenticate(ctx)
		if err == nil {
			t.Error("expected error for invalid JWT format")
		}
	})

	t.Run("invalid issuer", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
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
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
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
		Issuer:                    "https://issuer.example.com",
		Audience:                  "my-audience",
		SkipSignatureVerification: true,
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
	// Test RSA public key components (base64url encoded)
	// These are example values for testing - not a real key
	testN := "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw"
	testE := "AQAB"
	testKid := "test-key-1"

	t.Run("successful fetch", func(t *testing.T) {
		// Create mock OIDC server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
			case "/jwks":
				w.Header().Set("Content-Type", "application/json")
				jwks := fmt.Sprintf(`{"keys": [{"kty": "RSA", "kid": "%s", "use": "sig", "n": "%s", "e": "%s"}]}`, testKid, testN, testE)
				_, _ = w.Write([]byte(jwks))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		// Create authenticator with skip signature verification to avoid JWKS fetch on startup
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    server.URL,
			SkipSignatureVerification: true,
		})

		// Now manually fetch JWKS to test the method
		err := auth.FetchJWKS(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if auth.jwks == nil {
			t.Error("jwks should be cached")
		}
		if len(auth.jwks.keys) != 1 {
			t.Errorf("expected 1 key, got %d", len(auth.jwks.keys))
		}
		if _, ok := auth.jwks.keys[testKid]; !ok {
			t.Errorf("expected key with kid %q", testKid)
		}
	})

	t.Run("discovery not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer server.Close()

		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    server.URL,
			SkipSignatureVerification: true,
		})

		err := auth.FetchJWKS(context.Background())
		if err == nil {
			t.Error("expected error for 404 response")
		}
	})

	t.Run("no valid RSA keys", func(t *testing.T) {
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
			Issuer:                    server.URL,
			SkipSignatureVerification: true,
		})

		err := auth.FetchJWKS(context.Background())
		if err == nil {
			t.Error("expected error for empty keys")
		}
	})
}

func TestOIDCConfig(t *testing.T) {
	cfg := OIDCConfig{
		Issuer:                    "https://issuer.example.com",
		ClientID:                  "client-id",
		Audience:                  "audience",
		RoleClaimPath:             "realm_access.roles",
		RolePrefix:                "app_",
		SkipIssuerVerification:    true,
		SkipSignatureVerification: true,
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
	if !cfg.SkipSignatureVerification {
		t.Error("SkipSignatureVerification = false")
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
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
			// Note: not skipping issuer verification
		})
		claims := map[string]any{
			"sub": "user123",
			"iss": "https://wrong-issuer.com",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err == nil || err.Error() != "invalid issuer" {
			t.Errorf("expected 'invalid issuer' error, got: %v", err)
		}
	})

	t.Run("invalid audience", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			Audience:                  "my-audience",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
			"aud": "wrong-audience",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err == nil || err.Error() != "invalid audience" {
			t.Errorf("expected 'invalid audience' error, got: %v", err)
		}
	})

	t.Run("expired token outside skew", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
			"exp": float64(time.Now().Add(-time.Hour).Unix()), // expired 1 hour ago, well beyond 30s skew
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for expired token")
		}
	})

	t.Run("valid claims", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
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

	t.Run("missing exp is rejected", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for missing exp claim")
		}
	})

	t.Run("missing sub is rejected", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for missing sub claim")
		}
	})

	t.Run("empty sub is rejected", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": "",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for empty sub claim")
		}
	})

	t.Run("nbf not yet valid", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": "user123",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
			"nbf": float64(time.Now().Add(time.Hour).Unix()), // not valid for an hour
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for token not yet valid")
		}
	})

	t.Run("token too old by iat", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
			MaxTokenAge:               1 * time.Hour,
		})
		claims := map[string]any{
			"sub": "user123",
			"exp": float64(time.Now().Add(time.Hour).Unix()),
			"iat": float64(time.Now().Add(-2 * time.Hour).Unix()), // issued 2 hours ago
		}
		err := auth.validateClaims(claims)
		if err == nil {
			t.Error("expected error for token too old")
		}
	})

	t.Run("clock skew allows slightly expired token", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
			ClockSkewSeconds:          60,
		})
		claims := map[string]any{
			"sub": "user123",
			"exp": float64(time.Now().Add(-10 * time.Second).Unix()), // expired 10 seconds ago
		}
		err := auth.validateClaims(claims)
		if err != nil {
			t.Errorf("expected clock skew to allow slightly expired token: %v", err)
		}
	})
}

func TestOIDCAuthenticator_parseAndValidateToken(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    "https://issuer.example.com",
		SkipIssuerVerification:    true,
		SkipSignatureVerification: true,
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
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
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
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
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
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for JWKS fetch failure")
	}
}

func TestOIDCAuthenticator_Authenticate_WithRoles(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    "https://issuer.example.com",
		SkipIssuerVerification:    true,
		SkipSignatureVerification: true,
		RoleClaimPath:             "roles",
		RolePrefix:                "app_",
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
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for invalid JWKS JSON")
	}
}

func TestOIDCAuthenticator_checkAudience_NoAudienceRequired(t *testing.T) {
	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    "https://issuer.example.com",
		SkipSignatureVerification: true,
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
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for empty jwks_uri")
	}
}

func TestOIDCAuthenticator_getPublicKey(t *testing.T) {
	t.Run("JWKS not loaded", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})
		// jwks is nil by default

		_, err := auth.getPublicKey("test-kid")
		if err == nil {
			t.Error("expected error for nil JWKS")
		}
		if err.Error() != "JWKS not loaded" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("JWKS cache expired", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})
		// Set expired cache
		auth.jwks = &jwksCache{
			keys:      make(map[string]*rsa.PublicKey),
			expiresAt: time.Now().Add(-time.Hour), // expired an hour ago
		}

		_, err := auth.getPublicKey("test-kid")
		if err == nil {
			t.Error("expected error for expired cache")
		}
		if err.Error() != "JWKS cache expired" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("key not found", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})
		// Set valid cache but no keys
		auth.jwks = &jwksCache{
			keys:      make(map[string]*rsa.PublicKey),
			expiresAt: time.Now().Add(time.Hour),
		}

		_, err := auth.getPublicKey("missing-kid")
		if err == nil {
			t.Error("expected error for missing key")
		}
		if !strings.Contains(err.Error(), "key not found") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("key found", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})
		// Set valid cache with a key
		testKey := &rsa.PublicKey{N: big.NewInt(12345), E: 65537}
		auth.jwks = &jwksCache{
			keys:      map[string]*rsa.PublicKey{"test-kid": testKey},
			expiresAt: time.Now().Add(time.Hour),
		}

		key, err := auth.getPublicKey("test-kid")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if key != testKey {
			t.Error("returned key does not match expected key")
		}
	})
}

func TestOIDCAuthenticator_RefreshJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Need at least one valid RSA key
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "RSA",
						"kid": "test-key",
						"use": "sig",
						"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
						"e": "AQAB"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	// RefreshJWKS should call FetchJWKS
	err := auth.RefreshJWKS(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify JWKS was loaded
	if auth.jwks == nil {
		t.Error("expected JWKS to be loaded after RefreshJWKS")
	}
}

func TestOIDCAuthenticator_parseAndValidateToken_SignatureVerification(t *testing.T) {
	// Helper to create authenticator with mock JWKS server
	createAuthWithMockJWKS := func(t *testing.T) *OIDCAuthenticator {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/openid-configuration":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
			case "/jwks":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"keys": [
						{
							"kty": "RSA",
							"kid": "test-key",
							"use": "sig",
							"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
							"e": "AQAB"
						}
					]
				}`))
			}
		}))
		t.Cleanup(server.Close)

		auth, err := NewOIDCAuthenticator(OIDCConfig{
			Issuer: server.URL,
		})
		if err != nil {
			t.Fatalf("failed to create authenticator: %v", err)
		}
		return auth
	}

	t.Run("missing kid header", func(t *testing.T) {
		auth := createAuthWithMockJWKS(t)

		// Create a token without kid in header
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user","exp":9999999999}`))
		sig := base64.RawURLEncoding.EncodeToString([]byte("fakesignature"))
		token := header + "." + payload + "." + sig

		_, err := auth.parseAndValidateToken(token)
		if err == nil {
			t.Fatal("expected error for token without kid")
		}
		// The error should mention "kid"
		if !strings.Contains(err.Error(), "kid") {
			t.Errorf("expected error about kid, got: %v", err)
		}
	})

	t.Run("unexpected signing method", func(t *testing.T) {
		auth := createAuthWithMockJWKS(t)

		// Create a token with HS256 (HMAC, not RSA)
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"test"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user","exp":9999999999}`))
		sig := base64.RawURLEncoding.EncodeToString([]byte("fakesignature"))
		token := header + "." + payload + "." + sig

		_, err := auth.parseAndValidateToken(token)
		if err == nil {
			t.Fatal("expected error for non-RSA signing method")
		}
		if !strings.Contains(err.Error(), "unexpected signing method") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("key not found in JWKS", func(t *testing.T) {
		auth := createAuthWithMockJWKS(t)

		// Create a token with RS256 and a kid that doesn't exist in the JWKS
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"nonexistent"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user","exp":9999999999}`))
		sig := base64.RawURLEncoding.EncodeToString([]byte("fakesignature"))
		token := header + "." + payload + "." + sig

		_, err := auth.parseAndValidateToken(token)
		if err == nil {
			t.Fatal("expected error for key not found")
		}
		if !strings.Contains(err.Error(), "key not found") {
			t.Errorf("expected 'key not found' error, got: %v", err)
		}
	})
}

func TestOIDCAuthenticator_parseAndValidateToken_ValidSignature(t *testing.T) {
	// Generate RSA key pair for testing
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Encode public key components for JWKS
	nBytes := privateKey.PublicKey.N.Bytes()
	nBase64 := base64.RawURLEncoding.EncodeToString(nBytes)

	eBytes := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
	eBase64 := base64.RawURLEncoding.EncodeToString(eBytes)

	// Create test server serving JWKS
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"keys": [
					{
						"kty": "RSA",
						"kid": "test-key-1",
						"use": "sig",
						"n": "%s",
						"e": "%s"
					}
				]
			}`, nBase64, eBase64)))
		}
	}))
	defer server.Close()

	auth, err := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                 server.URL,
		SkipIssuerVerification: true,
	})
	if err != nil {
		t.Fatalf("failed to create authenticator: %v", err)
	}

	// Create and sign a valid JWT
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "user123",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
		"iss": server.URL,
	})
	token.Header["kid"] = "test-key-1"

	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	// Parse and validate the token
	claims, err := auth.parseAndValidateToken(signedToken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify claims were extracted
	if claims["sub"] != "user123" {
		t.Errorf("expected sub='user123', got %v", claims["sub"])
	}
}

func TestOIDCAuthenticator_parseAndValidateToken_InvalidSignature(t *testing.T) {
	// Generate two different RSA key pairs
	signingKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwksKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	// Encode JWKS key (different from signing key)
	nBytes := jwksKey.PublicKey.N.Bytes()
	nBase64 := base64.RawURLEncoding.EncodeToString(nBytes)
	eBytes := big.NewInt(int64(jwksKey.PublicKey.E)).Bytes()
	eBase64 := base64.RawURLEncoding.EncodeToString(eBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"keys": [{"kty": "RSA", "kid": "test-key", "use": "sig", "n": "%s", "e": "%s"}]
			}`, nBase64, eBase64)))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                 server.URL,
		SkipIssuerVerification: true,
	})

	// Sign with the wrong key
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "user",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	token.Header["kid"] = "test-key"
	signedToken, _ := token.SignedString(signingKey) // Signed with wrong key

	_, err := auth.parseAndValidateToken(signedToken)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestOIDCAuthenticator_FetchJWKS_DiscoveryFetchError(t *testing.T) {
	// Create a server that returns HTTP error for discovery endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Error("expected error for discovery fetch failure")
	}
}

func TestOIDCAuthenticator_FetchJWKS_ValidKeys(t *testing.T) {
	// Test with a valid RSA key (using base64url-encoded n and e)
	// This is a minimal valid RSA public key representation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Valid JWKS with a minimal RSA key
			// n is a base64url-encoded 256-byte number (2048-bit key)
			// e is base64url-encoded 65537 (AQAB)
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "RSA",
						"kid": "test-key-1",
						"use": "sig",
						"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
						"e": "AQAB"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	err := auth.FetchJWKS(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the key was loaded
	if auth.jwks == nil {
		t.Fatal("expected JWKS to be loaded")
	}
	if len(auth.jwks.keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(auth.jwks.keys))
	}
	if _, ok := auth.jwks.keys["test-key-1"]; !ok {
		t.Error("expected key with kid 'test-key-1'")
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidKeyType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Key with non-RSA type (EC)
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "EC",
						"kid": "ec-key-1",
						"use": "sig"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	// Should error because no valid RSA signing keys are found
	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Fatal("expected error for JWKS with no valid RSA signing keys")
	}
	if !strings.Contains(err.Error(), "no valid RSA signing keys") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_KeyWithEncUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Key with use="enc" (encryption, not signing)
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "RSA",
						"kid": "enc-key-1",
						"use": "enc",
						"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
						"e": "AQAB"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	// Should error because encryption keys are not signing keys
	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Fatal("expected error for JWKS with only encryption keys")
	}
	if !strings.Contains(err.Error(), "no valid RSA signing keys") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidModulus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Key with invalid base64 modulus
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "RSA",
						"kid": "bad-key-1",
						"use": "sig",
						"n": "!!!invalid-base64!!!",
						"e": "AQAB"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	// Should error because invalid key is skipped, leaving no valid keys
	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Fatal("expected error for JWKS with only invalid keys")
	}
	if !strings.Contains(err.Error(), "no valid RSA signing keys") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidExponent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Key with invalid base64 exponent
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "RSA",
						"kid": "bad-exp-key",
						"use": "sig",
						"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
						"e": "!!!invalid!!!"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	// Should error because invalid key is skipped, leaving no valid keys
	err := auth.FetchJWKS(context.Background())
	if err == nil {
		t.Fatal("expected error for JWKS with only invalid keys")
	}
	if !strings.Contains(err.Error(), "no valid RSA signing keys") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_MissingKid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			// Key without kid - still a valid RSA signing key
			_, _ = w.Write([]byte(`{
				"keys": [
					{
						"kty": "RSA",
						"use": "sig",
						"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
						"e": "AQAB"
					}
				]
			}`))
		}
	}))
	defer server.Close()

	auth, _ := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                    server.URL,
		SkipSignatureVerification: true,
	})

	// Key without kid is still a valid RSA signing key - stored with empty string key
	err := auth.FetchJWKS(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Key should be loaded with empty string as the kid
	if len(auth.jwks.keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(auth.jwks.keys))
	}
	// The key is stored with empty string kid
	if _, ok := auth.jwks.keys[""]; !ok {
		t.Error("expected key to be stored with empty string kid")
	}
}
