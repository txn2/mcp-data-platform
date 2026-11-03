package apigateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/connstate"
)

// mattermost answers its root without a credential, as a web app's root does,
// and refuses the authenticated read with 401: an expired token.
func mattermost() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/users/me" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

// kubernetes refuses its root to a limited service-account token and answers
// /version.
func kubernetes() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
}

func stateToolkit(t *testing.T, name string, cfg map[string]any) *Toolkit {
	t.Helper()
	tk := NewMulti(MultiConfig{})
	cfg["connect_timeout"], cfg["call_timeout"] = "5s", "10s"
	if err := tk.AddConnection(name, cfg); err != nil {
		t.Fatalf("adding the connection: %v", err)
	}
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

func TestProbe_ADeclaredProbePathFindsAnExpiredToken(t *testing.T) {
	srv := mattermost()
	t.Cleanup(srv.Close)
	tk := stateToolkit(t, "mm-2443-declared", map[string]any{"base_url": srv.URL, "probe_path": "/api/v4/users/me"})
	res := tk.ProbeConnection(context.Background(), "mm-2443-declared")
	if res.OK || !strings.Contains(res.Detail, "GET /api/v4/users/me") {
		t.Fatalf("probe of the declared read = %+v, want a failure naming it", res)
	}
	if s, _ := connstate.State(Kind, "mm-2443-declared"); s != connstate.AuthFailed {
		t.Errorf("state = %q, want auth_failed", s)
	}
}

func TestProbe_AnUnauthenticatedRootDoesNotHideARefusedCall(t *testing.T) {
	srv := mattermost()
	t.Cleanup(srv.Close)
	tk := stateToolkit(t, "mm-2443-root", map[string]any{"base_url": srv.URL})
	// A real call is refused: the token expired.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v4/users/me", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := tk.lookup("mm-2443-root")
	resp, err := c.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if s, _ := connstate.State(Kind, "mm-2443-root"); s != connstate.AuthFailed {
		t.Fatalf("after the refused call state = %q, want auth_failed", s)
	}
	// The probe of the root answers 200, which says nothing of the token.
	if res := tk.ProbeConnection(context.Background(), "mm-2443-root"); !res.OK {
		t.Fatalf("probe = %+v", res)
	}
	if s, _ := connstate.State(Kind, "mm-2443-root"); s != connstate.AuthFailed {
		t.Errorf("after a probe of the root state = %q, want the refused call's auth_failed kept", s)
	}
}

func TestProbe_ALimitedTokenRefusedAtTheRootIsNotAuthFailed(t *testing.T) {
	srv := kubernetes()
	t.Cleanup(srv.Close)
	tk := stateToolkit(t, "k8s-2443-root", map[string]any{"base_url": srv.URL})
	if res := tk.ProbeConnection(context.Background(), "k8s-2443-root"); !res.OK {
		t.Fatalf("probe = %+v", res)
	}
	if s, seen := connstate.State(Kind, "k8s-2443-root"); seen {
		t.Errorf("a 403 at the base recorded %q; it says nothing about the credential", s)
	}
	tk = stateToolkit(t, "k8s-2443-version", map[string]any{"base_url": srv.URL, "probe_path": "/version"})
	if res := tk.ProbeConnection(context.Background(), "k8s-2443-version"); !res.OK {
		t.Fatalf("probe of /version = %+v", res)
	}
	if s, _ := connstate.State(Kind, "k8s-2443-version"); s != connstate.Healthy {
		t.Errorf("state = %q, want healthy", s)
	}
}

func TestProbe_AnUnreachableUpstreamIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	tk := stateToolkit(t, "gone-2443", map[string]any{"base_url": url})
	if res := tk.ProbeConnection(context.Background(), "gone-2443"); res.OK {
		t.Fatalf("probe of a closed server = %+v", res)
	}
	if s, _ := connstate.State(Kind, "gone-2443"); s != connstate.Unreachable {
		t.Errorf("state = %q, want unreachable", s)
	}
}

func TestProbePathValidation(t *testing.T) {
	for _, bad := range []string{"version", "/a#b", "/../etc"} {
		if _, err := ParseConfig(map[string]any{"base_url": "https://x.example", "probe_path": bad}); err == nil ||
			!strings.Contains(err.Error(), "probe_path") {
			t.Errorf("probe_path %q: err = %v, want a refusal naming probe_path", bad, err)
		}
	}
	c, err := ParseConfig(map[string]any{"base_url": "https://x.example", "probe_path": "/api/v4/users/me?fields=id"})
	if err != nil || c.ProbePath != "/api/v4/users/me?fields=id" {
		t.Errorf("ParseConfig = %+v, %v", c.ProbePath, err)
	}
}
