package resource

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// csrfHeaderName mirrors browsersession.CSRFHeaderName. Duplicated as a literal
// here because pkg/middleware imports pkg/resource, so a resource test cannot
// import pkg/browsersession (which imports pkg/middleware) without a cycle.
const csrfHeaderName = "X-CSRF-Token"

// TestHandleForbiddenMapsTo403 verifies the handler maps ErrForbidden (a CSRF
// rejection) to 403, distinct from the 401 returned for other auth failures.
func TestHandleForbiddenMapsTo403(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	forbidden := func(_ *http.Request) (*Claims, error) { return nil, ErrForbidden }
	h := newTestHandler(store, s3, forbidden)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/resources/some-id", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for CSRF rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestResourcesCSRFEnforcement wires a CSRF-enforcing claims extractor (the
// same shape as cmd/main.go's extractClaims, which maps a CSRF failure to
// ErrForbidden) into the real resource handler and proves the resources
// surface rejects a state-changing request that lacks the token with 403,
// while a token-bearing mutation and a safe GET pass the auth gate.
func TestResourcesCSRFEnforcement(t *testing.T) {
	// Extractor mirroring cmd/main.go: a state-changing request without a
	// valid CSRF header is surfaced as ErrForbidden; safe methods are exempt.
	extract := func(r *http.Request) (*Claims, error) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return &Claims{IsAdmin: true}, nil
		default:
			if r.Header.Get(csrfHeaderName) != "valid-token" {
				return nil, ErrForbidden
			}
			return &Claims{IsAdmin: true}, nil
		}
	}
	h := newTestHandler(newMockStore(), newMockS3(), extract)

	t.Run("DELETE without CSRF token is 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/resources/some-id", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("DELETE with valid CSRF token passes the auth gate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/resources/some-id", http.NoBody)
		req.Header.Set(csrfHeaderName, "valid-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		// Auth passed: a missing id yields 404, not the 403/401 a blocked
		// request would produce.
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Fatalf("expected auth to pass, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET is exempt (safe method)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/some-id", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Fatalf("expected safe GET to pass auth, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
