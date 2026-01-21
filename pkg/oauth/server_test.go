package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestNewServer(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		storage := &mockStorage{}
		server, err := NewServer(ServerConfig{}, storage)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if server == nil {
			t.Fatal("expected non-nil server")
		}
		if server.config.AccessTokenTTL != 1*time.Hour {
			t.Errorf("expected AccessTokenTTL 1h, got %v", server.config.AccessTokenTTL)
		}
		if server.config.RefreshTokenTTL != 24*time.Hour*30 {
			t.Errorf("expected RefreshTokenTTL 30d, got %v", server.config.RefreshTokenTTL)
		}
		if server.config.AuthCodeTTL != 10*time.Minute {
			t.Errorf("expected AuthCodeTTL 10m, got %v", server.config.AuthCodeTTL)
		}
	})

	t.Run("with DCR enabled", func(t *testing.T) {
		storage := &mockStorage{}
		server, err := NewServer(ServerConfig{
			DCR: DCRConfig{Enabled: true},
		}, storage)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if server.dcr == nil {
			t.Error("expected DCR service to be initialized")
		}
	})

	t.Run("DCR invalid pattern", func(t *testing.T) {
		storage := &mockStorage{}
		_, err := NewServer(ServerConfig{
			DCR: DCRConfig{
				Enabled:                 true,
				AllowedRedirectPatterns: []string{`[invalid`},
			},
		}, storage)
		if err == nil {
			t.Error("expected error for invalid DCR pattern")
		}
	})
}

func TestServerAuthorize(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	t.Run("successful authorization", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
			RequirePKCE:  false,
		}
		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			saveAuthorizationCodeFunc: func(_ context.Context, _ *AuthorizationCode) error {
				return nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		code, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType: "code",
			ClientID:     "client-123",
			RedirectURI:  "http://localhost:8080/callback",
			Scope:        "read",
		}, "user-123", map[string]any{"role": "admin"})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code == "" {
			t.Error("expected non-empty code")
		}
	})

	t.Run("invalid client", func(t *testing.T) {
		storage := &mockStorage{}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType: "code",
			ClientID:     "invalid",
			RedirectURI:  "http://localhost:8080/callback",
		}, "user-123", nil)

		if err == nil {
			t.Error("expected error for invalid client")
		}
	})

	t.Run("inactive client", func(t *testing.T) {
		client := &Client{
			ClientID: "client-123",
			Active:   false,
		}
		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType: "code",
			ClientID:     "client-123",
			RedirectURI:  "http://localhost:8080/callback",
		}, "user-123", nil)

		if err == nil {
			t.Error("expected error for inactive client")
		}
	})

	t.Run("invalid redirect URI", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
		}
		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType: "code",
			ClientID:     "client-123",
			RedirectURI:  "http://attacker.com/callback",
		}, "user-123", nil)

		if err == nil {
			t.Error("expected error for invalid redirect URI")
		}
	})

	t.Run("unsupported response type", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
		}
		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType: "token",
			ClientID:     "client-123",
			RedirectURI:  "http://localhost:8080/callback",
		}, "user-123", nil)

		if err == nil {
			t.Error("expected error for unsupported response type")
		}
	})

	t.Run("PKCE required but missing", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
			RequirePKCE:  true,
		}
		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType: "code",
			ClientID:     "client-123",
			RedirectURI:  "http://localhost:8080/callback",
		}, "user-123", nil)

		if err == nil {
			t.Error("expected error for missing PKCE")
		}
	})

	t.Run("PKCE invalid method", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
			RequirePKCE:  true,
		}
		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Authorize(ctx, AuthorizationRequest{
			ResponseType:        "code",
			ClientID:            "client-123",
			RedirectURI:         "http://localhost:8080/callback",
			CodeChallenge:       "challenge",
			CodeChallengeMethod: "invalid",
		}, "user-123", nil)

		if err == nil {
			t.Error("expected error for invalid PKCE method")
		}
	})
}

func TestServerToken(t *testing.T) {
	ctx := context.Background()

	t.Run("unsupported grant type", func(t *testing.T) {
		storage := &mockStorage{}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType: "password",
		})

		if err == nil {
			t.Error("expected error for unsupported grant type")
		}
	})
}

func TestServerRegisterClient(t *testing.T) {
	ctx := context.Background()

	t.Run("DCR disabled", func(t *testing.T) {
		storage := &mockStorage{}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.RegisterClient(ctx, DCRRequest{
			ClientName:   "Test",
			RedirectURIs: []string{"http://localhost:8080"},
		})

		if err == nil {
			t.Error("expected error when DCR is disabled")
		}
	})

	t.Run("DCR enabled", func(t *testing.T) {
		storage := &mockStorage{}
		server, _ := NewServer(ServerConfig{
			DCR: DCRConfig{Enabled: true},
		}, storage)

		resp, err := server.RegisterClient(ctx, DCRRequest{
			ClientName:   "Test",
			RedirectURIs: []string{"http://localhost:8080"},
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.ClientID == "" {
			t.Error("expected non-empty client_id")
		}
	})
}

func TestServerHTTPHandlers(t *testing.T) {
	storage := &mockStorage{}
	server, _ := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		DCR:    DCRConfig{Enabled: true},
	}, storage)

	t.Run("metadata endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "issuer") {
			t.Error("expected issuer in response")
		}
	})

	t.Run("token endpoint wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/token", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("register endpoint wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/register", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", w.Code)
		}
	})

	t.Run("token endpoint with form", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=password"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("register endpoint with valid JSON", func(t *testing.T) {
		body := `{"client_name":"Test","redirect_uris":["http://localhost:8080"]}`
		req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("expected status 201, got %d", w.Code)
		}
	})

	t.Run("register endpoint with invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader("invalid"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})
}

func TestBuildAuthorizationURL(t *testing.T) {
	url := BuildAuthorizationURL(
		"http://localhost:8080",
		"client-123",
		"http://localhost:3000/callback",
		"read write",
		"state123",
	)

	if !strings.Contains(url, "client_id=client-123") {
		t.Error("expected client_id in URL")
	}
	if !strings.Contains(url, "response_type=code") {
		t.Error("expected response_type in URL")
	}
	if !strings.Contains(url, "code_challenge=") {
		t.Error("expected code_challenge in URL")
	}
	if !strings.Contains(url, "code_challenge_method=S256") {
		t.Error("expected code_challenge_method in URL")
	}
}

func TestServerConfig(t *testing.T) {
	cfg := ServerConfig{
		Issuer:          "http://localhost:8080",
		AccessTokenTTL:  2 * time.Hour,
		RefreshTokenTTL: 7 * 24 * time.Hour,
		AuthCodeTTL:     5 * time.Minute,
		DCR:             DCRConfig{Enabled: true},
	}

	if cfg.Issuer != "http://localhost:8080" {
		t.Error("unexpected Issuer")
	}
	if cfg.AccessTokenTTL != 2*time.Hour {
		t.Error("unexpected AccessTokenTTL")
	}
}

func TestTokenRequest(t *testing.T) {
	req := TokenRequest{
		GrantType:    "authorization_code",
		Code:         "code123",
		RedirectURI:  "http://localhost:8080/callback",
		ClientID:     "client-123",
		ClientSecret: "secret",
		CodeVerifier: "verifier",
		RefreshToken: "",
		Scope:        "read",
	}

	if req.GrantType != "authorization_code" {
		t.Error("unexpected GrantType")
	}
	if req.Code != "code123" {
		t.Error("unexpected Code")
	}
}

func TestTokenResponse(t *testing.T) {
	resp := TokenResponse{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "refresh-123",
		Scope:        "read write",
	}

	if resp.AccessToken != "access-123" {
		t.Error("unexpected AccessToken")
	}
	if resp.TokenType != "Bearer" {
		t.Error("unexpected TokenType")
	}
	if resp.ExpiresIn != 3600 {
		t.Error("unexpected ExpiresIn")
	}
}

func TestErrorResponse(t *testing.T) {
	resp := ErrorResponse{
		Error:            "invalid_request",
		ErrorDescription: "Missing required parameter",
	}

	if resp.Error != "invalid_request" {
		t.Error("unexpected Error")
	}
	if resp.ErrorDescription != "Missing required parameter" {
		t.Error("unexpected ErrorDescription")
	}
}

func TestAuthorizationRequest(t *testing.T) {
	req := AuthorizationRequest{
		ResponseType:        "code",
		ClientID:            "client-123",
		RedirectURI:         "http://localhost:8080/callback",
		Scope:               "read write",
		State:               "state123",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
	}

	if req.ResponseType != "code" {
		t.Error("unexpected ResponseType")
	}
	if req.CodeChallengeMethod != "S256" {
		t.Error("unexpected CodeChallengeMethod")
	}
}

func TestHandleAuthorizationCodeGrant(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	t.Run("successful authorization code grant", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
		}
		authCode := &AuthorizationCode{
			ID:          "code-id-1",
			Code:        "valid-code",
			ClientID:    "client-123",
			UserID:      "user-123",
			UserClaims:  map[string]any{"role": "admin"},
			RedirectURI: "http://localhost:8080/callback",
			Scope:       "read",
			ExpiresAt:   time.Now().Add(10 * time.Minute),
			Used:        false,
			CreatedAt:   time.Now(),
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
			deleteAuthorizationCodeFunc: func(_ context.Context, _ string) error {
				return nil
			},
			saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
				return nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		resp, err := server.Token(ctx, TokenRequest{
			GrantType:    "authorization_code",
			Code:         "valid-code",
			RedirectURI:  "http://localhost:8080/callback",
			ClientID:     "client-123",
			ClientSecret: "secret",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.AccessToken == "" {
			t.Error("expected non-empty access token")
		}
		if resp.RefreshToken == "" {
			t.Error("expected non-empty refresh token")
		}
		if resp.TokenType != "Bearer" {
			t.Errorf("expected token type Bearer, got %s", resp.TokenType)
		}
	})

	t.Run("invalid authorization code", func(t *testing.T) {
		storage := &mockStorage{
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return nil, fmt.Errorf("not found")
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType: "authorization_code",
			Code:      "invalid-code",
		})

		if err == nil {
			t.Error("expected error for invalid code")
		}
	})

	t.Run("authorization code already used", func(t *testing.T) {
		authCode := &AuthorizationCode{
			Code:        "used-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			Used:        true,
		}

		storage := &mockStorage{
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:   "authorization_code",
			Code:        "used-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
		})

		if err == nil {
			t.Error("expected error for used code")
		}
	})

	t.Run("authorization code expired", func(t *testing.T) {
		authCode := &AuthorizationCode{
			Code:        "expired-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			ExpiresAt:   time.Now().Add(-time.Hour),
			Used:        false,
		}

		storage := &mockStorage{
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:   "authorization_code",
			Code:        "expired-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
		})

		if err == nil {
			t.Error("expected error for expired code")
		}
	})

	t.Run("client_id mismatch", func(t *testing.T) {
		authCode := &AuthorizationCode{
			Code:        "valid-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			ExpiresAt:   time.Now().Add(10 * time.Minute),
			Used:        false,
		}

		storage := &mockStorage{
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:   "authorization_code",
			Code:        "valid-code",
			ClientID:    "other-client",
			RedirectURI: "http://localhost:8080/callback",
		})

		if err == nil {
			t.Error("expected error for client_id mismatch")
		}
	})

	t.Run("redirect_uri mismatch", func(t *testing.T) {
		authCode := &AuthorizationCode{
			Code:        "valid-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			ExpiresAt:   time.Now().Add(10 * time.Minute),
			Used:        false,
		}

		storage := &mockStorage{
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:   "authorization_code",
			Code:        "valid-code",
			ClientID:    "client-123",
			RedirectURI: "http://attacker.com/callback",
		})

		if err == nil {
			t.Error("expected error for redirect_uri mismatch")
		}
	})

	t.Run("invalid client credentials", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
		}
		authCode := &AuthorizationCode{
			Code:        "valid-code",
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			ExpiresAt:   time.Now().Add(10 * time.Minute),
			Used:        false,
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "authorization_code",
			Code:         "valid-code",
			ClientID:     "client-123",
			ClientSecret: "wrong-secret",
			RedirectURI:  "http://localhost:8080/callback",
		})

		if err == nil {
			t.Error("expected error for invalid client credentials")
		}
	})

	t.Run("PKCE verification with valid verifier", func(t *testing.T) {
		codeVerifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		codeChallenge, _ := GenerateCodeChallenge(codeVerifier, PKCEMethodS256)

		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
		}
		authCode := &AuthorizationCode{
			Code:          "pkce-code",
			ClientID:      "client-123",
			UserID:        "user-123",
			RedirectURI:   "http://localhost:8080/callback",
			CodeChallenge: codeChallenge,
			ExpiresAt:     time.Now().Add(10 * time.Minute),
			Used:          false,
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
			deleteAuthorizationCodeFunc: func(_ context.Context, _ string) error {
				return nil
			},
			saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
				return nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		resp, err := server.Token(ctx, TokenRequest{
			GrantType:    "authorization_code",
			Code:         "pkce-code",
			ClientID:     "client-123",
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/callback",
			CodeVerifier: codeVerifier,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.AccessToken == "" {
			t.Error("expected non-empty access token")
		}
	})

	t.Run("PKCE verification missing verifier", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
		}
		authCode := &AuthorizationCode{
			Code:          "pkce-code",
			ClientID:      "client-123",
			RedirectURI:   "http://localhost:8080/callback",
			CodeChallenge: "some-challenge",
			ExpiresAt:     time.Now().Add(10 * time.Minute),
			Used:          false,
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "authorization_code",
			Code:         "pkce-code",
			ClientID:     "client-123",
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/callback",
		})

		if err == nil {
			t.Error("expected error for missing code_verifier")
		}
	})

	t.Run("PKCE verification invalid verifier", func(t *testing.T) {
		correctVerifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		codeChallenge, _ := GenerateCodeChallenge(correctVerifier, PKCEMethodS256)

		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			Active:       true,
		}
		authCode := &AuthorizationCode{
			Code:          "pkce-code",
			ClientID:      "client-123",
			RedirectURI:   "http://localhost:8080/callback",
			CodeChallenge: codeChallenge,
			ExpiresAt:     time.Now().Add(10 * time.Minute),
			Used:          false,
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
				return authCode, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "authorization_code",
			Code:         "pkce-code",
			ClientID:     "client-123",
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/callback",
			CodeVerifier: "wrong-verifier-that-does-not-match",
		})

		if err == nil {
			t.Error("expected error for invalid code_verifier")
		}
	})
}

func TestHandleRefreshTokenGrant(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	t.Run("successful refresh token grant", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			Active:       true,
		}
		refreshToken := &RefreshToken{
			ID:         "token-id-1",
			Token:      "valid-refresh-token",
			ClientID:   "client-123",
			UserID:     "user-123",
			UserClaims: map[string]any{"role": "admin"},
			Scope:      "read",
			ExpiresAt:  time.Now().Add(24 * time.Hour),
			CreatedAt:  time.Now(),
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
				return refreshToken, nil
			},
			deleteRefreshTokenFunc: func(_ context.Context, _ string) error {
				return nil
			},
			saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
				return nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		resp, err := server.Token(ctx, TokenRequest{
			GrantType:    "refresh_token",
			RefreshToken: "valid-refresh-token",
			ClientID:     "client-123",
			ClientSecret: "secret",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.AccessToken == "" {
			t.Error("expected non-empty access token")
		}
		if resp.RefreshToken == "" {
			t.Error("expected non-empty refresh token (rotated)")
		}
	})

	t.Run("invalid refresh token", func(t *testing.T) {
		storage := &mockStorage{
			getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
				return nil, fmt.Errorf("not found")
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "refresh_token",
			RefreshToken: "invalid-token",
		})

		if err == nil {
			t.Error("expected error for invalid refresh token")
		}
	})

	t.Run("expired refresh token", func(t *testing.T) {
		refreshToken := &RefreshToken{
			Token:     "expired-token",
			ClientID:  "client-123",
			ExpiresAt: time.Now().Add(-time.Hour),
		}

		storage := &mockStorage{
			getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
				return refreshToken, nil
			},
			deleteRefreshTokenFunc: func(_ context.Context, _ string) error {
				return nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "refresh_token",
			RefreshToken: "expired-token",
			ClientID:     "client-123",
		})

		if err == nil {
			t.Error("expected error for expired refresh token")
		}
	})

	t.Run("client_id mismatch", func(t *testing.T) {
		refreshToken := &RefreshToken{
			Token:     "valid-token",
			ClientID:  "client-123",
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}

		storage := &mockStorage{
			getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
				return refreshToken, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "refresh_token",
			RefreshToken: "valid-token",
			ClientID:     "other-client",
		})

		if err == nil {
			t.Error("expected error for client_id mismatch")
		}
	})

	t.Run("invalid client credentials", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
		}
		refreshToken := &RefreshToken{
			Token:     "valid-token",
			ClientID:  "client-123",
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
				return refreshToken, nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		_, err := server.Token(ctx, TokenRequest{
			GrantType:    "refresh_token",
			RefreshToken: "valid-token",
			ClientID:     "client-123",
			ClientSecret: "wrong-secret",
		})

		if err == nil {
			t.Error("expected error for invalid client credentials")
		}
	})

	t.Run("refresh token with scope override", func(t *testing.T) {
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			Active:       true,
		}
		refreshToken := &RefreshToken{
			Token:     "valid-token",
			ClientID:  "client-123",
			UserID:    "user-123",
			Scope:     "read write",
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}

		storage := &mockStorage{
			getClientFunc: func(_ context.Context, _ string) (*Client, error) {
				return client, nil
			},
			getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
				return refreshToken, nil
			},
			deleteRefreshTokenFunc: func(_ context.Context, _ string) error {
				return nil
			},
			saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
				return nil
			},
		}
		server, _ := NewServer(ServerConfig{}, storage)

		resp, err := server.Token(ctx, TokenRequest{
			GrantType:    "refresh_token",
			RefreshToken: "valid-token",
			ClientID:     "client-123",
			ClientSecret: "secret",
			Scope:        "read",
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Scope != "read" {
			t.Errorf("expected scope 'read', got %q", resp.Scope)
		}
	})
}

func TestTokenEndpointBasicAuth(t *testing.T) {
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
	}
	authCode := &AuthorizationCode{
		Code:        "valid-code",
		ClientID:    "client-123",
		UserID:      "user-123",
		RedirectURI: "http://localhost:8080/callback",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
		Used:        false,
	}

	storage := &mockStorage{
		getClientFunc: func(_ context.Context, _ string) (*Client, error) {
			return client, nil
		},
		getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
			return authCode, nil
		},
		deleteAuthorizationCodeFunc: func(_ context.Context, _ string) error {
			return nil
		},
		saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
			return nil
		},
	}
	server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

	body := "grant_type=authorization_code&code=valid-code&redirect_uri=http://localhost:8080/callback"
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("client-123", "secret")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestStartCleanupRoutine(t *testing.T) {
	var mu sync.Mutex
	cleanupCodesCalled := false
	cleanupTokensCalled := false

	storage := &mockStorage{
		cleanupExpiredCodesFunc: func(_ context.Context) error {
			mu.Lock()
			cleanupCodesCalled = true
			mu.Unlock()
			return nil
		},
		cleanupExpiredTokensFunc: func(_ context.Context) error {
			mu.Lock()
			cleanupTokensCalled = true
			mu.Unlock()
			return nil
		},
	}
	server, _ := NewServer(ServerConfig{}, storage)

	ctx, cancel := context.WithCancel(context.Background())
	server.StartCleanupRoutine(ctx, 50*time.Millisecond)

	// Wait for at least one cleanup cycle
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Give the goroutine time to stop
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	codesCalled := cleanupCodesCalled
	tokensCalled := cleanupTokensCalled
	mu.Unlock()

	if !codesCalled {
		t.Error("expected cleanup expired codes to be called")
	}
	if !tokensCalled {
		t.Error("expected cleanup expired tokens to be called")
	}
}

func TestAuthorizeSaveCodeError(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
		RequirePKCE:  false,
	}
	storage := &mockStorage{
		getClientFunc: func(_ context.Context, _ string) (*Client, error) {
			return client, nil
		},
		saveAuthorizationCodeFunc: func(_ context.Context, _ *AuthorizationCode) error {
			return fmt.Errorf("database error")
		},
	}
	server, _ := NewServer(ServerConfig{}, storage)

	_, err := server.Authorize(ctx, AuthorizationRequest{
		ResponseType: "code",
		ClientID:     "client-123",
		RedirectURI:  "http://localhost:8080/callback",
		Scope:        "read",
	}, "user-123", nil)

	if err == nil {
		t.Error("expected error for save failure")
	}
}

func TestGenerateTokensSaveRefreshError(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
	}
	authCode := &AuthorizationCode{
		Code:        "valid-code",
		ClientID:    "client-123",
		UserID:      "user-123",
		RedirectURI: "http://localhost:8080/callback",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
		Used:        false,
	}

	storage := &mockStorage{
		getClientFunc: func(_ context.Context, _ string) (*Client, error) {
			return client, nil
		},
		getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
			return authCode, nil
		},
		deleteAuthorizationCodeFunc: func(_ context.Context, _ string) error {
			return nil
		},
		saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
			return fmt.Errorf("database error")
		},
	}
	server, _ := NewServer(ServerConfig{}, storage)

	_, err := server.Token(ctx, TokenRequest{
		GrantType:    "authorization_code",
		Code:         "valid-code",
		RedirectURI:  "http://localhost:8080/callback",
		ClientID:     "client-123",
		ClientSecret: "secret",
	})

	if err == nil {
		t.Error("expected error for save refresh token failure")
	}
}

func TestRefreshTokenDeleteIgnoresError(t *testing.T) {
	// Delete refresh token errors are ignored (the token rotation proceeds)
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		Active:       true,
	}
	refreshToken := &RefreshToken{
		Token:      "valid-token",
		ClientID:   "client-123",
		UserID:     "user-123",
		UserClaims: map[string]any{"role": "admin"},
		Scope:      "read",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}

	storage := &mockStorage{
		getClientFunc: func(_ context.Context, _ string) (*Client, error) {
			return client, nil
		},
		getRefreshTokenFunc: func(_ context.Context, _ string) (*RefreshToken, error) {
			return refreshToken, nil
		},
		deleteRefreshTokenFunc: func(_ context.Context, _ string) error {
			return fmt.Errorf("database error") // Error is ignored
		},
		saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
			return nil
		},
	}
	server, _ := NewServer(ServerConfig{}, storage)

	resp, err := server.Token(ctx, TokenRequest{
		GrantType:    "refresh_token",
		RefreshToken: "valid-token",
		ClientID:     "client-123",
		ClientSecret: "secret",
	})

	// Should succeed despite delete error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if resp.AccessToken == "" {
		t.Error("expected non-empty access token")
	}
}

func TestHandleRegisterEndpointStorageError(t *testing.T) {
	storage := &mockStorage{
		createClientFunc: func(_ context.Context, _ *Client) error {
			return fmt.Errorf("database error")
		},
	}
	server, _ := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		DCR:    DCRConfig{Enabled: true},
	}, storage)

	body := `{"client_name":"Test","redirect_uris":["http://localhost:8080"]}`
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	// Storage errors are returned as 400 (invalid_request) in this implementation
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestGenerateTokensSaveError(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	authCode := &AuthorizationCode{
		Code:        "valid-code",
		ClientID:    "client-123",
		RedirectURI: "http://localhost:8080/callback",
		UserID:      "user-123",
		Scope:       "read",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
	}

	storage := &mockStorage{
		getAuthorizationCodeFunc: func(_ context.Context, _ string) (*AuthorizationCode, error) {
			return authCode, nil
		},
		getClientFunc: func(_ context.Context, _ string) (*Client, error) {
			return client, nil
		},
		deleteAuthorizationCodeFunc: func(_ context.Context, _ string) error {
			return nil
		},
		saveRefreshTokenFunc: func(_ context.Context, _ *RefreshToken) error {
			return fmt.Errorf("database unavailable")
		},
	}
	server, _ := NewServer(ServerConfig{}, storage)

	_, err := server.Token(ctx, TokenRequest{
		GrantType:    "authorization_code",
		Code:         "valid-code",
		RedirectURI:  "http://localhost:8080/callback",
		ClientID:     "client-123",
		ClientSecret: "secret",
	})

	if err == nil {
		t.Error("expected error when SaveRefreshToken fails")
	}
	if !strings.Contains(err.Error(), "saving refresh token") {
		t.Errorf("expected 'saving refresh token' in error, got: %v", err)
	}
}

func TestValidatePKCEPlain(t *testing.T) {
	ctx := context.Background()
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	codeVerifier := "plain-code-verifier-123456"
	codeChallenge := codeVerifier // plain method uses verifier directly

	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
		RequirePKCE:  true,
	}

	storage := &mockStorage{
		getClientFunc: func(_ context.Context, _ string) (*Client, error) {
			return client, nil
		},
		saveAuthorizationCodeFunc: func(_ context.Context, _ *AuthorizationCode) error {
			return nil
		},
	}
	server, _ := NewServer(ServerConfig{}, storage)

	// Authorize with plain PKCE
	code, err := server.Authorize(ctx, AuthorizationRequest{
		ResponseType:        "code",
		ClientID:            "client-123",
		RedirectURI:         "http://localhost:8080/callback",
		Scope:               "read",
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: "plain",
	}, "user-123", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code == "" {
		t.Error("expected non-empty code")
	}
}
