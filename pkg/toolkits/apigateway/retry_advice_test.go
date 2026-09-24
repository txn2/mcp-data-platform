package apigateway

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// invokeStatus calls an upstream that answers status with the given headers.
func invokeStatus(t *testing.T, method string, status int, header http.Header) InvokeOutput {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		maps.Copy(w.Header(), header)
		w.Header().Set("Content-Type", applicationJSON)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"slow down"}`))
	}))
	t.Cleanup(srv.Close)
	cfg := Config{
		BaseURL: srv.URL, AuthMode: AuthModeNone, ConnectTimeout: 2 * time.Second,
		CallTimeout: 5 * time.Second, MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, err := NewAuthenticator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := invoke(context.Background(), invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)},
		InvokeInput{Connection: "x", Method: method, Path: "/v1/things"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestInvoke_ReportsWhenToAskAgain holds #1859: a 429 and a 503 to a read
// carry the advice a script's host waits on; a 503 to a write and any other
// failure do not.
func TestInvoke_ReportsWhenToAskAgain(t *testing.T) {
	out := invokeStatus(t, http.MethodGet, http.StatusTooManyRequests, http.Header{"Retry-After": {"4"}})
	if !out.Retryable || out.RetryAfterSeconds != 4 {
		t.Errorf("a 429 with Retry-After 4 = %+v; want retryable after 4 seconds", out.Advice)
	}
	if out := invokeStatus(t, http.MethodGet, http.StatusServiceUnavailable, nil); !out.Retryable || out.RetryAfterSeconds != 0 {
		t.Errorf("a GET answered 503 = %+v; want retryable with no interval", out.Advice)
	}
	if out := invokeStatus(t, http.MethodPost, http.StatusServiceUnavailable, nil); out.Retryable {
		t.Error("a POST answered 503 is marked retryable; a write is not repeated")
	}
	if out := invokeStatus(t, http.MethodGet, http.StatusNotFound, nil); out.Retryable {
		t.Error("a 404 is marked retryable")
	}
}

// TestBudgetOrErrorResult_StampsATransportFailure: a transport failure from a
// walk or an export carries the outcome the error contract reads as an
// upstream that did not answer, and a timeout is told apart.
func TestBudgetOrErrorResult_StampsATransportFailure(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"refused": {&transportError{msg: "dial tcp: connection refused"}, "transport_err"},
		"timeout": {&transportError{msg: "context deadline exceeded"}, "upstream_timeout"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := budgetOrErrorResult(tc.err)
			if !res.IsError {
				t.Fatal("a transport failure is not an error result")
			}
			if got := res.Meta["audit_outcome"]; got != tc.want {
				t.Errorf("audit_outcome = %v; want %s", got, tc.want)
			}
		})
	}
	plain := budgetOrErrorResult(notTransportError{})
	if plain.Meta != nil {
		t.Errorf("an ordinary failure was stamped: %v", plain.Meta)
	}
}

type notTransportError struct{}

func (notTransportError) Error() string { return "name is required" }
