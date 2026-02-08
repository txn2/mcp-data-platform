package oauth

import (
	"context"
	"errors"
	"testing"
)

// mockStorage implements Storage for testing.
type mockStorage struct {
	createClientFunc             func(ctx context.Context, client *Client) error
	getClientFunc                func(ctx context.Context, clientID string) (*Client, error)
	updateClientFunc             func(ctx context.Context, client *Client) error
	deleteClientFunc             func(ctx context.Context, clientID string) error
	listClientsFunc              func(ctx context.Context) ([]*Client, error)
	saveAuthorizationCodeFunc    func(ctx context.Context, code *AuthorizationCode) error
	getAuthorizationCodeFunc     func(ctx context.Context, code string) (*AuthorizationCode, error)
	deleteAuthorizationCodeFunc  func(ctx context.Context, code string) error
	cleanupExpiredCodesFunc      func(ctx context.Context) error
	saveRefreshTokenFunc         func(ctx context.Context, token *RefreshToken) error
	getRefreshTokenFunc          func(ctx context.Context, token string) (*RefreshToken, error)
	deleteRefreshTokenFunc       func(ctx context.Context, token string) error
	deleteRefreshTokensForClient func(ctx context.Context, clientID string) error
	cleanupExpiredTokensFunc     func(ctx context.Context) error
}

func (m *mockStorage) CreateClient(ctx context.Context, client *Client) error {
	if m.createClientFunc != nil {
		return m.createClientFunc(ctx, client)
	}
	return nil
}

func (m *mockStorage) GetClient(ctx context.Context, clientID string) (*Client, error) {
	if m.getClientFunc != nil {
		return m.getClientFunc(ctx, clientID)
	}
	return nil, errors.New("client not found")
}

func (m *mockStorage) UpdateClient(ctx context.Context, client *Client) error {
	if m.updateClientFunc != nil {
		return m.updateClientFunc(ctx, client)
	}
	return nil
}

func (m *mockStorage) DeleteClient(ctx context.Context, clientID string) error {
	if m.deleteClientFunc != nil {
		return m.deleteClientFunc(ctx, clientID)
	}
	return nil
}

func (m *mockStorage) ListClients(ctx context.Context) ([]*Client, error) {
	if m.listClientsFunc != nil {
		return m.listClientsFunc(ctx)
	}
	return nil, nil
}

func (m *mockStorage) SaveAuthorizationCode(ctx context.Context, code *AuthorizationCode) error {
	if m.saveAuthorizationCodeFunc != nil {
		return m.saveAuthorizationCodeFunc(ctx, code)
	}
	return nil
}

func (m *mockStorage) GetAuthorizationCode(ctx context.Context, code string) (*AuthorizationCode, error) {
	if m.getAuthorizationCodeFunc != nil {
		return m.getAuthorizationCodeFunc(ctx, code)
	}
	return nil, errors.New("code not found")
}

func (m *mockStorage) DeleteAuthorizationCode(ctx context.Context, code string) error {
	if m.deleteAuthorizationCodeFunc != nil {
		return m.deleteAuthorizationCodeFunc(ctx, code)
	}
	return nil
}

func (m *mockStorage) CleanupExpiredCodes(ctx context.Context) error {
	if m.cleanupExpiredCodesFunc != nil {
		return m.cleanupExpiredCodesFunc(ctx)
	}
	return nil
}

func (m *mockStorage) SaveRefreshToken(ctx context.Context, token *RefreshToken) error {
	if m.saveRefreshTokenFunc != nil {
		return m.saveRefreshTokenFunc(ctx, token)
	}
	return nil
}

func (m *mockStorage) GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error) {
	if m.getRefreshTokenFunc != nil {
		return m.getRefreshTokenFunc(ctx, token)
	}
	return nil, errors.New("token not found")
}

func (m *mockStorage) DeleteRefreshToken(ctx context.Context, token string) error {
	if m.deleteRefreshTokenFunc != nil {
		return m.deleteRefreshTokenFunc(ctx, token)
	}
	return nil
}

func (m *mockStorage) DeleteRefreshTokensForClient(ctx context.Context, clientID string) error {
	if m.deleteRefreshTokensForClient != nil {
		return m.deleteRefreshTokensForClient(ctx, clientID)
	}
	return nil
}

func (m *mockStorage) CleanupExpiredTokens(ctx context.Context) error {
	if m.cleanupExpiredTokensFunc != nil {
		return m.cleanupExpiredTokensFunc(ctx)
	}
	return nil
}

func TestNewDCRService(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		storage := &mockStorage{}
		config := DCRConfig{
			Enabled:                 true,
			AllowedRedirectPatterns: []string{`^http://localhost.*`},
			DefaultGrantTypes:       []string{"authorization_code"},
		}

		service, err := NewDCRService(storage, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if service == nil {
			t.Fatal("expected non-nil service")
		}
	})

	t.Run("invalid regex pattern", func(t *testing.T) {
		storage := &mockStorage{}
		config := DCRConfig{
			Enabled:                 true,
			AllowedRedirectPatterns: []string{`[invalid`},
		}

		_, err := NewDCRService(storage, config)
		if err == nil {
			t.Error("expected error for invalid regex")
		}
	})

	t.Run("default grant types", func(t *testing.T) {
		storage := &mockStorage{}
		config := DCRConfig{
			Enabled: true,
		}

		service, err := NewDCRService(storage, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(service.config.DefaultGrantTypes) != 2 {
			t.Errorf("expected 2 default grant types, got %d", len(service.config.DefaultGrantTypes))
		}
	})
}

func TestDCRServiceRegister(t *testing.T) {
	ctx := context.Background()

	t.Run("DCR disabled", func(t *testing.T) {
		storage := &mockStorage{}
		service, _ := NewDCRService(storage, DCRConfig{Enabled: false})

		_, err := service.Register(ctx, DCRRequest{
			ClientName:   "Test Client",
			RedirectURIs: []string{"http://localhost:8080/callback"},
		})
		if err == nil {
			t.Error("expected error when DCR is disabled")
		}
	})

	t.Run("invalid redirect URI", func(t *testing.T) {
		storage := &mockStorage{}
		service, _ := NewDCRService(storage, DCRConfig{
			Enabled:                 true,
			AllowedRedirectPatterns: []string{`^http://localhost.*`},
		})

		_, err := service.Register(ctx, DCRRequest{
			ClientName:   "Test Client",
			RedirectURIs: []string{"http://attacker.com/callback"},
		})
		if err == nil {
			t.Error("expected error for invalid redirect URI")
		}
	})

	t.Run("successful registration", func(t *testing.T) {
		var createdClient *Client
		storage := &mockStorage{
			createClientFunc: func(_ context.Context, client *Client) error {
				createdClient = client
				return nil
			},
		}
		service, _ := NewDCRService(storage, DCRConfig{
			Enabled:     true,
			RequirePKCE: true,
		})

		resp, err := service.Register(ctx, DCRRequest{
			ClientName:   "Test Client",
			RedirectURIs: []string{"http://localhost:8080/callback"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.ClientID == "" {
			t.Error("expected non-empty client_id")
		}
		if resp.ClientSecret == "" {
			t.Error("expected non-empty client_secret")
		}
		if resp.ClientName != "Test Client" {
			t.Errorf("expected client_name 'Test Client', got %q", resp.ClientName)
		}
		if createdClient == nil || !createdClient.RequirePKCE {
			t.Error("expected client to require PKCE")
		}
	})
}

func TestDCRServiceRegister_ErrorsAndOptions(t *testing.T) {
	ctx := context.Background()

	t.Run("storage error", func(t *testing.T) {
		storage := &mockStorage{
			createClientFunc: func(_ context.Context, _ *Client) error {
				return errors.New("storage error")
			},
		}
		service, _ := NewDCRService(storage, DCRConfig{Enabled: true})

		_, err := service.Register(ctx, DCRRequest{
			ClientName:   "Test Client",
			RedirectURIs: []string{"http://localhost:8080/callback"},
		})
		if err == nil {
			t.Error("expected error from storage")
		}
	})

	t.Run("custom grant types", func(t *testing.T) {
		storage := &mockStorage{}
		service, _ := NewDCRService(storage, DCRConfig{Enabled: true})

		resp, err := service.Register(ctx, DCRRequest{
			ClientName:   "Test Client",
			RedirectURIs: []string{"http://localhost:8080/callback"},
			GrantTypes:   []string{"authorization_code"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.GrantTypes) != 1 || resp.GrantTypes[0] != "authorization_code" {
			t.Errorf("expected custom grant types, got %v", resp.GrantTypes)
		}
	})
}

func TestIsAllowedRedirectURI(t *testing.T) {
	t.Run("no patterns allows all", func(t *testing.T) {
		storage := &mockStorage{}
		service, _ := NewDCRService(storage, DCRConfig{Enabled: true})

		if !service.isAllowedRedirectURI("http://any-url.com/callback") {
			t.Error("expected any URL to be allowed when no patterns configured")
		}
	})

	t.Run("pattern matching", func(t *testing.T) {
		storage := &mockStorage{}
		service, _ := NewDCRService(storage, DCRConfig{
			Enabled:                 true,
			AllowedRedirectPatterns: []string{`^http://localhost.*`, `^https://.*\.example\.com/.*`},
		})

		tests := []struct {
			uri     string
			allowed bool
		}{
			{"http://localhost:8080/callback", true},
			{"http://localhost/callback", true},
			{"https://app.example.com/oauth/callback", true},
			{"http://attacker.com/callback", false},
			{"https://example.com.attacker.com/callback", false},
		}

		for _, tt := range tests {
			result := service.isAllowedRedirectURI(tt.uri)
			if result != tt.allowed {
				t.Errorf("URI %q: expected %v, got %v", tt.uri, tt.allowed, result)
			}
		}
	})
}

func TestGenerateSecureToken(t *testing.T) {
	t.Run("generates token of expected length", func(t *testing.T) {
		token, err := generateSecureToken(32)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// base64 encoding of 32 bytes should be ~43 characters
		if len(token) < 40 {
			t.Errorf("token too short: %d", len(token))
		}
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		tokens := make(map[string]bool)
		for range 100 {
			token, err := generateSecureToken(32)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tokens[token] {
				t.Error("duplicate token generated")
			}
			tokens[token] = true
		}
	})
}

func TestGenerateID(t *testing.T) {
	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for range 100 {
			id := generateID()
			if ids[id] {
				t.Error("duplicate ID generated")
			}
			ids[id] = true
		}
	})
}

func TestDCRRequest(t *testing.T) {
	req := DCRRequest{
		ClientName:              "Test",
		RedirectURIs:            []string{"http://localhost:8080"},
		GrantTypes:              []string{"authorization_code"},
		TokenEndpointAuthMethod: "client_secret_basic",
	}

	if req.ClientName != "Test" {
		t.Error("unexpected ClientName")
	}
	if len(req.RedirectURIs) != 1 {
		t.Error("unexpected RedirectURIs")
	}
}

func TestDCRResponse(t *testing.T) {
	resp := DCRResponse{
		ClientID:              "client-123",
		ClientSecret:          "secret-456",
		ClientName:            "Test Client",
		RedirectURIs:          []string{"http://localhost:8080"},
		GrantTypes:            []string{"authorization_code"},
		ClientSecretExpiresAt: 0,
	}

	if resp.ClientID != "client-123" {
		t.Error("unexpected ClientID")
	}
	if resp.ClientSecretExpiresAt != 0 {
		t.Error("expected ClientSecretExpiresAt to be 0")
	}
}

func TestDCRConfig(t *testing.T) {
	cfg := DCRConfig{
		Enabled:                 true,
		AllowedRedirectPatterns: []string{`^http://.*`},
		DefaultGrantTypes:       []string{"authorization_code"},
		RequirePKCE:             true,
	}

	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if !cfg.RequirePKCE {
		t.Error("expected RequirePKCE to be true")
	}
}

// Verify mockStorage implements Storage.
var _ Storage = (*mockStorage)(nil)
