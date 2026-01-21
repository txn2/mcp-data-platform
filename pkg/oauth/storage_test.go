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

func TestClientStruct(t *testing.T) {
	now := time.Now()
	client := Client{
		ID:           "123",
		ClientID:     "client-id",
		ClientSecret: "hashed-secret",
		Name:         "Test Client",
		RedirectURIs: []string{"http://localhost:8080"},
		GrantTypes:   []string{"authorization_code"},
		RequirePKCE:  true,
		CreatedAt:    now,
		Active:       true,
	}

	if client.ID != "123" {
		t.Errorf("unexpected ID: %s", client.ID)
	}
	if client.ClientID != "client-id" {
		t.Errorf("unexpected ClientID: %s", client.ClientID)
	}
	if !client.RequirePKCE {
		t.Error("expected RequirePKCE to be true")
	}
	if !client.Active {
		t.Error("expected Active to be true")
	}
}

func TestAuthorizationCodeStruct(t *testing.T) {
	now := time.Now()
	code := AuthorizationCode{
		ID:            "id-1",
		Code:          "code-value",
		ClientID:      "client-id",
		UserID:        "user-123",
		UserClaims:    map[string]any{"role": "admin"},
		CodeChallenge: "challenge",
		RedirectURI:   "http://localhost:8080/callback",
		Scope:         "read write",
		ExpiresAt:     now.Add(10 * time.Minute),
		Used:          false,
		CreatedAt:     now,
	}

	if code.ID != "id-1" {
		t.Errorf("unexpected ID: %s", code.ID)
	}
	if code.Code != "code-value" {
		t.Errorf("unexpected Code: %s", code.Code)
	}
	if code.UserClaims["role"] != "admin" {
		t.Error("unexpected UserClaims")
	}
	if code.Used {
		t.Error("expected Used to be false")
	}
}

func TestRefreshTokenStruct(t *testing.T) {
	now := time.Now()
	token := RefreshToken{
		ID:         "id-1",
		Token:      "token-value",
		ClientID:   "client-id",
		UserID:     "user-123",
		UserClaims: map[string]any{"role": "user"},
		Scope:      "read",
		ExpiresAt:  now.Add(30 * 24 * time.Hour),
		CreatedAt:  now,
	}

	if token.ID != "id-1" {
		t.Errorf("unexpected ID: %s", token.ID)
	}
	if token.Token != "token-value" {
		t.Errorf("unexpected Token: %s", token.Token)
	}
	if token.UserClaims["role"] != "user" {
		t.Error("unexpected UserClaims")
	}
}
