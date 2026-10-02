package apigateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// sessionUpstream signs in at /signin and accepts its token in X-Auth; it
// records the tokens it is asked to sign out.
type sessionUpstream struct {
	mu       sync.Mutex
	refuse   bool
	signOuts []string
}

func (u *sessionUpstream) serve(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch r.URL.Path {
	case "/signin":
		raw, _ := io.ReadAll(r.Body)
		if u.refuse || !strings.Contains(string(raw), "pat-secret") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"credentials":{"token":"tok"}}`)
	case "/signout":
		u.signOuts = append(u.signOuts, r.Header.Get("X-Auth"))
	default:
		if r.Header.Get("X-Auth") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func sessionToolkit(t *testing.T, u *sessionUpstream) *Toolkit {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(srv.Close)
	tk := NewMulti(MultiConfig{})
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	tk.SetMetrics(m)
	if err := tk.AddConnection("vendor", map[string]any{
		"base_url":             srv.URL,
		"auth_mode":            "session_login",
		"session_login_url":    "/signin",
		"session_login_body":   `{"secret":"{{secret}}"}`,
		"session_login_secret": "pat-secret",
		"session_token_source": "body:credentials.token",
		"session_token_header": "X-Auth",
		"session_logout_url":   "/signout",
	}); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}
	return tk
}

// The connection test signs in for real: a probe that reached the upstream
// with the session token got past its credential check (#2015).
func TestProbeConnection_SessionLoginSignsIn(t *testing.T) {
	tk := sessionToolkit(t, &sessionUpstream{})
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "vendor")
	if !res.OK || !strings.Contains(res.Detail, "HTTP 404") {
		t.Fatalf("a signed-in probe was not reported as answered: %+v", res)
	}
}

// A refused sign-in is reported as the sign-in failing, not as the upstream
// being unreachable.
func TestProbeConnection_SessionLoginRefused(t *testing.T) {
	tk := sessionToolkit(t, &sessionUpstream{refuse: true})
	t.Cleanup(func() { _ = tk.Close() })

	res := tk.ProbeConnection(context.Background(), "vendor")
	if res.OK || !strings.Contains(res.Detail, "could not sign in") || !strings.Contains(res.Error, "HTTP 401") {
		t.Fatalf("a refused sign-in was not reported as one: %+v", res)
	}
}

// Removing a connection ends its session at the upstream, through the metrics
// wrapper the toolkit puts on every client.
func TestRemoveConnection_SignsOutTheSession(t *testing.T) {
	u := &sessionUpstream{}
	tk := sessionToolkit(t, u)
	if res := tk.ProbeConnection(context.Background(), "vendor"); !res.OK {
		t.Fatalf("probe: %+v", res)
	}
	if err := tk.RemoveConnection("vendor"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// The sign-out is sent in the background, so the toolkit's lock is not
	// held across a call to the upstream.
	deadline := time.Now().Add(5 * time.Second)
	for {
		u.mu.Lock()
		outs := append([]string(nil), u.signOuts...)
		u.mu.Unlock()
		if len(outs) == 1 && outs[0] == "tok" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sign-outs = %v, want the one session ended", outs)
		}
		runtime.Gosched()
	}
}
