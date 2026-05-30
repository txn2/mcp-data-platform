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
