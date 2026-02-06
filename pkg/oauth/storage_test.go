package oauth

import (
	"testing"
	"time"
)

func TestClientValidRedirectURI(t *testing.T) {
	client := &Client{
		RedirectURIs: []string{
			"http://localhost:8080/callback",
			"https://example.com/oauth/callback",
		},
	}

	tests := []struct {
		name     string
		uri      string
		expected bool
	}{
		{"valid localhost", "http://localhost:8080/callback", true},
		{"valid example", "https://example.com/oauth/callback", true},
		{"invalid uri", "http://attacker.com/callback", false},
		{"empty uri", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.ValidRedirectURI(tt.uri)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestClientSupportsGrantType(t *testing.T) {
	client := &Client{
		GrantTypes: []string{"authorization_code", "refresh_token"},
	}

	tests := []struct {
		name      string
		grantType string
		expected  bool
	}{
		{"supports authorization_code", "authorization_code", true},
		{"supports refresh_token", "refresh_token", true},
		{"does not support client_credentials", "client_credentials", false},
		{"does not support password", "password", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.SupportsGrantType(tt.grantType)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestAuthorizationCodeIsExpired(t *testing.T) {
	t.Run("not expired", func(t *testing.T) {
		code := &AuthorizationCode{
			ExpiresAt: time.Now().Add(10 * time.Minute),
		}
		if code.IsExpired() {
			t.Error("expected code to not be expired")
		}
	})

	t.Run("expired", func(t *testing.T) {
		code := &AuthorizationCode{
			ExpiresAt: time.Now().Add(-10 * time.Minute),
		}
		if !code.IsExpired() {
			t.Error("expected code to be expired")
		}
	})
}

func TestRefreshTokenIsExpired(t *testing.T) {
	t.Run("not expired", func(t *testing.T) {
		token := &RefreshToken{
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		if token.IsExpired() {
			t.Error("expected token to not be expired")
		}
	})

	t.Run("expired", func(t *testing.T) {
		token := &RefreshToken{
			ExpiresAt: time.Now().Add(-24 * time.Hour),
		}
		if !token.IsExpired() {
			t.Error("expected token to be expired")
		}
	})
}

