package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// errNoToken simulates token authentication failing, proving the decision
// in the browser-session case comes from the pre-authenticated cookie user
// rather than the token authenticator.
var errNoToken = errors.New("no token")

// stubAuthorizer is a minimal middleware.Authorizer for the proxy
// authorization tests.
type stubAuthorizer struct {
	allowed bool
	persona string
}

func (s stubAuthorizer) IsAuthorized(_ context.Context, _ string, _ []string, _, _ string) (authorized bool, personaName, reason string) {
	return s.allowed, s.persona, ""
}

// fakeCookieResolver stands in for *browsersession.Authenticator so the
// browser-auth middleware can be exercised without minting a signed cookie.
type fakeCookieResolver struct {
	info *middleware.UserInfo
	err  error
}

func (f fakeCookieResolver) AuthenticateHTTP(_ *http.Request) (*middleware.UserInfo, error) {
	return f.info, f.err
}

// TestObservabilityAuthorizer_HonorsBrowserSessionUser is the regression
// guard for the v1.69.0 bug where the portal's API Gateway and Health tabs
// (which call /api/v1/observability/*) bounced cookie-authenticated admins
// to the login page: the proxy authorizer only read header tokens, so a
// browser-session request with no Bearer/API-key header resolved no
// identity and the proxy returned 401.
func TestObservabilityAuthorizer_HonorsBrowserSessionUser(t *testing.T) {
	authz := observabilityAuthorizer{
		authn: stubAuthenticator{err: errNoToken}, // token auth would fail
		authz: stubAuthorizer{allowed: true, persona: "admin"},
	}

	// Simulate observabilityBrowserAuth having resolved a session cookie.
	ctx := middleware.WithPreAuthenticatedUser(context.Background(), &middleware.UserInfo{
		UserID: "u-1",
		Email:  "admin@example.com",
		Roles:  []string{"admin"},
	})

	dec := authz.Authorize(ctx)
	if !dec.Authenticated {
		t.Fatal("Authenticated=false for browser-session user: proxy would 401 and redirect to login")
	}
	if !dec.Allowed {
		t.Fatal("Allowed=false: admin persona with observability:read should be permitted")
	}
	if dec.UserID != "u-1" || dec.Email != "admin@example.com" {
		t.Fatalf("identity mismatch: %+v", dec)
	}
	if dec.Persona != "admin" {
		t.Fatalf("persona = %q, want admin", dec.Persona)
	}
}

// TestObservabilityAuthorizer_FallsBackToToken verifies the token path still
// works when no browser-session user is present (programmatic callers).
func TestObservabilityAuthorizer_FallsBackToToken(t *testing.T) {
	authz := observabilityAuthorizer{
		authn: stubAuthenticator{info: &middleware.UserInfo{UserID: "svc", Roles: []string{"admin"}}},
		authz: stubAuthorizer{allowed: true, persona: "admin"},
	}

	dec := authz.Authorize(context.Background())
	if !dec.Authenticated || dec.UserID != "svc" {
		t.Fatalf("token fallback failed: %+v", dec)
	}
}

// TestObservabilityAuthorizer_NoCredentials verifies the authorizer denies
// when neither a cookie user nor a token resolves, so the proxy returns 401.
func TestObservabilityAuthorizer_NoCredentials(t *testing.T) {
	authz := observabilityAuthorizer{
		authn: stubAuthenticator{err: errNoToken},
		authz: stubAuthorizer{allowed: true},
	}

	if dec := authz.Authorize(context.Background()); dec.Authenticated {
		t.Fatal("Authenticated=true with no credentials; want false")
	}
}

// TestObservabilityAuthorizer_NilAuthnNoCookie covers the branch where no
// token authenticator is wired and there is no cookie user.
func TestObservabilityAuthorizer_NilAuthnNoCookie(t *testing.T) {
	authz := observabilityAuthorizer{authn: nil, authz: stubAuthorizer{allowed: true}}
	if dec := authz.Authorize(context.Background()); dec.Authenticated {
		t.Fatal("Authenticated=true with nil authenticator and no cookie; want false")
	}
}

// TestObservabilityBrowserAuth verifies the middleware lifts a resolved
// cookie user onto the context and otherwise leaves it untouched.
func TestObservabilityBrowserAuth(t *testing.T) {
	cases := []struct {
		name     string
		resolver browserCookieResolver
		wantUser bool
	}{
		{"cookie resolves user", fakeCookieResolver{info: &middleware.UserInfo{UserID: "u1"}}, true},
		{"no cookie", fakeCookieResolver{}, false},
		{"resolver error", fakeCookieResolver{err: errNoToken}, false},
		{"nil resolver", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *middleware.UserInfo
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = middleware.GetPreAuthenticatedUser(r.Context())
				w.WriteHeader(http.StatusOK)
			})
			h := observabilityBrowserAuth(tc.resolver)(next)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/observability/query", http.NoBody))

			if tc.wantUser && got == nil {
				t.Fatal("expected pre-authenticated user on context, got none")
			}
			if !tc.wantUser && got != nil {
				t.Fatalf("expected no pre-authenticated user, got %+v", got)
			}
			if rr.Code != http.StatusOK {
				t.Fatalf("next handler not reached: status %d", rr.Code)
			}
		})
	}
}

// TestObservabilityAuthMiddleware covers both the nil and non-nil
// browser-session-authenticator branches of the platform accessor.
func TestObservabilityAuthMiddleware(t *testing.T) {
	serve := func(mw func(http.Handler) http.Handler) bool {
		served := false
		next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { served = true })
		mw(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody))
		return served
	}

	if !serve((&Platform{}).ObservabilityAuthMiddleware()) {
		t.Fatal("nil browser-session path did not reach next handler")
	}

	// A zero-value authenticator is non-nil; with no cookie it resolves
	// nobody, so the request still passes through.
	p := &Platform{browserSessionAuth: &browsersession.Authenticator{}}
	if !serve(p.ObservabilityAuthMiddleware()) {
		t.Fatal("non-nil browser-session path did not reach next handler")
	}
}
