package apigateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/timeoutcap"
)

// TestInvoke_ACappedTimeoutIsReportedAndNamedWhenItFires is #2210 on
// api_invoke_endpoint: a timeout_seconds above the connection's
// call_timeout was replaced by it silently, since the connection's client
// enforces it. The result now reports the cap, and a request stopped by it
// says so.
func TestInvoke_ACappedTimeoutIsReportedAndNamedWhenItFires(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	defer close(release)

	cfg := Config{BaseURL: srv.URL, AuthMode: AuthModeNone, ConnectTimeout: time.Second, CallTimeout: time.Second, MaxResponseBytes: DefaultMaxResponseBytes}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	inv := invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}

	out, err := invoke(context.Background(), inv, InvokeInput{Connection: "c", Method: "GET", Path: "/fast", TimeoutSeconds: 240})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	want := timeoutcap.Cap{AppliedSeconds: 1, CappedBy: timeoutcap.CappedByCallTimeout, RequestedSeconds: 240}
	if out.Cap != want {
		t.Errorf("a successful call reports the cap: got %+v, want %+v", out.Cap, want)
	}

	out, err = invoke(context.Background(), inv, InvokeInput{Connection: "c", Method: "GET", Path: "/slow", TimeoutSeconds: 240})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.HasPrefix(out.Error, "timed out after 1s (the connection's call_timeout; timeout_seconds 240 was capped): ") {
		t.Errorf("error = %q", out.Error)
	}
	if out.Cap != want {
		t.Errorf("a failed call reports the cap too: got %+v", out.Cap)
	}

	out, err = invoke(context.Background(), inv, InvokeInput{Connection: "c", Method: "GET", Path: "/fast", TimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out.Capped() {
		t.Errorf("a timeout within the connection's is not capped: %+v", out.Cap)
	}
}
