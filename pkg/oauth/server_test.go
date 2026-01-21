package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
