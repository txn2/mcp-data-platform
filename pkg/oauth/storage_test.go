package oauth

import (
	"testing"
	"time"
)

const (
	testExampleCallback = "https://example.com/oauth/callback"
	testLocalhostURI    = "http://localhost"
	testLoopback127     = "http://127.0.0.1"
)

func TestClientValidRedirectURI(t *testing.T) {
	client := &Client{
		RedirectURIs: []string{
			"http://localhost:8080/callback",
			testExampleCallback,
		},
	}

	tests := []struct {
		name     string
		uri      string
		expected bool
	}{
		{"valid localhost", "http://localhost:8080/callback", true},
		{"valid example", testExampleCallback, true},
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

func TestClientValidRedirectURI_Loopback(t *testing.T) {
	client := &Client{
		RedirectURIs: []string{
			testLocalhostURI,
			testLoopback127,
			testExampleCallback,
		},
	}

	tests := []struct {
		name     string
		uri      string
		expected bool
	}{
		{"localhost with dynamic port", "http://localhost:52431/callback", true},
		{"localhost with different port", "http://localhost:9999", true},
		{"localhost exact match", testLocalhostURI, true},
		{"127.0.0.1 with dynamic port", "http://127.0.0.1:12345/callback", true},
		{"127.0.0.1 with different port and path", "http://127.0.0.1:8080/oauth/cb", true},
		{"127.0.0.1 exact match", testLoopback127, true},
		{"non-loopback exact match", testExampleCallback, true},
		{"non-loopback with port rejected", "https://example.com:8080/oauth/callback", false},
		{"scheme mismatch on loopback", "https://localhost:1234", false},
		{"attacker host rejected", "http://attacker.com/callback", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.ValidRedirectURI(tt.uri)
			if result != tt.expected {
				t.Errorf("ValidRedirectURI(%q) = %v, want %v", tt.uri, result, tt.expected)
			}
		})
	}
}

func TestIsLoopbackURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expected bool
	}{
		{"localhost http", testLocalhostURI, true},
		{"localhost with port", "http://localhost:8080", true},
		{"localhost with path", "http://localhost:8080/callback", true},
		{"127.0.0.1 http", testLoopback127, true},
		{"127.0.0.1 with port", "http://127.0.0.1:3000", true},
		{"ipv6 loopback", "http://[::1]:8080", true},
		{"https localhost not loopback", "https://localhost:443", false},
		{"https 127.0.0.1 not loopback", "https://127.0.0.1:443", false},
		{"external host", "http://example.com", false},
		{"empty string", "", false},
		{"invalid uri", "://bad", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLoopbackURI(tt.uri)
			if result != tt.expected {
				t.Errorf("isLoopbackURI(%q) = %v, want %v", tt.uri, result, tt.expected)
			}
		})
	}
}

func TestMatchesRedirectURI(t *testing.T) {
	tests := []struct {
		name       string
		registered string
		requested  string
		expected   bool
	}{
		{"exact match non-loopback", "https://example.com/cb", "https://example.com/cb", true},
		{"exact match loopback", "http://localhost:8080/cb", "http://localhost:8080/cb", true},
		{"loopback different port", testLocalhostURI, "http://localhost:52431/callback", true},
		{"loopback different path", "http://localhost:8080", "http://localhost:8080/other", true},
		{"127.0.0.1 different port", testLoopback127, "http://127.0.0.1:12345/callback", true},
		{"ipv6 loopback different port", "http://[::1]", "http://[::1]:9999/cb", true},
		{"non-loopback port mismatch", "https://example.com", "https://example.com:8080", false},
		{"non-loopback path mismatch", "https://example.com/a", "https://example.com/b", false},
		{"scheme mismatch loopback", testLocalhostURI, "https://localhost:1234", false},
		{"host mismatch", testLocalhostURI, "http://127.0.0.1:1234", false},
		{"registered non-loopback requested loopback", "https://example.com", "http://localhost:1234", false},
		{"registered loopback requested non-loopback", testLocalhostURI, "https://example.com", false},
		{"empty registered", "", testLocalhostURI, false},
		{"empty requested", testLocalhostURI, "", false},
		{"both empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesRedirectURI(tt.registered, tt.requested)
			if result != tt.expected {
				t.Errorf("matchesRedirectURI(%q, %q) = %v, want %v",
					tt.registered, tt.requested, result, tt.expected)
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
