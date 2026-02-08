package oauth

import (
	"context"
	"testing"
	"time"
)

const (
	fuzzDefaultAccessTTL  = 3600
	fuzzDefaultRefreshTTL = 86400
	fuzzDefaultCodeTTL    = 600
)

// newFuzzMockStorage creates a mock storage for fuzz testing that returns proper nil errors.
func newFuzzMockStorage() *mockStorage {
	clients := make(map[string]*Client)
	codes := make(map[string]*AuthorizationCode)
	tokens := make(map[string]*RefreshToken)

	return &mockStorage{
		createClientFunc: func(_ context.Context, client *Client) error {
			clients[client.ID] = client
			return nil
		},
		getClientFunc: func(_ context.Context, clientID string) (*Client, error) {
			if c, ok := clients[clientID]; ok {
				return c, nil
			}
			return nil, nil
		},
		saveAuthorizationCodeFunc: func(_ context.Context, code *AuthorizationCode) error {
			codes[code.Code] = code
			return nil
		},
		getAuthorizationCodeFunc: func(_ context.Context, code string) (*AuthorizationCode, error) {
			if c, ok := codes[code]; ok {
				return c, nil
			}
			return nil, nil
		},
		deleteAuthorizationCodeFunc: func(_ context.Context, code string) error {
			delete(codes, code)
			return nil
		},
		saveRefreshTokenFunc: func(_ context.Context, token *RefreshToken) error {
			tokens[token.Token] = token
			return nil
		},
		getRefreshTokenFunc: func(_ context.Context, token string) (*RefreshToken, error) {
			if t, ok := tokens[token]; ok {
				return t, nil
			}
			return nil, nil
		},
		deleteRefreshTokenFunc: func(_ context.Context, token string) error {
			delete(tokens, token)
			return nil
		},
	}
}

// FuzzDCRRequest fuzzes the DCRRequest structure.
func FuzzDCRRequest(f *testing.F) {
	f.Add("Test Client", "http://localhost:8080/callback", "authorization_code")
	f.Add("", "", "")
	f.Add("Client", "https://example.com/callback", "refresh_token")
	f.Add("Name with spaces", "http://localhost/path?query=1", "client_credentials")

	storage := newFuzzMockStorage()
	server, err := NewServer(ServerConfig{
		Issuer:          "https://issuer.example.com",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		AuthCodeTTL:     10 * time.Minute,
		DCR: DCRConfig{
			Enabled:           true,
			DefaultGrantTypes: []string{"authorization_code"},
		},
	}, storage)
	if err != nil {
		return
	}

	f.Fuzz(func(_ *testing.T, clientName, redirectURI, grantType string) {
		var redirectURIs []string
		if redirectURI != "" {
			redirectURIs = []string{redirectURI}
		}
		var grantTypes []string
		if grantType != "" {
			grantTypes = []string{grantType}
		}

		req := DCRRequest{
			ClientName:   clientName,
			RedirectURIs: redirectURIs,
			GrantTypes:   grantTypes,
		}

		// Should not panic - errors are expected
		_, _ = server.RegisterClient(context.Background(), req)
	})
}

// FuzzServerConfig fuzzes server configuration.
func FuzzServerConfig(f *testing.F) {
	f.Add("https://example.com", int64(fuzzDefaultAccessTTL), int64(fuzzDefaultRefreshTTL), int64(fuzzDefaultCodeTTL))
	f.Add("", int64(0), int64(0), int64(0))
	f.Add("http://localhost", int64(1), int64(1), int64(1))
	f.Add("invalid-url", int64(-1), int64(-1), int64(-1))

	f.Fuzz(func(_ *testing.T, issuer string, accessTTL, refreshTTL, authCodeTTL int64) {
		storage := newFuzzMockStorage()
		cfg := ServerConfig{
			Issuer:          issuer,
			AccessTokenTTL:  time.Duration(accessTTL) * time.Second,
			RefreshTokenTTL: time.Duration(refreshTTL) * time.Second,
			AuthCodeTTL:     time.Duration(authCodeTTL) * time.Second,
		}

		// Should not panic
		_, _ = NewServer(cfg, storage)
	})
}

// FuzzDCRConfig fuzzes DCR configuration.
func FuzzDCRConfig(f *testing.F) {
	f.Add(true, "authorization_code")
	f.Add(false, "")
	f.Add(true, "refresh_token")
	f.Add(true, "client_credentials")

	f.Fuzz(func(_ *testing.T, enabled bool, grantType string) {
		var grantTypes []string
		if grantType != "" {
			grantTypes = []string{grantType}
		}

		storage := newFuzzMockStorage()
		cfg := DCRConfig{
			Enabled:           enabled,
			DefaultGrantTypes: grantTypes,
		}

		// Should not panic
		_, _ = NewDCRService(storage, cfg)
	})
}
