package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestHandleAuthorizeEndpoint(t *testing.T) {
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	client := &Client{
		ClientID:     "client-123",
		ClientSecret: string(hashedSecret),
		RedirectURIs: []string{"http://localhost:8080/callback"},
		Active:       true,
		RequirePKCE:  true,
	}

	t.Run("method not allowed", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       "http://keycloak:8180/realms/test",
				ClientID:     "mcp-server",
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("invalid client", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       "http://keycloak:8180/realms/test",
				ClientID:     "mcp-server",
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=invalid&redirect_uri=http://localhost:8080/callback", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("missing PKCE", func(t *testing.T) {
		storage := NewMemoryStorage()
		_ = storage.CreateClient(context.Background(), client)
		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       "http://keycloak:8180/realms/test",
				ClientID:     "mcp-server",
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("no upstream configured", func(t *testing.T) {
		storage := NewMemoryStorage()
		clientNoPKCE := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
			RequirePKCE:  false,
		}
		_ = storage.CreateClient(context.Background(), clientNoPKCE)
		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			// No Upstream configured
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected status 500, got %d", w.Code)
		}
	})

	t.Run("successful redirect to upstream", func(t *testing.T) {
		storage := NewMemoryStorage()
		clientNoPKCE := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
			RequirePKCE:  false,
		}
		_ = storage.CreateClient(context.Background(), clientNoPKCE)
		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       "http://keycloak:8180/realms/test",
				ClientID:     "mcp-server",
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback&state=mystate", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusFound {
			t.Errorf("expected status 302, got %d", w.Code)
		}

		location := w.Header().Get("Location")
		if location == "" {
			t.Error("expected Location header")
		}

		// Verify redirect URL points to Keycloak
		u, _ := url.Parse(location)
		if u.Host != "keycloak:8180" {
			t.Errorf("expected redirect to keycloak, got %s", u.Host)
		}
		if u.Query().Get("client_id") != "mcp-server" {
			t.Errorf("expected client_id=mcp-server, got %s", u.Query().Get("client_id"))
		}
	})
}

func TestHandleCallbackEndpoint(t *testing.T) {
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	t.Run("method not allowed", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodPost, "/oauth/callback", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("error from upstream", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=access_denied&error_description=User+denied+access", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("missing code or state", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=abc", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("invalid state", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=abc&state=invalid", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("successful callback with mock keycloak", func(t *testing.T) {
		// Create a mock Keycloak server
		mockKeycloak := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
				// Return mock token response
				idToken := createMockIDToken(map[string]any{
					"sub":   "user-123",
					"email": "test@example.com",
					"name":  "Test User",
				})
				resp := map[string]any{
					"access_token":  "mock-access-token",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"refresh_token": "mock-refresh-token",
					"id_token":      idToken,
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			http.NotFound(w, r)
		}))
		defer mockKeycloak.Close()

		storage := NewMemoryStorage()
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
			RequirePKCE:  false,
		}
		_ = storage.CreateClient(context.Background(), client)

		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       mockKeycloak.URL + "/realms/test",
				ClientID:     "mcp-server",
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		// Save a state to simulate the flow
		state := &AuthorizationState{
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			State:       "client-state",
			CreatedAt:   time.Now(),
		}
		_ = server.stateStore.Save("upstream-state", state)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=keycloak-code&state=upstream-state", nil)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusFound {
			t.Errorf("expected status 302, got %d: %s", w.Code, w.Body.String())
		}

		location := w.Header().Get("Location")
		if location == "" {
			t.Error("expected Location header")
		}

		// Verify redirect back to client
		u, _ := url.Parse(location)
		if u.Query().Get("code") == "" {
			t.Error("expected code in redirect URL")
		}
		if u.Query().Get("state") != "client-state" {
			t.Errorf("expected state=client-state, got %s", u.Query().Get("state"))
		}
	})
}

func TestBuildUpstreamAuthURL(t *testing.T) {
	storage := NewMemoryStorage()
	server, _ := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:       "http://keycloak:8180/realms/test",
			ClientID:     "mcp-server",
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, storage)

	authURL := server.buildUpstreamAuthURL("test-state")

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("invalid URL: %v", err)
	}

	if u.Host != "keycloak:8180" {
		t.Errorf("expected host keycloak:8180, got %s", u.Host)
	}
	if u.Path != "/realms/test/protocol/openid-connect/auth" {
		t.Errorf("unexpected path: %s", u.Path)
	}
	if u.Query().Get("response_type") != "code" {
		t.Errorf("expected response_type=code")
	}
	if u.Query().Get("client_id") != "mcp-server" {
		t.Errorf("expected client_id=mcp-server")
	}
	if u.Query().Get("state") != "test-state" {
		t.Errorf("expected state=test-state")
	}
}

func TestBuildClientRedirectURL(t *testing.T) {
	storage := NewMemoryStorage()
	server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

	t.Run("with state", func(t *testing.T) {
		result := server.buildClientRedirectURL("http://localhost:8080/callback", "code123", "state456")
		u, _ := url.Parse(result)
		if u.Query().Get("code") != "code123" {
			t.Errorf("expected code=code123, got %s", u.Query().Get("code"))
		}
		if u.Query().Get("state") != "state456" {
			t.Errorf("expected state=state456, got %s", u.Query().Get("state"))
		}
	})

	t.Run("without state", func(t *testing.T) {
		result := server.buildClientRedirectURL("http://localhost:8080/callback", "code123", "")
		u, _ := url.Parse(result)
		if u.Query().Get("code") != "code123" {
			t.Errorf("expected code=code123")
		}
		if u.Query().Get("state") != "" {
			t.Errorf("expected no state, got %s", u.Query().Get("state"))
		}
	})

	t.Run("preserves existing query params", func(t *testing.T) {
		result := server.buildClientRedirectURL("http://localhost:8080/callback?foo=bar", "code123", "state456")
		u, _ := url.Parse(result)
		if u.Query().Get("foo") != "bar" {
			t.Errorf("expected foo=bar preserved")
		}
	})
}

func TestDecodeJWTClaims(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		token := createMockIDToken(map[string]any{
			"sub":   "user-123",
			"email": "test@example.com",
		})

		claims := decodeJWTClaims(token)
		if claims == nil {
			t.Fatal("expected claims")
		}
		if claims["sub"] != "user-123" {
			t.Errorf("expected sub=user-123, got %v", claims["sub"])
		}
	})

	t.Run("invalid token format", func(t *testing.T) {
		claims := decodeJWTClaims("not.a.valid.token.format")
		if claims != nil {
			t.Error("expected nil for invalid token")
		}
	})

	t.Run("invalid base64", func(t *testing.T) {
		claims := decodeJWTClaims("header.!!!invalid!!!.signature")
		if claims != nil {
			t.Error("expected nil for invalid base64")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		invalidPayload := base64.RawURLEncoding.EncodeToString([]byte("not json"))
		claims := decodeJWTClaims("header." + invalidPayload + ".signature")
		if claims != nil {
			t.Error("expected nil for invalid JSON")
		}
	})
}

func TestExtractUserFromUpstreamToken(t *testing.T) {
	storage := NewMemoryStorage()
	server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

	t.Run("with ID token", func(t *testing.T) {
		token := &upstreamTokenResponse{
			AccessToken: "access-token",
			IDToken: createMockIDToken(map[string]any{
				"sub":   "user-123",
				"email": "test@example.com",
			}),
		}

		userID, claims := server.extractUserFromUpstreamToken(token)
		if userID != "user-123" {
			t.Errorf("expected userID=user-123, got %s", userID)
		}
		if claims["email"] != "test@example.com" {
			t.Errorf("expected email in claims")
		}
	})

	t.Run("without ID token", func(t *testing.T) {
		token := &upstreamTokenResponse{
			AccessToken: "access-token",
		}

		userID, claims := server.extractUserFromUpstreamToken(token)
		if userID != "unknown" {
			t.Errorf("expected userID=unknown, got %s", userID)
		}
		if len(claims) != 0 {
			t.Errorf("expected empty claims")
		}
	})
}

// createMockIDToken creates a mock JWT ID token for testing.
func createMockIDToken(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	signature := base64.RawURLEncoding.EncodeToString([]byte("mock-signature"))
	return header + "." + payloadB64 + "." + signature
}
