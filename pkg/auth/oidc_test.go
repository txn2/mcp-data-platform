package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

const (
	testUserID        = "user123"
	oidcDiscoveryPath = "/.well-known/openid-configuration"
	jwksPath          = "/jwks"

	rsaKeyBits        = 2048
	errUnexpected     = "unexpected error: %v"
	errUnexpectedMsg  = "unexpected error message: %v"
	errNoValidRSAKeys = "no valid RSA signing keys"
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
			t.Fatalf(errUnexpected, err)
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
			"sub":   testUserID,
			"email": "user@example.com",
			"name":  "Test User",
			"exp":   float64(time.Now().Add(time.Hour).Unix()),
		}
		token := createTestJWT(claims)

		ctx := WithToken(context.Background(), token)
		userInfo, err := auth.Authenticate(ctx)
		if err != nil {
			t.Fatalf(errUnexpected, err)
		}
		if userInfo.UserID != testUserID {
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

	t.Run("api-key-shaped credential returns ErrNotAJWT sentinel", func(t *testing.T) {
		// Same chain-fallthrough contract as the OAuth JWT
		// authenticator: a zero-dot credential (API key) must surface
		// as the sentinel so ChainedAuthenticator can advance silently.
		// Verified in skip-signature mode AND in verifying mode so the
		// shape-check short-circuit runs before either branch.
		t.Run("skip signature mode", func(t *testing.T) {
			auth, _ := NewOIDCAuthenticator(OIDCConfig{
				Issuer:                    "https://issuer.example.com",
				SkipSignatureVerification: true,
			})
			ctx := WithToken(context.Background(), "nifi-etl")
			_, err := auth.Authenticate(ctx)
			if !errors.Is(err, ErrNotAJWT) {
				t.Errorf("err = %v, want ErrNotAJWT", err)
			}
		})
		t.Run("verifying mode", func(t *testing.T) {
			auth, _ := NewOIDCAuthenticator(OIDCConfig{
				Issuer: "https://issuer.example.com",
			})
			ctx := WithToken(context.Background(), "nifi-etl")
			_, err := auth.Authenticate(ctx)
			if !errors.Is(err, ErrNotAJWT) {
				t.Errorf("err = %v, want ErrNotAJWT", err)
			}
		})
	})

	t.Run("invalid issuer", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipSignatureVerification: true,
		})

		claims := map[string]any{
			"sub": testUserID,
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
			"sub": testUserID,
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
			case oidcDiscoveryPath:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
			case jwksPath:
				w.Header().Set("Content-Type", "application/json")
				jwks := fmt.Sprintf(`{"keys": [{"kty": "RSA", "kid": "%s", "use": "sig", "n": "%s", "e": "%s"}]}`, testKid, testN, testE) //nolint:gocritic // JSON template requires literal quotes, not %q
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
			t.Fatalf(errUnexpected, err)
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
}

func TestOIDCAuthenticator_FetchJWKS_NoValidRSAKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
			"sub": testUserID,
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
			"sub": testUserID,
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
			"sub": testUserID,
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
			"sub": testUserID,
			"exp": float64(time.Now().Add(time.Hour).Unix()),
		}
		err := auth.validateClaims(claims)
		if err != nil {
			t.Errorf(errUnexpected, err)
		}
	})

	t.Run("missing exp is rejected", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": testUserID,
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
}

func TestOIDCAuthenticator_validateClaims_TimeBased(t *testing.T) {
	t.Run("nbf not yet valid", func(t *testing.T) {
		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    "https://issuer.example.com",
			SkipIssuerVerification:    true,
			SkipSignatureVerification: true,
		})
		claims := map[string]any{
			"sub": testUserID,
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
			"sub": testUserID,
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
			"sub": testUserID,
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
		_, err := auth.parseAndValidateToken(context.Background(), "header.payload")
		if err == nil {
			t.Error("expected error for JWT with only two parts")
		}
	})

	t.Run("invalid base64 payload", func(t *testing.T) {
		_, err := auth.parseAndValidateToken(context.Background(), "header.!!!invalid-base64!!!.sig")
		if err == nil {
			t.Error("expected error for invalid base64")
		}
	})

	t.Run("invalid JSON payload", func(t *testing.T) {
		payload := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
		_, err := auth.parseAndValidateToken(context.Background(), "header."+payload+".sig")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestOIDCAuthenticator_FetchJWKS_InvalidDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == oidcDiscoveryPath {
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
		if r.URL.Path == oidcDiscoveryPath {
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
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
		"sub":   testUserID,
		"roles": []any{"app_admin", "other_role", "app_user"},
		"exp":   float64(time.Now().Add(time.Hour).Unix()),
	}
	token := createTestJWT(claims)

	ctx := WithToken(context.Background(), token)
	userInfo, err := auth.Authenticate(ctx)
	if err != nil {
		t.Fatalf(errUnexpected, err)
	}

	// Should filter to only app_ prefixed roles
	if len(userInfo.Roles) != 2 {
		t.Errorf("expected 2 filtered roles, got %d: %v", len(userInfo.Roles), userInfo.Roles)
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidJWKSJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
		if r.URL.Path == oidcDiscoveryPath {
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
	// brokenIssuer serves a discovery endpoint that always fails, so an
	// on-demand refresh triggered by a cache miss fails deterministically
	// without touching a real network.
	brokenIssuer := func(t *testing.T) *OIDCAuthenticator {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusInternalServerError)
		}))
		t.Cleanup(server.Close)

		auth, _ := NewOIDCAuthenticator(OIDCConfig{
			Issuer:                    server.URL,
			SkipSignatureVerification: true,
		})
		return auth
	}

	t.Run("nil cache triggers refresh and fails closed", func(t *testing.T) {
		auth := brokenIssuer(t)
		// jwks is nil by default; the refresh attempt fails.

		_, err := auth.getPublicKey(context.Background(), "test-kid")
		if err == nil {
			t.Fatal("expected error for nil JWKS with failing refresh")
		}
		if !strings.Contains(err.Error(), "refreshing jwks") {
			t.Errorf(errUnexpected, err)
		}
		// An unreachable IdP with no usable cache is a transient failure, so the
		// HTTP gate can fail open rather than drop a possibly-valid client.
		if !errors.Is(err, middleware.ErrValidationUnavailable) {
			t.Errorf("error = %v, want it to wrap middleware.ErrValidationUnavailable", err)
		}
	})

	t.Run("expired cache triggers refresh and fails closed", func(t *testing.T) {
		auth := brokenIssuer(t)
		auth.jwks = &jwksCache{
			keys:      make(map[string]*rsa.PublicKey),
			expiresAt: time.Now().Add(-time.Hour), // expired an hour ago
		}

		_, err := auth.getPublicKey(context.Background(), "test-kid")
		if err == nil {
			t.Fatal("expected error for expired cache with failing refresh")
		}
		if !strings.Contains(err.Error(), "refreshing jwks") {
			t.Errorf(errUnexpected, err)
		}
		if !errors.Is(err, middleware.ErrValidationUnavailable) {
			t.Errorf("error = %v, want it to wrap middleware.ErrValidationUnavailable", err)
		}
	})

	t.Run("unknown kid on valid cache returns key not found", func(t *testing.T) {
		auth := brokenIssuer(t)
		// Valid cache without the requested kid. The refresh attempt fails but
		// the still-valid cache means the caller reports key-not-found.
		auth.jwks = &jwksCache{
			keys:      make(map[string]*rsa.PublicKey),
			expiresAt: time.Now().Add(time.Hour),
		}

		_, err := auth.getPublicKey(context.Background(), "missing-kid")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
		if !strings.Contains(err.Error(), "key not found") {
			t.Errorf(errUnexpected, err)
		}
		// A fresh cache that simply lacks the kid is a DEFINITIVE miss, not a
		// transient failure: the gate must fail closed (401), not fail open.
		if errors.Is(err, middleware.ErrValidationUnavailable) {
			t.Errorf("error = %v, must NOT wrap middleware.ErrValidationUnavailable (definitive miss)", err)
		}
	})

	t.Run("key found on valid cache without refresh", func(t *testing.T) {
		auth := brokenIssuer(t)
		// Set valid cache with a key; the fast path returns it with no refresh.
		testKey := &rsa.PublicKey{N: big.NewInt(12345), E: 65537}
		auth.jwks = &jwksCache{
			keys:      map[string]*rsa.PublicKey{"test-kid": testKey},
			expiresAt: time.Now().Add(time.Hour),
		}

		key, err := auth.getPublicKey(context.Background(), "test-kid")
		if err != nil {
			t.Fatalf(errUnexpected, err)
		}
		if key != testKey {
			t.Error("returned key does not match expected key")
		}
	})
}

func TestOIDCAuthenticator_RefreshJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
		t.Fatalf(errUnexpected, err)
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
			case oidcDiscoveryPath:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
			case jwksPath:
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

		_, err := auth.parseAndValidateToken(context.Background(), token)
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

		_, err := auth.parseAndValidateToken(context.Background(), token)
		if err == nil {
			t.Fatal("expected error for non-RSA signing method")
		}
		if !strings.Contains(err.Error(), "unexpected signing method") {
			t.Errorf(errUnexpectedMsg, err)
		}
	})

	t.Run("key not found in JWKS", func(t *testing.T) {
		auth := createAuthWithMockJWKS(t)

		// Create a token with RS256 and a kid that doesn't exist in the JWKS
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"nonexistent"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user","exp":9999999999}`))
		sig := base64.RawURLEncoding.EncodeToString([]byte("fakesignature"))
		token := header + "." + payload + "." + sig

		_, err := auth.parseAndValidateToken(context.Background(), token)
		if err == nil {
			t.Fatal("expected error for key not found")
		}
		if !strings.Contains(err.Error(), "key not found") {
			t.Errorf("expected 'key not found' error, got: %v", err)
		}
	})

	t.Run("transient JWKS failure propagates middleware.ErrValidationUnavailable", func(t *testing.T) {
		auth := createAuthWithMockJWKS(t)
		// Expire the cache and make the IdP unreachable, so the on-demand refresh
		// during validation fails and the cache cannot be renewed. This proves the
		// transient sentinel survives jwt.Parse's keyfunc error wrapping all the
		// way out of parseAndValidateToken, so the chain and HTTP gate can act on it.
		auth.jwks.expiresAt = time.Now().Add(-time.Hour)
		auth.cfg.Issuer = "http://127.0.0.1:1" // unreachable

		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"test-key"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user","exp":9999999999}`))
		token := header + "." + payload + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))

		_, err := auth.parseAndValidateToken(context.Background(), token)
		if err == nil {
			t.Fatal("expected error when JWKS is unreachable")
		}
		if !errors.Is(err, middleware.ErrValidationUnavailable) {
			t.Errorf("error = %v, want it to wrap middleware.ErrValidationUnavailable", err)
		}
	})
}

func TestOIDCAuthenticator_parseAndValidateToken_ValidSignature(t *testing.T) {
	// Generate RSA key pair for testing
	privateKey, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Encode public key components for JWKS
	nBytes := privateKey.N.Bytes()
	nBase64 := base64.RawURLEncoding.EncodeToString(nBytes)

	eBytes := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
	eBase64 := base64.RawURLEncoding.EncodeToString(eBytes)

	// Create test server serving JWKS
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"keys": [
					{
						"kty": "RSA",
						"kid": "test-key-1",
						"use": "sig",
						"n": "%s",
						"e": "%s"
					}
				]
			}`, nBase64, eBase64)
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
		"sub": testUserID,
		"exp": float64(time.Now().Add(time.Hour).Unix()),
		"iss": server.URL,
	})
	token.Header["kid"] = "test-key-1"

	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	// Parse and validate the token
	claims, err := auth.parseAndValidateToken(context.Background(), signedToken)
	if err != nil {
		t.Fatalf(errUnexpected, err)
	}

	// Verify claims were extracted
	if claims["sub"] != testUserID {
		t.Errorf("expected sub='user123', got %v", claims["sub"])
	}
}

func TestOIDCAuthenticator_parseAndValidateToken_InvalidSignature(t *testing.T) {
	// Generate two different RSA key pairs
	signingKey, _ := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	jwksKey, _ := rsa.GenerateKey(rand.Reader, rsaKeyBits)

	// Encode JWKS key (different from signing key)
	nBytes := jwksKey.N.Bytes()
	nBase64 := base64.RawURLEncoding.EncodeToString(nBytes)
	eBytes := big.NewInt(int64(jwksKey.PublicKey.E)).Bytes()
	eBase64 := base64.RawURLEncoding.EncodeToString(eBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"keys": [{"kty": "RSA", "kid": "test-key", "use": "sig", "n": "%s", "e": "%s"}]
			}`, nBase64, eBase64)
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

	_, err := auth.parseAndValidateToken(context.Background(), signedToken)
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
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
		t.Fatalf(errUnexpected, err)
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
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
	if !strings.Contains(err.Error(), errNoValidRSAKeys) {
		t.Errorf(errUnexpectedMsg, err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_KeyWithEncUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
	if !strings.Contains(err.Error(), errNoValidRSAKeys) {
		t.Errorf(errUnexpectedMsg, err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidModulus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
	if !strings.Contains(err.Error(), errNoValidRSAKeys) {
		t.Errorf(errUnexpectedMsg, err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_InvalidExponent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
	if !strings.Contains(err.Error(), errNoValidRSAKeys) {
		t.Errorf(errUnexpectedMsg, err)
	}
}

func TestOIDCAuthenticator_FetchJWKS_MissingKid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case oidcDiscoveryPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
		case jwksPath:
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
		t.Fatalf(errUnexpected, err)
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

// jwksTestServer is a controllable JWKS/OIDC discovery server for exercising the
// on-demand refresh path: it counts /jwks requests, can rotate its signing key,
// and can be made to fail the /jwks endpoint.
type jwksTestServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	kid     string
	key     *rsa.PrivateKey
	hits    atomic.Int32
	fail    atomic.Bool
	delay   time.Duration
	delayMu sync.Mutex
}

func newJWKSTestServer(t *testing.T) *jwksTestServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	js := &jwksTestServer{kid: "key-a", key: key}
	js.server = httptest.NewServer(http.HandlerFunc(js.handle))
	t.Cleanup(js.server.Close)
	return js
}

func (j *jwksTestServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case oidcDiscoveryPath:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jwks_uri": "` + "http://" + r.Host + `/jwks"}`))
	case jwksPath:
		j.hits.Add(1)
		j.delayMu.Lock()
		d := j.delay
		j.delayMu.Unlock()
		if d > 0 {
			time.Sleep(d)
		}
		if j.fail.Load() {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		j.mu.Lock()
		kid, key := j.kid, j.key
		j.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		nB64 := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		eB64 := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
		_, _ = fmt.Fprintf(w, `{"keys": [{"kty": "RSA", "kid": "%s", "use": "sig", "n": "%s", "e": "%s"}]}`, kid, nB64, eB64) //nolint:gocritic // JSON template requires literal quotes, not %q
	default:
		http.NotFound(w, r)
	}
}

// rotate replaces the server's signing key and kid, simulating IdP key rotation.
func (j *jwksTestServer) rotate(t *testing.T, newKid string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		t.Fatalf("generating rotated RSA key: %v", err)
	}
	j.mu.Lock()
	j.kid, j.key = newKid, key
	j.mu.Unlock()
}

// signToken signs a valid JWT with the server's current key and kid.
func (j *jwksTestServer) signToken(t *testing.T) string {
	t.Helper()
	j.mu.Lock()
	kid, key := j.kid, j.key
	j.mu.Unlock()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": testUserID,
		"exp": float64(time.Now().Add(time.Hour).Unix()),
		"iss": j.server.URL,
	})
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}

func (j *jwksTestServer) currentKid() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.kid
}

func (j *jwksTestServer) setDelay(d time.Duration) {
	j.delayMu.Lock()
	j.delay = d
	j.delayMu.Unlock()
}

func (j *jwksTestServer) resetHits()      { j.hits.Store(0) }
func (j *jwksTestServer) fetchCount() int { return int(j.hits.Load()) }

// expireCache forces the authenticator's cached JWKS to appear stale.
func expireCache(t *testing.T, auth *OIDCAuthenticator) {
	t.Helper()
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.jwks == nil {
		t.Fatal("cannot expire a nil JWKS cache")
	}
	auth.jwks.expiresAt = time.Now().Add(-time.Minute)
}

func newSelfHealAuth(t *testing.T, js *jwksTestServer) *OIDCAuthenticator {
	t.Helper()
	auth, err := NewOIDCAuthenticator(OIDCConfig{
		Issuer:                 js.server.URL,
		SkipIssuerVerification: true,
	})
	if err != nil {
		t.Fatalf("creating authenticator: %v", err)
	}
	return auth
}

// TestOIDCAuthenticator_RefreshOnExpiry verifies token verification succeeds
// after the JWKS cache TTL has elapsed, where the pre-fix code failed with a
// cache-expired error (issue #882 finding 1.2).
func TestOIDCAuthenticator_RefreshOnExpiry(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	token := js.signToken(t)
	expireCache(t, auth)

	claims, err := auth.parseAndValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("expected verification to succeed after expiry, got: %v", err)
	}
	if claims["sub"] != testUserID {
		t.Errorf("expected sub=%q, got %v", testUserID, claims["sub"])
	}
	if js.fetchCount() < 2 {
		t.Errorf("expected an on-demand refresh (>=2 total fetches), got %d", js.fetchCount())
	}
}

// TestOIDCAuthenticator_KeyRotation verifies a token signed with a rotated key
// and new kid verifies without a process restart.
func TestOIDCAuthenticator_KeyRotation(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	tokenA := js.signToken(t)
	if _, err := auth.parseAndValidateToken(context.Background(), tokenA); err != nil {
		t.Fatalf("token A should verify from the startup cache: %v", err)
	}

	js.rotate(t, "key-b")
	tokenB := js.signToken(t)
	claims, err := auth.parseAndValidateToken(context.Background(), tokenB)
	if err != nil {
		t.Fatalf("token B should verify after on-demand refresh: %v", err)
	}
	if claims["sub"] != testUserID {
		t.Errorf("expected sub=%q, got %v", testUserID, claims["sub"])
	}
}

// TestOIDCAuthenticator_SingleFlightRefresh verifies that many concurrent
// verifications against an expired cache collapse into exactly one JWKS fetch.
func TestOIDCAuthenticator_SingleFlightRefresh(t *testing.T) {
	js := newJWKSTestServer(t)
	js.setDelay(100 * time.Millisecond) // widen the in-flight window
	auth := newSelfHealAuth(t, js)

	js.resetHits()
	expireCache(t, auth)

	const n = 50
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	kid := js.currentKid()
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = auth.getPublicKey(context.Background(), kid)
		}(i)
	}
	close(start)
	wg.Wait()

	if got := js.fetchCount(); got != 1 {
		t.Errorf("expected exactly 1 JWKS fetch under single-flight, got %d", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
}

// TestOIDCAuthenticator_RefreshThrottle verifies repeated unknown-kid lookups
// within the throttle window trigger at most one additional fetch.
func TestOIDCAuthenticator_RefreshThrottle(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	js.resetHits()

	if _, err := auth.getPublicKey(context.Background(), "unknown-1"); err == nil {
		t.Fatal("expected key-not-found for unknown-1")
	}
	if _, err := auth.getPublicKey(context.Background(), "unknown-2"); err == nil {
		t.Fatal("expected key-not-found for unknown-2")
	}

	if got := js.fetchCount(); got != 1 {
		t.Errorf("expected at most 1 additional fetch within throttle window, got %d", got)
	}
}

// fakeClock is a manually-advanced clock for deterministic throttle-window tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// TestOIDCAuthenticator_FailedRefreshRecoversPastShortWindow verifies that after
// a FAILED refresh the short recovery window applies: an attempt made 10s ago
// (within the 1-minute anti-hammer window but past the 5s recovery window) does
// not throttle recovery, so a transient issuer blip heals quickly rather than
// prolonging the fail-closed outage for the full minute (issue #882 findings 1 & 4).
func TestOIDCAuthenticator_FailedRefreshRecoversPastShortWindow(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	base := time.Now()
	auth.now = (&fakeClock{t: base}).now
	js.resetHits()

	// A refresh FAILED 10s ago and the cache is now expired.
	auth.mu.Lock()
	auth.lastRefreshAttempt = base.Add(-10 * time.Second)
	auth.lastRefreshOK = false
	auth.jwks.expiresAt = base.Add(-time.Second)
	auth.mu.Unlock()

	key, err := auth.getPublicKey(context.Background(), js.currentKid())
	if err != nil {
		t.Fatalf("expected recovery refresh to succeed past the short window, got: %v", err)
	}
	if key == nil {
		t.Fatal("expected a key after recovery refresh")
	}
	if got := js.fetchCount(); got != 1 {
		t.Errorf("expected exactly 1 recovery fetch, got %d", got)
	}
}

// TestOIDCAuthenticator_SuccessfulRefreshUsesLongWindow verifies that after a
// SUCCESSFUL refresh the full anti-hammer window applies: an unknown-kid lookup
// 10s later (past the 5s recovery window but within the 1-minute window) still
// throttles, so garbage-kid floods cannot hammer the issuer between recovery
// intervals. This holds regardless of the caller's cache state, so single-flight
// collapsing valid- and expired-cache callers cannot pick the wrong window.
func TestOIDCAuthenticator_SuccessfulRefreshUsesLongWindow(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	base := time.Now()
	auth.now = (&fakeClock{t: base}).now
	js.resetHits()

	// A refresh SUCCEEDED 10s ago; the cache remains valid.
	auth.mu.Lock()
	auth.lastRefreshAttempt = base.Add(-10 * time.Second)
	auth.lastRefreshOK = true
	auth.jwks.expiresAt = base.Add(jwksCacheTTL)
	auth.mu.Unlock()

	if _, err := auth.getPublicKey(context.Background(), "unknown-kid"); err == nil {
		t.Fatal("expected key-not-found for unknown kid")
	}
	if got := js.fetchCount(); got != 0 {
		t.Errorf("expected no fetch within the anti-hammer window, got %d", got)
	}
}

// TestOIDCAuthenticator_ThrottledExpiredFailsClosed verifies that when the cache
// is expired but a refresh was attempted within the throttle window, the lookup
// fails closed without issuing another fetch.
func TestOIDCAuthenticator_ThrottledExpiredFailsClosed(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	js.resetHits()
	// Simulate a very recent refresh attempt so the throttle is active.
	auth.mu.Lock()
	auth.lastRefreshAttempt = time.Now()
	auth.mu.Unlock()
	expireCache(t, auth)

	_, err := auth.getPublicKey(context.Background(), js.currentKid())
	if err == nil {
		t.Fatal("expected fail-closed error for throttled expired cache")
	}
	if !strings.Contains(err.Error(), "jwks cache expired") {
		t.Errorf(errUnexpected, err)
	}
	if got := js.fetchCount(); got != 0 {
		t.Errorf("expected no fetch while throttled, got %d", got)
	}
}

// TestOIDCAuthenticator_FailClosedOnRefreshError verifies that when the JWKS
// endpoint fails after the cache has expired, verification errors and nothing is
// accepted unverified.
func TestOIDCAuthenticator_FailClosedOnRefreshError(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	token := js.signToken(t)
	js.fail.Store(true)
	expireCache(t, auth)

	if _, err := auth.parseAndValidateToken(context.Background(), token); err == nil {
		t.Fatal("expected fail-closed error when refresh fails on expired cache")
	}
}

// TestOIDCAuthenticator_RefreshHonorsCallerContext verifies that an on-demand
// refresh does not pin the request goroutine to the fetch: when the caller's
// context deadline expires while the IdP is slow, getPublicKey returns promptly
// with the context error instead of blocking for the full fetch (issue #882
// finding 0).
func TestOIDCAuthenticator_RefreshHonorsCallerContext(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	// Make the JWKS endpoint slow only after the fast startup fetch.
	js.setDelay(300 * time.Millisecond)
	expireCache(t, auth)
	js.resetHits()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := auth.getPublicKey(ctx, js.currentKid())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when caller context expires during refresh")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
	if elapsed >= 300*time.Millisecond {
		t.Errorf("getPublicKey blocked for %v; should return on the caller deadline, not the fetch", elapsed)
	}
}

// TestOIDCAuthenticator_AuthenticateHonorsCallerDeadline drives the caller
// deadline through the real Authenticate -> parseAndValidateToken -> getPublicKey
// chain (per CLAUDE.md rule 5), proving the request context actually propagates
// end to end: with a slow issuer and a short deadline, Authenticate returns
// promptly with a context error instead of blocking on the fetch.
func TestOIDCAuthenticator_AuthenticateHonorsCallerDeadline(t *testing.T) {
	js := newJWKSTestServer(t)
	auth := newSelfHealAuth(t, js)

	token := js.signToken(t)
	// Make the issuer slow only after the fast startup fetch, and expire the
	// cache so validating the token forces an on-demand refresh.
	js.setDelay(300 * time.Millisecond)
	expireCache(t, auth)
	js.resetHits()

	deadlineCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ctx := WithToken(deadlineCtx, token)

	start := time.Now()
	_, err := auth.Authenticate(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Authenticate to fail when the caller deadline expires during JWKS refresh")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected error to wrap context.DeadlineExceeded, got: %v", err)
	}
	if elapsed >= 300*time.Millisecond {
		t.Errorf("Authenticate blocked for %v; the caller deadline should win over the slow fetch", elapsed)
	}
}
