package browsersession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// TestCallbackHandler_CountsRefusedSignIns counts a browser sign-in the
// identity provider refused and one that arrived malformed under
// auth_attempts_total{method="browser"} (#1898).
func TestCallbackHandler_CountsRefusedSignIns(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := opsobs.Metrics()
	opsobs.SetMetrics(m)
	t.Cleanup(func() { opsobs.SetMetrics(prev) })

	srv := mockOIDCProvider(t, nil)
	defer srv.Close()
	cfg := testFlowConfig(srv.URL)
	cfg.HTTPClient = srv.Client()
	flow, err := NewFlow(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewFlow: %v", err)
	}
	for _, q := range []string{"?error=access_denied", "?state=abc", "?code=c&state=s"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/portal/auth/callback"+q, http.NoBody)
		flow.CallbackHandler(httptest.NewRecorder(), req)
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`auth_attempts_total{method="browser",reason="invalid_claims",result="failure"} 1`,
		`auth_attempts_total{method="browser",reason="malformed",result="failure"} 2`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("scrape missing %q\n%s", want, body)
		}
	}
}

// TestExchangeFailureReason: an identity provider that refuses the code with a
// 4xx (an expired or reused code, a client it does not accept) answered, so
// the sign-in is code_rejected; no answer or a 5xx is idp_unavailable.
func TestExchangeFailureReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&tokenEndpointError{status: http.StatusBadRequest}, opsobs.AuthReasonCodeRejected},
		{fmt.Errorf("exchange: %w", &tokenEndpointError{status: http.StatusUnauthorized}), opsobs.AuthReasonCodeRejected},
		{&tokenEndpointError{status: http.StatusBadGateway}, opsobs.AuthReasonIDPUnavailable},
		{errors.New("token request: dial tcp: connection refused"), opsobs.AuthReasonIDPUnavailable},
	} {
		if got := exchangeFailureReason(tc.err); got != tc.want {
			t.Errorf("exchangeFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
