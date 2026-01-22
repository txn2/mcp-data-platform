package http

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

		req := httptest.NewRequest("GET", "/", nil)
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

		req := httptest.NewRequest("GET", "/", nil)
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

		req := httptest.NewRequest("GET", "/", nil)
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

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("allows request when auth not required and no token", func(t *testing.T) {
		handlerCalled := false
		handler := AuthMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/", nil)
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

		req := httptest.NewRequest("GET", "/", nil)
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

	req := httptest.NewRequest("GET", "/", nil)
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

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Error("OptionalAuth should call handler without token")
	}
}
