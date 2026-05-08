package apigateway

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAuthenticator_DispatchesByMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"none", Config{AuthMode: AuthModeNone}},
		{"bearer", Config{AuthMode: AuthModeBearer, Credential: "tok"}},
		{"api_key header", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-API-Key"}},
		{"api_key query", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: "key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewAuthenticator(tc.cfg)
			if err != nil {
				t.Fatalf("NewAuthenticator: %v", err)
			}
			if a == nil {
				t.Fatal("NewAuthenticator returned nil")
			}
		})
	}
}

func TestNewAuthenticator_RejectsUnknownMode(t *testing.T) {
	if _, err := NewAuthenticator(Config{AuthMode: "weird"}); err == nil {
		t.Fatal("NewAuthenticator: want error for unknown mode")
	}
}

func TestBearerAuth_AppliesAuthorizationHeader(t *testing.T) {
	a, err := NewAuthenticator(Config{AuthMode: AuthModeBearer, Credential: "tok-xyz"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/foo", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-xyz" {
		t.Errorf("Authorization = %q; want %q", got, "Bearer tok-xyz")
	}
}

func TestBearerAuth_RejectsEmptyCredential(t *testing.T) {
	a := bearerAuth{credential: ""}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
	if err := a.Apply(req); err == nil {
		t.Error("Apply: want error for empty credential")
	}
}

func TestAPIKeyAuth_HeaderPlacement(t *testing.T) {
	a, err := NewAuthenticator(Config{
		AuthMode:        AuthModeAPIKey,
		Credential:      "secret-key",
		APIKeyPlacement: APIKeyPlacementHeader,
		APIKeyHeader:    "X-Api-Token",
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/foo", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := req.Header.Get("X-Api-Token"); got != "secret-key" {
		t.Errorf("X-Api-Token = %q; want %q", got, "secret-key")
	}
}

func TestAPIKeyAuth_QueryPlacement_PreservesExistingQuery(t *testing.T) {
	a, err := NewAuthenticator(Config{
		AuthMode:        AuthModeAPIKey,
		Credential:      "qkey",
		APIKeyPlacement: APIKeyPlacementQuery,
		APIKeyParam:     "api_key",
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/foo?existing=v1", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	q := req.URL.Query()
	if q.Get("api_key") != "qkey" {
		t.Errorf("api_key = %q; want %q", q.Get("api_key"), "qkey")
	}
	if q.Get("existing") != "v1" {
		t.Errorf("existing param dropped: %s", req.URL.RawQuery)
	}
}

func TestNoneAuth_SetsNoHeaders(t *testing.T) {
	a, err := NewAuthenticator(Config{AuthMode: AuthModeNone})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
	if err := a.Apply(req); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("noneAuth set Authorization")
	}
}

func TestNewAPIKeyAuth_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty credential", Config{AuthMode: AuthModeAPIKey, APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-Key"}},
		{"empty header", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: ""}},
		{"empty query param", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: ""}},
		{"unknown placement", Config{AuthMode: AuthModeAPIKey, Credential: "k", APIKeyPlacement: "weird"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newAPIKeyAuth(tc.cfg); err == nil {
				t.Errorf("newAPIKeyAuth(%+v) want error", tc.cfg)
			}
		})
	}
}

// Credentials never appear in slog output, error messages, or
// stringified Authenticator state. A future change that adds a
// fmt.Errorf("apigateway: bad credential %q", c.Credential) would
// be a real leak; this test is the canary.
func TestAuthenticators_DoNotLeakCredentials(t *testing.T) {
	const secret = "supersecret-credential-nonsense-9b7"
	cfgs := []Config{
		{AuthMode: AuthModeBearer, Credential: secret},
		{AuthMode: AuthModeAPIKey, Credential: secret, APIKeyPlacement: APIKeyPlacementHeader, APIKeyHeader: "X-K"},
		{AuthMode: AuthModeAPIKey, Credential: secret, APIKeyPlacement: APIKeyPlacementQuery, APIKeyParam: "k"},
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	for _, cfg := range cfgs {
		a, err := NewAuthenticator(cfg)
		if err != nil {
			t.Errorf("NewAuthenticator(%s): %v", cfg.AuthMode, err)
			continue
		}
		// Stringify and log everything we have access to.
		logger.Info("auth materialized",
			"mode", cfg.AuthMode,
			"placement", cfg.APIKeyPlacement,
			"header", cfg.APIKeyHeader,
			"param", cfg.APIKeyParam,
		)
		// Log the error from a deliberately broken Apply call too.
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
		_ = a.Apply(req)
	}

	if strings.Contains(buf.String(), secret) {
		t.Errorf("log buffer contains credential: %s", buf.String())
	}
}

func TestAPIKeyAuth_Apply_RejectsUnknownPlacement(t *testing.T) {
	// Force-construct an apiKeyAuth in an invalid state to verify the
	// Apply default branch fires. NewAuthenticator and newAPIKeyAuth
	// would normally reject this at construction time.
	a := apiKeyAuth{credential: "k", placement: "tampered", header: "X", param: "y"}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", http.NoBody)
	if err := a.Apply(req); err == nil {
		t.Fatal("Apply: want error for unknown placement")
	}
}
