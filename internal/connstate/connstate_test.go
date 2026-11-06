package connstate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// reset drops every recorded state and restores the clock and maximum age,
// so a test starts from none.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	state = map[key]entry{}
	now = time.Now
	maxAge = DefaultMaxAge
}

// captureLogs sends slog's default logger to a JSON buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// records decodes the JSON log lines written so far.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func TestObserveAndState(t *testing.T) {
	reset()
	t.Cleanup(reset)

	if _, ok := State("api", "crm"); ok {
		t.Fatal("a connection no call used has a state")
	}
	Observe("api", "crm", Unreachable)
	Observe("api", "crm", Healthy)
	if s, ok := State("api", "crm"); !ok || s != Healthy {
		t.Errorf("State = %q, %v; want the last call's healthy", s, ok)
	}
	if _, ok := State("graphql", "crm"); ok {
		t.Error("a state leaked to another kind's connection of the same name")
	}

	Observe("", "crm", AuthFailed)
	Observe("api", "", AuthFailed)
	Observe("api", "crm", "")
	Observe("api", "crm", "broken")
	if s, _ := State("api", "crm"); s != Healthy {
		t.Errorf("an empty kind, connection or state, or one this package does not define, changed the record: %q", s)
	}
}

func TestFromHTTP(t *testing.T) {
	for _, tc := range []struct {
		status int
		err    error
		want   string
	}{
		{http.StatusOK, nil, Healthy},
		{http.StatusNotFound, nil, Healthy},
		{http.StatusForbidden, nil, Healthy},
		{http.StatusInternalServerError, nil, Healthy},
		{http.StatusUnauthorized, nil, AuthFailed},
		{http.StatusProxyAuthRequired, nil, AuthFailed},
		{http.StatusBadGateway, nil, Unreachable},
		{http.StatusServiceUnavailable, nil, Unreachable},
		{http.StatusGatewayTimeout, nil, Unreachable},
		{0, errors.New("dial tcp: connection refused"), Unreachable},
		{0, fmt.Errorf("round trip: %w", context.Canceled), ""},
		{0, nil, ""},
	} {
		if got := FromHTTP(tc.status, tc.err); got != tc.want {
			t.Errorf("FromHTTP(%d, %v) = %q, want %q", tc.status, tc.err, got, tc.want)
		}
	}
}

func TestFromResultText(t *testing.T) {
	codes := []string{"InvalidAccessKeyId", "SignatureDoesNotMatch"}
	for _, tc := range []struct {
		text string
		want string
	}{
		{"failed to list buckets: operation error S3: ListBuckets, api error InvalidAccessKeyId: The key does not exist", AuthFailed},
		{"api error SIGNATUREDOESNOTMATCH", AuthFailed},
		{"failed to list objects: dial tcp 10.0.0.1:9000: connect: connection refused", Unreachable},
		{"lookup minio: no such host", Unreachable},
		{"failed to get object: NoSuchKey: The specified key does not exist", Healthy},
	} {
		if got := FromResultText(tc.text, codes...); got != tc.want {
			t.Errorf("FromResultText(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestForget(t *testing.T) {
	reset()
	t.Cleanup(reset)
	Observe("api", "crm", Unreachable)
	Observe("api", "erp", AuthFailed)
	Forget("api", "crm")
	if _, ok := State("api", "crm"); ok {
		t.Error("a forgotten connection still has a state")
	}
	if s, _ := State("api", "erp"); s != AuthFailed {
		t.Errorf("forgetting one connection changed another: %q", s)
	}
	Forget("api", "never-seen")
}

// TestTransitionsAreLoggedNamingTheConnection is #2442's first criterion: a
// call that finds a connection's credential refused writes one WARN record
// naming it, with from_state, to_state and reason; its recovery writes one
// INFO record; a call that changes nothing writes nothing.
func TestTransitionsAreLoggedNamingTheConnection(t *testing.T) {
	reset()
	t.Cleanup(reset)
	buf := captureLogs(t)

	ObserveHTTP("api", "crm", http.StatusOK, nil)
	if got := records(t, buf); len(got) != 0 {
		t.Fatalf("a first healthy call logged %v", got)
	}
	ObserveHTTP("api", "crm", http.StatusUnauthorized, nil)
	ObserveHTTP("api", "crm", http.StatusUnauthorized, nil)
	ObserveHTTP("api", "crm", http.StatusOK, nil)
	Observe("trino", "warehouse", Unreachable)

	got := records(t, buf)
	if len(got) != 3 {
		t.Fatalf("records = %d, want 3: %v", len(got), got)
	}
	want := []map[string]any{
		{"level": "WARN", "connection": "crm", "kind": "api", "from_state": Healthy, "to_state": AuthFailed, "reason": "http_401"},
		{"level": "INFO", "connection": "crm", "kind": "api", "from_state": AuthFailed, "to_state": Healthy, "reason": "http_2xx"},
		{"level": "WARN", "connection": "warehouse", "kind": "trino", "from_state": "configured", "to_state": Unreachable, "reason": ReasonNetworkError},
	}
	for i, w := range want {
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("record %d %s = %v, want %v (%v)", i, k, got[i][k], v, got[i])
			}
		}
	}
}

// TestTransitionLogCarriesNoSecret is the threat model's criterion: an error
// whose text holds a URL with a password and a token reaches neither the log
// record nor the recorded reason, in any encoding.
func TestTransitionLogCarriesNoSecret(t *testing.T) {
	reset()
	t.Cleanup(reset)
	buf := captureLogs(t)
	const secret = "s3cr3t-2442-token"
	err := &url.Error{Op: "Get", URL: "https://svc:" + secret + "@upstream.example.com/v1?access_token=" + secret, Err: errors.New("dial tcp: connection refused " + secret)}
	ObserveHTTP("api", "crm", 0, err)
	o, ok := Observed("api", "crm")
	if !ok || o.State != Unreachable || o.Reason != ReasonTransportError {
		t.Fatalf("Observed = %+v, %v", o, ok)
	}
	out := buf.String() + o.Reason
	for _, form := range []string{
		secret, base64.StdEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)),
		url.QueryEscape(secret), strings.Trim(string(must(json.Marshal(secret))), `"`), "upstream.example.com",
	} {
		if strings.Contains(out, form) {
			t.Errorf("the log or reason carries %q:\n%s", form, out)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestStateAgesOut is #2442's added criterion: a connection that failed once
// and is not called again reads as configured after the maximum age, and the
// next failure after that is logged as a move from configured.
func TestStateAgesOut(t *testing.T) {
	reset()
	t.Cleanup(reset)
	buf := captureLogs(t)
	clock := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	mu.Lock()
	now = func() time.Time { return clock }
	mu.Unlock()
	SetMaxAge(10 * time.Minute)

	Observe("api", "crm", Unreachable)
	clock = clock.Add(10 * time.Minute)
	if s, ok := State("api", "crm"); !ok || s != Unreachable {
		t.Fatalf("at the maximum age State = %q, %v; want unreachable", s, ok)
	}
	if o, _ := Observed("api", "crm"); !o.At.Equal(clock.Add(-10 * time.Minute)) {
		t.Errorf("Observed.At = %v", o.At)
	}
	clock = clock.Add(time.Second)
	if _, ok := State("api", "crm"); ok {
		t.Fatal("a state older than the maximum age still stands")
	}
	Observe("api", "crm", Unreachable)
	got := records(t, buf)
	if len(got) != 2 || got[1]["from_state"] != "configured" {
		t.Errorf("a failure after the record aged out was not logged from configured: %v", got)
	}

	SetMaxAge(0)
	mu.RLock()
	defer mu.RUnlock()
	if maxAge != DefaultMaxAge {
		t.Errorf("SetMaxAge(0) = %v, want the default", maxAge)
	}
}

func TestHTTPReason(t *testing.T) {
	for _, tc := range []struct {
		status int
		err    error
		want   string
	}{
		{http.StatusOK, nil, "http_2xx"},
		{http.StatusNotFound, nil, "http_4xx"},
		{http.StatusInternalServerError, nil, "http_5xx"},
		{http.StatusUnauthorized, nil, "http_401"},
		{http.StatusProxyAuthRequired, nil, "http_407"},
		{http.StatusBadGateway, nil, "http_502"},
		{http.StatusServiceUnavailable, nil, "http_503"},
		{http.StatusGatewayTimeout, nil, "http_504"},
		{0, nil, ReasonAnswered},
		{999, nil, ReasonAnswered},
		{0, context.DeadlineExceeded, ReasonTimeout},
		{0, &url.Error{Op: "Get", URL: "x", Err: timeoutError{}}, ReasonTimeout},
		{0, errors.New("connection refused"), ReasonTransportError},
	} {
		if got := HTTPReason(tc.status, tc.err); got != tc.want {
			t.Errorf("HTTPReason(%d, %v) = %q, want %q", tc.status, tc.err, got, tc.want)
		}
	}
	reset()
	t.Cleanup(reset)
	ObserveStatus("api", "crm", AuthFailed, http.StatusForbidden)
	if o, _ := Observed("api", "crm"); o.Reason != "http_4xx" {
		t.Errorf("ObserveStatus reason = %q", o.Reason)
	}
	for s, want := range map[string]string{AuthFailed: ReasonCredentialRefused, Unreachable: ReasonNetworkError, Healthy: ReasonAnswered} {
		if got := stateReason(s); got != want {
			t.Errorf("stateReason(%q) = %q, want %q", s, got, want)
		}
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
