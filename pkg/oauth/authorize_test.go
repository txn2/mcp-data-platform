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

const (
	testKeycloakHost   = "keycloak:8180"
	testUpstreamClient = "mcp-server"
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
				ClientID:     testUpstreamClient,
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", http.NoBody)
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
				ClientID:     testUpstreamClient,
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=invalid&redirect_uri=http://localhost:8080/callback", http.NoBody)
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
				ClientID:     testUpstreamClient,
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback", http.NoBody)
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

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback", http.NoBody)
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
				ClientID:     testUpstreamClient,
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=client-123&redirect_uri=http://localhost:8080/callback&state=mystate", http.NoBody)
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
		if u.Host != testKeycloakHost {
			t.Errorf("expected redirect to keycloak, got %s", u.Host)
		}
		if u.Query().Get("client_id") != testUpstreamClient {
			t.Errorf("expected client_id=mcp-server, got %s", u.Query().Get("client_id"))
		}
	})
}

func TestHandleCallbackEndpoint(t *testing.T) {
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	t.Run("method not allowed", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodPost, "/oauth/callback", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", w.Code)
		}
	})

	t.Run("error from upstream", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=access_denied&error_description=User+denied+access", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("missing code or state", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=abc", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("invalid state", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{Issuer: "http://localhost:8080"}, storage)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=abc&state=invalid", http.NoBody)
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
				ClientID:     testUpstreamClient,
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

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=keycloak-code&state=upstream-state", http.NoBody)
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
			ClientID:     testUpstreamClient,
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, storage)

	authURL := server.buildUpstreamAuthURL("test-state")

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("invalid URL: %v", err)
	}

	if u.Host != testKeycloakHost {
		t.Errorf("expected host keycloak:8180, got %s", u.Host)
	}
	if u.Path != "/realms/test/protocol/openid-connect/auth" {
		t.Errorf("unexpected path: %s", u.Path)
	}
	if u.Query().Get("response_type") != "code" {
		t.Errorf("expected response_type=code")
	}
	if u.Query().Get("client_id") != testUpstreamClient {
		t.Errorf("expected client_id=mcp-server")
	}
	if u.Query().Get("state") != "test-state" {
		t.Errorf("expected state=test-state")
	}
	if u.Query().Get("prompt") != "none" {
		t.Errorf("expected prompt=none, got %s", u.Query().Get("prompt"))
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

func TestBuildUpstreamAuthURLWithPrompt(t *testing.T) {
	storage := NewMemoryStorage()
	server, _ := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:       "http://keycloak:8180/realms/test",
			ClientID:     testUpstreamClient,
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, storage)

	t.Run("with prompt=none", func(t *testing.T) {
		authURL := server.buildUpstreamAuthURLWithPrompt("test-state", true)
		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("invalid URL: %v", err)
		}
		if u.Query().Get("prompt") != "none" {
			t.Errorf("expected prompt=none, got %q", u.Query().Get("prompt"))
		}
		if u.Query().Get("state") != "test-state" {
			t.Errorf("expected state=test-state, got %q", u.Query().Get("state"))
		}
	})

	t.Run("without prompt", func(t *testing.T) {
		authURL := server.buildUpstreamAuthURLWithPrompt("test-state", false)
		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("invalid URL: %v", err)
		}
		if u.Query().Get("prompt") != "" {
			t.Errorf("expected no prompt param, got %q", u.Query().Get("prompt"))
		}
		if u.Query().Get("client_id") != testUpstreamClient {
			t.Errorf("expected client_id=mcp-server, got %q", u.Query().Get("client_id"))
		}
	})
}

func TestHandleLoginRequiredError(t *testing.T) {
	storage := NewMemoryStorage()
	server, _ := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		Upstream: &UpstreamConfig{
			Issuer:       "http://keycloak:8180/realms/test",
			ClientID:     testUpstreamClient,
			ClientSecret: "secret",
			RedirectURI:  "http://localhost:8080/oauth/callback",
		},
	}, storage)

	t.Run("handles login_required with redirect", func(t *testing.T) {
		state := &AuthorizationState{
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			State:       "client-state",
			CreatedAt:   time.Now(),
		}
		_ = server.stateStore.Save("upstream-state", state)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=login_required&state=upstream-state", http.NoBody)
		w := httptest.NewRecorder()

		handled := server.handleLoginRequiredError(w, req)
		if !handled {
			t.Fatal("expected handleLoginRequiredError to return true")
		}
		if w.Code != http.StatusFound {
			t.Errorf("expected 302, got %d", w.Code)
		}

		location := w.Header().Get("Location")
		u, err := url.Parse(location)
		if err != nil {
			t.Fatalf("invalid redirect URL: %v", err)
		}
		// Should NOT have prompt=none on the retry
		if u.Query().Get("prompt") != "" {
			t.Errorf("expected no prompt param on retry, got %q", u.Query().Get("prompt"))
		}
		if u.Host != testKeycloakHost {
			t.Errorf("expected redirect to keycloak, got %s", u.Host)
		}

		// Verify state was marked as attempted
		savedState, _ := server.stateStore.Get("upstream-state")
		if !savedState.PromptNoneAttempted {
			t.Error("expected PromptNoneAttempted to be true")
		}
	})

	t.Run("returns false for other errors", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=access_denied&state=some-state", http.NoBody)
		w := httptest.NewRecorder()

		handled := server.handleLoginRequiredError(w, req)
		if handled {
			t.Error("expected false for non-login_required error")
		}
	})

	t.Run("returns false when state is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=login_required", http.NoBody)
		w := httptest.NewRecorder()

		handled := server.handleLoginRequiredError(w, req)
		if handled {
			t.Error("expected false when state param is empty")
		}
	})

	t.Run("returns false when state not found in store", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=login_required&state=nonexistent", http.NoBody)
		w := httptest.NewRecorder()

		handled := server.handleLoginRequiredError(w, req)
		if handled {
			t.Error("expected false when state not found in store")
		}
	})

	t.Run("prevents infinite loop when already attempted", func(t *testing.T) {
		state := &AuthorizationState{
			ClientID:            "client-123",
			RedirectURI:         "http://localhost:8080/callback",
			State:               "client-state",
			PromptNoneAttempted: true, // Already attempted
			CreatedAt:           time.Now(),
		}
		_ = server.stateStore.Save("loop-state", state)

		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=login_required&state=loop-state", http.NoBody)
		w := httptest.NewRecorder()

		handled := server.handleLoginRequiredError(w, req)
		if handled {
			t.Error("expected false to prevent infinite redirect loop")
		}
	})
}

func TestHandleCallbackEndpointLoginRequired(t *testing.T) {
	hashedSecret, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)

	t.Run("login_required retry redirects to upstream", func(t *testing.T) {
		storage := NewMemoryStorage()
		client := &Client{
			ClientID:     "client-123",
			ClientSecret: string(hashedSecret),
			RedirectURIs: []string{"http://localhost:8080/callback"},
			Active:       true,
		}
		_ = storage.CreateClient(context.Background(), client)

		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       "http://keycloak:8180/realms/test",
				ClientID:     testUpstreamClient,
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		// Save state simulating initial prompt=none attempt
		state := &AuthorizationState{
			ClientID:    "client-123",
			RedirectURI: "http://localhost:8080/callback",
			State:       "client-state",
			CreatedAt:   time.Now(),
		}
		_ = server.stateStore.Save("upstream-state", state)

		// Simulate Keycloak returning login_required (user has no session)
		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=login_required&state=upstream-state", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
		}

		location := w.Header().Get("Location")
		u, err := url.Parse(location)
		if err != nil {
			t.Fatalf("invalid redirect URL: %v", err)
		}

		// Should redirect to Keycloak without prompt=none
		if u.Host != testKeycloakHost {
			t.Errorf("expected redirect to keycloak, got %s", u.Host)
		}
		if u.Query().Get("prompt") != "" {
			t.Errorf("expected no prompt on retry, got %q", u.Query().Get("prompt"))
		}
		if u.Query().Get("state") != "upstream-state" {
			t.Errorf("expected same upstream state, got %q", u.Query().Get("state"))
		}
	})

	t.Run("login_required loop prevention falls through to error", func(t *testing.T) {
		storage := NewMemoryStorage()
		server, _ := NewServer(ServerConfig{
			Issuer: "http://localhost:8080",
			Upstream: &UpstreamConfig{
				Issuer:       "http://keycloak:8180/realms/test",
				ClientID:     testUpstreamClient,
				ClientSecret: "secret",
				RedirectURI:  "http://localhost:8080/oauth/callback",
			},
		}, storage)

		// Save state with PromptNoneAttempted already true
		state := &AuthorizationState{
			ClientID:            "client-123",
			RedirectURI:         "http://localhost:8080/callback",
			State:               "client-state",
			PromptNoneAttempted: true,
			CreatedAt:           time.Now(),
		}
		_ = server.stateStore.Save("loop-state", state)

		// login_required again after retry — should fall through to generic error handler
		req := httptest.NewRequest(http.MethodGet, "/oauth/callback?error=login_required&state=loop-state", http.NoBody)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)

		// Should fall through to generic error response (400)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 on loop prevention, got %d: %s", w.Code, w.Body.String())
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
