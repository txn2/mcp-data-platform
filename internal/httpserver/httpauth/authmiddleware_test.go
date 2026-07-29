package httpauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

func TestAuthMiddleware(t *testing.T) {
	t.Run("extracts Bearer token", func(t *testing.T) {
		var extractedToken string
		handler := AuthMiddleware(false)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody)
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
		handler := MCPAuthGateway(nil, rmURL)(okHandler)

		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
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
		handler := MCPAuthGateway(nil, "")(okHandler)

		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
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
		handler := MCPAuthGateway(nil, "https://mcp.example.com/.well-known/oauth-protected-resource")(inner)

		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
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
		handler := MCPAuthGateway(nil, "https://mcp.example.com/.well-known/oauth-protected-resource")(inner)

		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
		req.Header.Set("X-API-Key", "some-api-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "some-api-key" {
			t.Errorf("expected token 'some-api-key' in context, got %q", extractedToken)
		}
	})

	t.Run("rejects Authorization header without Bearer prefix", func(t *testing.T) {
		handler := MCPAuthGateway(nil, "")(okHandler)

		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", http.NoBody)
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
		handler := RequireAuthWithOAuth(nil, rmURL)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/sse", http.NoBody)
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
		handler := RequireAuthWithOAuth(nil, "")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/sse", http.NoBody)
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
		handler := RequireAuthWithOAuth(nil, rmURL)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/sse", http.NoBody)
		req.Header.Set("Authorization", "Bearer my-oauth-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "my-oauth-token" { //nolint:gosec // G101: test fixture, not a credential
			t.Errorf("expected token 'my-oauth-token', got %q", extractedToken)
		}
	})

	t.Run("passes through and sets token with API key", func(t *testing.T) {
		var extractedToken string
		handler := RequireAuthWithOAuth(nil, rmURL)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			extractedToken = auth.GetToken(r.Context())
		}))

		req := httptest.NewRequestWithContext(context.Background(), "GET", "/sse", http.NoBody)
		req.Header.Set("X-API-Key", "my-api-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if extractedToken != "my-api-key" {
			t.Errorf("expected token 'my-api-key', got %q", extractedToken)
		}
	})
}

// mintGatewayJWT signs an HS256 token for the gateway validation tests. Signing
// with a key other than the authenticator's verification key yields a token that
// is syntactically a JWT but fails verification (an "unknown" token).
func mintGatewayJWT(t *testing.T, key []byte, issuer string, exp time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": "user-123",
		"aud": issuer,
		"exp": exp.Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}

// TestMCPGateways_ValidateToken covers issue #926: with the platform's
// authenticator wired in, both MCP HTTP gateways must reject a present-but-
// invalid token (expired, unknown-signature, malformed, or unrecognized) with
// HTTP 401 + WWW-Authenticate carrying error="invalid_token", while a valid
// credential — a JWT or an API key, including one whose value happens to be
// JWT-shaped — reaches the inner handler with its credential bridged into the
// request context. Absent credentials keep the pre-existing 401 without the
// invalid_token error code.
func TestMCPGateways_ValidateToken(t *testing.T) {
	signingKey := []byte("test-signing-key-at-least-32-bytes!!")
	issuer := "https://oauth.example.com"
	rmURL := "https://mcp.example.com/.well-known/oauth-protected-resource"

	// A JWT-shaped API key value (three dot-separated segments) guards against a
	// regression where a JWT-shaped key is misrouted to the JWT authenticator and
	// rejected before the API-key authenticator in the chain can accept it.
	apiKeyValue := "team.prod.k9f2x"
	oauthAuth, err := auth.NewOAuthJWTAuthenticator(auth.OAuthJWTConfig{
		Issuer:     issuer,
		SigningKey: signingKey,
	})
	if err != nil {
		t.Fatalf("building OAuth authenticator: %v", err)
	}
	apiKeyAuth := auth.NewAPIKeyAuthenticator(auth.APIKeyConfig{
		Keys: []auth.APIKey{{Key: apiKeyValue, Name: "svc", Roles: []string{"admin"}}},
	})
	// The same chain order production uses: JWT first, API key last.
	validator := auth.NewChainedAuthenticator(
		auth.ChainedAuthConfig{AllowAnonymous: false}, oauthAuth, apiKeyAuth)

	validToken := mintGatewayJWT(t, signingKey, issuer, time.Now().Add(time.Hour))
	expiredToken := mintGatewayJWT(t, signingKey, issuer, time.Now().Add(-time.Hour))
	unknownToken := mintGatewayJWT(t, []byte("a-different-signing-key-32-byteslong"), issuer, time.Now().Add(time.Hour))

	gateways := []struct {
		name string
		ctor func(middleware.Authenticator, string) func(http.Handler) http.Handler
	}{
		{"streamable", MCPAuthGateway},
		{"sse", RequireAuthWithOAuth},
	}

	cases := []struct {
		name        string
		token       string // bearer token value; "" means no Authorization header
		wantStatus  int
		wantReach   bool // inner handler should run
		wantInvalid bool // WWW-Authenticate should carry error="invalid_token"
	}{
		{"valid JWT reaches handler", validToken, http.StatusOK, true, false},
		{"valid JWT-shaped API key reaches handler", apiKeyValue, http.StatusOK, true, false},
		{"expired JWT rejected", expiredToken, http.StatusUnauthorized, false, true},
		{"unknown-signature JWT rejected", unknownToken, http.StatusUnauthorized, false, true},
		{"malformed JWT rejected", "aaa.bbb.ccc", http.StatusUnauthorized, false, true},
		{"unrecognized credential rejected", "not-a-known-credential", http.StatusUnauthorized, false, true},
		{"absent token rejected", "", http.StatusUnauthorized, false, false},
	}

	for _, gw := range gateways {
		for _, tc := range cases {
			t.Run(gw.name+"/"+tc.name, func(t *testing.T) {
				var reached bool
				var bridged string
				inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reached = true
					bridged = auth.GetToken(r.Context())
					w.WriteHeader(http.StatusOK)
				})
				handler := gw.ctor(validator, rmURL)(inner)

				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
				if tc.token != "" {
					req.Header.Set("Authorization", "Bearer "+tc.token)
				}
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)

				if rr.Code != tc.wantStatus {
					t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
				}
				if reached != tc.wantReach {
					t.Errorf("inner handler reached = %v, want %v", reached, tc.wantReach)
				}
				if tc.wantReach && bridged != tc.token {
					t.Errorf("bridged token = %q, want %q", bridged, tc.token)
				}
				if tc.wantStatus != http.StatusUnauthorized {
					return
				}
				www := rr.Header().Get("WWW-Authenticate")
				if !strings.Contains(www, `resource_metadata="`+rmURL+`"`) {
					t.Errorf("WWW-Authenticate = %q, want resource_metadata", www)
				}
				if gotInvalid := strings.Contains(www, `error="invalid_token"`); gotInvalid != tc.wantInvalid {
					t.Errorf("WWW-Authenticate = %q, invalid_token present = %v, want %v", www, gotInvalid, tc.wantInvalid)
				}
			})
		}
	}
}

// stubAuthenticator returns a fixed error (or a success when err is nil).
type stubAuthenticator struct{ err error }

func (s stubAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &middleware.UserInfo{UserID: "u"}, nil
}

// TestMCPGateways_FailOpenOnTransient asserts the gate fails OPEN (passes the
// request through to the protocol layer) when the authenticator reports a
// transient validation failure (middleware.ErrValidationUnavailable, e.g. the OIDC
// JWKS endpoint is unreachable), and fails CLOSED (401) on a definitive
// rejection. An IdP blip must not drop a possibly-valid client, while a
// genuinely invalid token is still rejected.
func TestMCPGateways_FailOpenOnTransient(t *testing.T) {
	rmURL := "https://mcp.example.com/.well-known/oauth-protected-resource"

	tests := []struct {
		name      string
		err       error
		wantReach bool
		wantCode  int
	}{
		{"transient failure fails open", fmt.Errorf("verifying token: %w", middleware.ErrValidationUnavailable), true, http.StatusOK},
		{"definitive failure fails closed", errors.New("invalid token"), false, http.StatusUnauthorized},
	}
	for _, gwName := range []string{"streamable", "sse"} {
		ctor := MCPAuthGateway
		if gwName == "sse" {
			ctor = RequireAuthWithOAuth
		}
		for _, tc := range tests {
			t.Run(gwName+"/"+tc.name, func(t *testing.T) {
				var reached bool
				inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					reached = true
					w.WriteHeader(http.StatusOK)
				})
				handler := ctor(stubAuthenticator{err: tc.err}, rmURL)(inner)

				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
				req.Header.Set("Authorization", "Bearer some.jwt.token")
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)

				if rr.Code != tc.wantCode {
					t.Fatalf("status = %d, want %d", rr.Code, tc.wantCode)
				}
				if reached != tc.wantReach {
					t.Errorf("inner handler reached = %v, want %v", reached, tc.wantReach)
				}
			})
		}
	}
}
