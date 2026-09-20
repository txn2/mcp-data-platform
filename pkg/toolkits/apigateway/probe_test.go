package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// probeToolkit serves one connection pointed at the given base URL.
func probeToolkit(t *testing.T, baseURL string) *Toolkit {
	t.Helper()
	tk := NewMulti(MultiConfig{})
	if err := tk.AddConnection("vendor", map[string]any{
		"base_url": baseURL, "connect_timeout": "5s", "call_timeout": "10s",
	}); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

// Any status the upstream answers with proves the route and the credential, so
// a 404 from its router is as good an answer as a 200: the probe reports the
// status rather than judging it (#1805).
func TestProbeConnection_UpstreamAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	res := probeToolkit(t, srv.URL).ProbeConnection(context.Background(), "vendor")

	if !res.OK {
		t.Fatalf("an answered request was reported as a failure: %+v", res)
	}
	if !strings.Contains(res.Detail, "HTTP 404") {
		t.Errorf("detail does not report the status: %q", res.Detail)
	}
}

// The two statuses that mean "your credential was not accepted" are failures,
// because a connection whose upstream rejects it is exactly what an operator is
// asking about, and calling that healthy is the silence this endpoint ends.
func TestProbeConnection_CredentialRejected(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		res := probeToolkit(t, srv.URL).ProbeConnection(context.Background(), "vendor")
		srv.Close()

		if res.OK {
			t.Errorf("HTTP %d was reported as healthy", status)
		}
		if !strings.Contains(res.Detail, "rejecting this connection's credential") {
			t.Errorf("detail does not name the credential: %q", res.Detail)
		}
	}
}

// An upstream that cannot be reached carries the transport's own error.
func TestProbeConnection_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tk := probeToolkit(t, srv.URL)
	srv.Close()

	res := tk.ProbeConnection(context.Background(), "vendor")

	if res.OK {
		t.Fatal("a closed upstream was reported as healthy")
	}
	if res.Error == "" {
		t.Error("the failure carries no error")
	}
}

// A name this toolkit does not serve is an answer about that name.
func TestProbeConnection_UnknownConnection(t *testing.T) {
	res := NewMulti(MultiConfig{}).ProbeConnection(context.Background(), "nope")

	if res.OK {
		t.Fatal("an unknown connection was reported as healthy")
	}
	if !strings.Contains(res.Detail, "not served") {
		t.Errorf("detail does not say the connection is unknown: %q", res.Detail)
	}
}

// The built-in connection resolved by an in-process handler has a synthetic
// base URL and no upstream, so dialing it would report the platform's own
// connection as unreachable.
func TestProbeConnection_InternalHandler(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetInternalHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := tk.AddConnection("util", map[string]any{
		"handler": HandlerInternal, "connect_timeout": "5s", "call_timeout": "10s",
	}); err != nil {
		t.Fatalf("adding the internal connection: %v", err)
	}

	res := tk.ProbeConnection(context.Background(), "util")

	if !res.OK {
		t.Fatalf("the platform's own connection was reported as broken: %+v", res)
	}
	if !strings.Contains(res.Detail, "no upstream to reach") {
		t.Errorf("detail does not say why nothing was dialed: %q", res.Detail)
	}
}
