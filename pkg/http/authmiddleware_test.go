package http //nolint:revive // var-naming: intentional package name to match directory

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/auth"
)

func TestAuthMiddleware(t *testing.T) {
	t.Run("extracts Bearer token", func(t *testing.T) {
		var extractedToken string
		handler := AuthMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer test-token-123")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "test-token-123" {
			t.Errorf("expected token 'test-token-123', got %q", extractedToken)
		}
	})

	t.Run("extracts X-API-Key header", func(t *testing.T) {
		var extractedToken string
		handler := AuthMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.Header.Set("X-API-Key", "api-key-456")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "api-key-456" {
			t.Errorf("expected token 'api-key-456', got %q", extractedToken)
		}
	})

	t.Run("prefers Bearer over X-API-Key", func(t *testing.T) {
		var extractedToken string
		handler := AuthMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer bearer-token")
		req.Header.Set("X-API-Key", "api-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "bearer-token" {
			t.Errorf("expected Bearer token to take precedence, got %q", extractedToken)
		}
	})

	t.Run("returns 401 when auth required and no token", func(t *testing.T) {
		handler := AuthMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})
}

func TestAuthMiddleware_AllowsAndRequires(t *testing.T) {
	t.Run("allows request when auth not required and no token", func(t *testing.T) {
		handlerCalled := false
		handler := AuthMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if !handlerCalled {
			t.Error("expected handler to be called")
		}
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
	})

	t.Run("allows request when auth required and valid token", func(t *testing.T) {
		handlerCalled := false
		handler := AuthMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer valid-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if !handlerCalled {
			t.Error("expected handler to be called")
		}
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
	})
}

func TestRequireAuth(t *testing.T) {
	handler := RequireAuth()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("RequireAuth should return 401 without token, got %d", rr.Code)
	}
}

func TestOptionalAuth(t *testing.T) {
	handlerCalled := false
	handler := OptionalAuth()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", http.NoBody)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Error("OptionalAuth should call handler without token")
	}
}

func TestMCPAuthGateway(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("returns 401 with WWW-Authenticate when no credentials", func(t *testing.T) {
		rmURL := "https://mcp.example.com/.well-known/oauth-protected-resource"
		handler := MCPAuthGateway(rmURL)(okHandler)

		req := httptest.NewRequest("POST", "/", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
		wwwAuth := rr.Header().Get("WWW-Authenticate")
		expected := `Bearer resource_metadata="` + rmURL + `"`
		if wwwAuth != expected {
			t.Errorf("WWW-Authenticate header = %q, want %q", wwwAuth, expected)
		}
	})

	t.Run("returns 401 with plain Bearer when no resource metadata URL", func(t *testing.T) {
		handler := MCPAuthGateway("")(okHandler)

		req := httptest.NewRequest("POST", "/", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
		if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate header = %q, want %q", got, "Bearer")
		}
	})

	t.Run("passes through with Bearer token and bridges to context", func(t *testing.T) {
		var extractedToken string
		inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		})
		handler := MCPAuthGateway("https://mcp.example.com/.well-known/oauth-protected-resource")(inner)

		req := httptest.NewRequest("POST", "/", http.NoBody)
		req.Header.Set("Authorization", "Bearer some-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "some-token" {
			t.Errorf("expected token 'some-token' in context, got %q", extractedToken)
		}
	})

	t.Run("passes through with API key and bridges to context", func(t *testing.T) {
		var extractedToken string
		inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		})
		handler := MCPAuthGateway("https://mcp.example.com/.well-known/oauth-protected-resource")(inner)

		req := httptest.NewRequest("POST", "/", http.NoBody)
		req.Header.Set("X-API-Key", "some-api-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "some-api-key" {
			t.Errorf("expected token 'some-api-key' in context, got %q", extractedToken)
		}
	})

	t.Run("rejects Authorization header without Bearer prefix", func(t *testing.T) {
		handler := MCPAuthGateway("")(okHandler)

		req := httptest.NewRequest("POST", "/", http.NoBody)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401 for Basic auth, got %d", rr.Code)
		}
	})
}

func TestRequireAuthWithOAuth(t *testing.T) {
	rmURL := "https://mcp.example.com/.well-known/oauth-protected-resource"

	t.Run("returns 401 with WWW-Authenticate when no token", func(t *testing.T) {
		handler := RequireAuthWithOAuth(rmURL)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/sse", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
		wwwAuth := rr.Header().Get("WWW-Authenticate")
		expected := `Bearer resource_metadata="` + rmURL + `"`
		if wwwAuth != expected {
			t.Errorf("WWW-Authenticate header = %q, want %q", wwwAuth, expected)
		}
	})

	t.Run("returns 401 with plain Bearer when no resource metadata URL", func(t *testing.T) {
		handler := RequireAuthWithOAuth("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/sse", http.NoBody)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
		if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate header = %q, want %q", got, "Bearer")
		}
	})

	t.Run("passes through and sets token with Bearer", func(t *testing.T) {
		var extractedToken string
		handler := RequireAuthWithOAuth(rmURL)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequest("GET", "/sse", http.NoBody)
		req.Header.Set("Authorization", "Bearer my-oauth-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "my-oauth-token" { //nolint:gosec // G101: test fixture, not a credential
			t.Errorf("expected token 'my-oauth-token', got %q", extractedToken)
		}
	})

	t.Run("passes through and sets token with API key", func(t *testing.T) {
		var extractedToken string
		handler := RequireAuthWithOAuth(rmURL)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequest("GET", "/sse", http.NoBody)
		req.Header.Set("X-API-Key", "my-api-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "my-api-key" {
			t.Errorf("expected token 'my-api-key', got %q", extractedToken)
		}
	})
}
