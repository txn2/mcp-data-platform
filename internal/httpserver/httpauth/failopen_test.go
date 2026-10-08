package httpauth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// TestGate_FailOpenIsCountedAndLogged shows the fail-open half of the gate is
// no longer silent: auth_fail_open_total counts it and a warning names it
// (#1898).
func TestGate_FailOpenIsCountedAndLogged(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	prev := opsobs.Metrics()
	opsobs.SetMetrics(m)
	t.Cleanup(func() { opsobs.SetMetrics(prev) })
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	handler := MCPAuthGateway(stubAuthenticator{err: fmt.Errorf("jwks: %w", middleware.ErrValidationUnavailable)}, "")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer some.jwt.token")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "auth_fail_open_total 1") {
		t.Error("fail-open not counted")
	}
	if !strings.Contains(logs.String(), "identity provider unavailable") {
		t.Errorf("fail-open not logged: %s", logs.String())
	}
}
