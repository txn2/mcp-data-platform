package oauth

import (
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/oauth/signkey"
)

func TestGenerateAccessToken_CarriesKID(t *testing.T) {
	key := []byte("server-signing-key-at-least-32-bytes-x")
	server, err := NewServer(ServerConfig{
		Issuer:     "https://oauth.example.com",
		SigningKey: key,
	}, NewMemoryStorage())
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}

	tokenStr, err := server.generateAccessToken("client-1", "user-1", nil, "openid")
	if err != nil {
		t.Fatalf("generating access token: %v", err)
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parsing token header: %v", err)
	}

	kid, ok := parsed.Header["kid"].(string)
	if !ok || kid == "" {
		t.Fatalf("token missing kid header: %v", parsed.Header)
	}
	if want := signkey.KeyID(key); kid != want {
		t.Errorf("kid = %q, want %q (SHA-256 of signing key)", kid, want)
	}
}

func TestGenerateAccessToken_OpaqueWhenNoKey(t *testing.T) {
	server, err := NewServer(ServerConfig{Issuer: "https://oauth.example.com"}, NewMemoryStorage())
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}

	// With no signing key, the token is opaque (not a JWT) and has no kid.
	tokenStr, err := server.generateAccessToken("client-1", "user-1", nil, "openid")
	if err != nil {
		t.Fatalf("generating access token: %v", err)
	}
	if strings.Count(tokenStr, ".") == jwtPartCount-1 {
		t.Errorf("expected opaque token without a signing key, got a JWT: %q", tokenStr)
	}
}
