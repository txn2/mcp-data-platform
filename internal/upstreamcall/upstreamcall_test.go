package upstreamcall

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fixedAuth applies a credential the way a connection's authenticator does,
// and fails on demand.
type fixedAuth struct {
	token string
	err   error
}

func (a fixedAuth) Apply(req *http.Request) error {
	if a.err != nil {
		return a.err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	return nil
}

// echoServer answers every request, recording what it received.
func echoServer(t *testing.T, delay time.Duration) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestDo_AppliesTheCredentialAndTheOperatorsHeaders(t *testing.T) {
	srv, got := echoServer(t, 0)
	u := New(Config{
		Name: "slack-bot", BaseURL: srv.URL, Client: srv.Client(),
		Auth:          fixedAuth{token: "xoxb-secret"},
		StaticHeaders: map[string]string{"X-Quota-Project": "acme"},
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u.BaseURL()+"/chat.postMessage", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := u.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if auth := got.Get("Authorization"); auth != "Bearer xoxb-secret" {
		t.Errorf("Authorization = %q; the connection's credential must reach the upstream", auth)
	}
	if quota := got.Get("X-Quota-Project"); quota != "acme" {
		t.Errorf("X-Quota-Project = %q, want the operator's static header", quota)
	}
}

func TestDo_ReportsWhichConnectionFailedToAuthenticate(t *testing.T) {
	srv, _ := echoServer(t, 0)
	u := New(Config{
		Name: "slack-bot", BaseURL: srv.URL, Client: srv.Client(),
		Auth: fixedAuth{err: errors.New("token endpoint unreachable")},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody) //nolint:errcheck // fixed inputs
	resp, err := u.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "slack-bot") {
		t.Errorf("err = %v; a failure must name the connection that could not authenticate", err)
	}
}

func TestDo_BoundsARequestCarryingNoDeadline(t *testing.T) {
	// A queued send must not hang a worker on an upstream that accepts a
	// connection and never answers.
	srv, _ := echoServer(t, time.Second)
	u := New(Config{
		Name: "slow", BaseURL: srv.URL, Client: srv.Client(),
		Auth: fixedAuth{token: "t"}, CallTimeout: 50 * time.Millisecond,
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody) //nolint:errcheck // fixed inputs

	start := time.Now()
	resp, err := u.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("Do waited out an upstream that never answered")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Do took %v; the connection's call timeout did not bound it", elapsed)
	}
}

func TestDo_KeepsTheBodyReadableAfterItReturns(t *testing.T) {
	// The timeout context is released when the body is closed, not when Do
	// returns: the caller reads the body after this frame is gone, and
	// canceling early would close it under them.
	srv, _ := echoServer(t, 0)
	u := New(Config{
		Name: "ok", BaseURL: srv.URL, Client: srv.Client(),
		Auth: fixedAuth{token: "t"}, CallTimeout: time.Minute,
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody) //nolint:errcheck // fixed inputs
	resp, err := u.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body after Do returned: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestDo_LeavesACallersOwnDeadlineAlone(t *testing.T) {
	srv, _ := echoServer(t, 0)
	u := New(Config{
		Name: "ok", BaseURL: srv.URL, Client: srv.Client(),
		Auth: fixedAuth{token: "t"}, CallTimeout: time.Minute,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody) //nolint:errcheck // fixed inputs
	resp, err := u.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, ok := resp.Body.(cancelOnClose); ok {
		t.Error("Do wrapped a body whose request already carried a deadline")
	}
}

func TestDo_ReportsAnUnreachableUpstream(t *testing.T) {
	u := New(Config{
		Name: "gone", BaseURL: "http://127.0.0.1:1", Client: http.DefaultClient,
		Auth: fixedAuth{token: "t"}, CallTimeout: time.Second,
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1", http.NoBody) //nolint:errcheck // fixed inputs
	resp, err := u.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("err = %v; an unreachable upstream must name its connection", err)
	}
}

func TestAccessors(t *testing.T) {
	u := New(Config{Name: "c1", BaseURL: "https://slack.com/api"})
	if u.Connection() != "c1" {
		t.Errorf("Connection() = %q, want c1", u.Connection())
	}
	if u.BaseURL() != "https://slack.com/api" {
		t.Errorf("BaseURL() = %q", u.BaseURL())
	}
}
