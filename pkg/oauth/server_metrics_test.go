package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func scrapeForTest(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// TestSetMetrics_RecordsTokenOutcomes drives the real Token method on both grant
// types. The grants fail (empty storage), but Token records the outcome on both
// the success and failure paths, so the issuance and refresh series must appear.
func TestSetMetrics_RecordsTokenOutcomes(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	srv, err := NewServer(ServerConfig{
		Issuer:          "https://issuer.example.com",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
		AuthCodeTTL:     10 * time.Minute,
	}, &mockStorage{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.SetMetrics(m)

	ctx := context.Background()
	_, _ = srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: "missing", ClientID: "c"})
	_, _ = srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: "missing", ClientID: "c"})

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		"oauth_token_issuance_total",
		`grant_type="authorization_code"`,
		"oauth_token_refresh_total",
		"oauth_token_refresh_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestSetMetrics_NilSafeToken confirms the token path runs with a nil recorder.
func TestSetMetrics_NilSafeToken(t *testing.T) {
	srv, err := NewServer(ServerConfig{Issuer: "https://i", AccessTokenTTL: time.Hour}, &mockStorage{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.SetMetrics(nil)
	_, _ = srv.Token(context.Background(), TokenRequest{GrantType: "refresh_token", RefreshToken: "x"})
}

// TestSetMetrics_RecordsRegistrationsAndUnsupportedGrants counts dynamic client
// registration by result and an unsupported grant under one value, never the
// grant_type the client sent (#1898).
func TestSetMetrics_RecordsRegistrationsAndUnsupportedGrants(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	srv, err := NewServer(ServerConfig{
		Issuer: "http://localhost:8080",
		DCR:    DCRConfig{Enabled: true, AllowedRedirectPatterns: []string{`^http://localhost.*`}},
	}, &mockStorage{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.SetMetrics(m)

	for _, body := range []string{testDCRRequestBody, `{"client_name":"x","redirect_uris":["https://elsewhere.example.com"]}`} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(httptest.NewRecorder(), req)
	}
	_, _ = srv.Token(context.Background(), TokenRequest{GrantType: "client_credentials_made_up"})

	body := scrapeForTest(t, m.Handler())
	for _, want := range []string{
		`oauth_client_registrations_total{result="success"} 1`,
		`oauth_client_registrations_total{result="failure"} 1`,
		`oauth_token_issuance_total{grant_type="unsupported",status="client_err"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	if strings.Contains(body, "client_credentials_made_up") {
		t.Error("a client-chosen grant_type became a label value")
	}
}
