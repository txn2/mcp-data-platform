//go:build integration

package acceptance

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Acceptance for #2443: the connection test (and the state it records in
// mcp_platform_connections) reports a credential's health from an
// authenticated read, not from GET /.
//
// Neither Kubernetes nor Mattermost runs in the dev stack, so each is stood in
// for by a server in this process answering as the ticket reports them: a
// Kubernetes API refuses / to a limited service-account token and answers
// /version, and a Mattermost web app answers / with no credential at all and
// refuses /api/v4/users/me to an expired token. The platform reaches them over
// loopback, through the same client a call uses. The state gauge is summed
// over the replicas; each criterion talks to one replica, so only that one
// moves.
//
// Wire forms: the admin connection route takes a JSON object of config
// strings; api_invoke_endpoint takes connection, method, path and purpose as
// strings. Every call below sends those JSON types.

const token2443 = "acceptance-2443-token"

// standIn2443 answers path "ok" with 200 to the token and everything else
// with root.
func standIn2443(t *testing.T, ok string, root, refused int) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ok && r.Header.Get("Authorization") == "Bearer "+token2443:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == ok:
			w.WriteHeader(refused)
		default:
			w.WriteHeader(root)
		}
	})}}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

// register2443 saves a bearer api connection on c's replica.
func register2443(t *testing.T, c *client, base, token, probePath string) string {
	t.Helper()
	name := "acc-2443-" + unique1579()
	cfg := map[string]any{
		"base_url": base, "connection_name": name, "auth_mode": "bearer", "credential": token,
		"connect_timeout": "2s", "call_timeout": "5s", "trust_level": "untrusted",
	}
	if probePath != "" {
		cfg["probe_path"] = probePath
	}
	path := "/api/v1/admin/connection-instances/api/" + name
	if status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{"config": cfg, "description": "Acceptance #2443"})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	return name
}

// test2443 runs the admin connection test on c's replica.
func test2443(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	status, out := c.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+name+"/test", http.NoBody)
	if _, judged := out["ok"].(bool); !judged {
		t.Fatalf("the connection test answered %d with no verdict: %v", status, out)
	}
	return out
}

func authFailed2443(t *testing.T) float64 {
	return issue1898Count(t, "mcp_platform_connections", map[string]string{"kind": "api", "state": "auth_failed"})
}

func TestIssue2443_AKubernetesConnectionWithAValidLimitedTokenIsNotAuthFailed(t *testing.T) {
	r := replicas(t)[0]
	c := connectAt(t, r.base, devAPIKey())
	base := standIn2443(t, "/version", http.StatusForbidden, http.StatusUnauthorized)
	before := authFailed2443(t)

	name := register2443(t, c, base, token2443, "")
	if out := test2443(t, c, name); out["ok"] != true {
		t.Errorf("a 403 at / with no probe_path failed the test: %v", out)
	}
	if got := authFailed2443(t); got != before {
		t.Errorf("auth_failed moved %v -> %v for a token that works", before, got)
	}

	declared := register2443(t, c, base, token2443, "/version")
	if out := test2443(t, c, declared); out["ok"] != true {
		t.Errorf("GET /version with the token failed the test: %v", out)
	}
	if got := authFailed2443(t); got != before {
		t.Errorf("auth_failed moved %v -> %v with probe_path /version", before, got)
	}
}

func TestIssue2443_AnExpiredBearerTokenIsAuthFailedOnADeclaredProbe(t *testing.T) {
	r := replicas(t)[0]
	c := connectAt(t, r.base, devAPIKey())
	base := standIn2443(t, "/api/v4/users/me", http.StatusOK, http.StatusUnauthorized)
	before := authFailed2443(t)

	name := register2443(t, c, base, "expired-"+token2443, "/api/v4/users/me")
	out := test2443(t, c, name)
	if out["ok"] != false {
		t.Errorf("an expired token passed the test of its authenticated read: %v", out)
	}
	if got := authFailed2443(t); got != before+1 {
		t.Errorf("auth_failed = %v after one probe of a declared read, want %v", got, before+1)
	}
}

func TestIssue2443_AnExpiredBearerTokenIsAuthFailedAfterOneRealCallAndTheRootDoesNotHideIt(t *testing.T) {
	r := replicas(t)[0]
	c := connectAt(t, r.base, devAPIKey())
	base := standIn2443(t, "/api/v4/users/me", http.StatusOK, http.StatusUnauthorized)
	before := authFailed2443(t)

	name := register2443(t, c, base, "expired-"+token2443, "")
	if _, _, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": name, "method": "GET", "path": "/api/v4/users/me",
		"purpose": "Acceptance 2443: a real call with an expired token.",
	}); err != nil {
		t.Fatalf("api_invoke_endpoint: %v", err)
	}
	if got := authFailed2443(t); got != before+1 {
		t.Fatalf("auth_failed = %v after one refused call, want %v", got, before+1)
	}
	// The root answers 200 with no credential at all; probing it must not
	// read as the connection having recovered.
	if out := test2443(t, c, name); out["ok"] != true {
		t.Logf("connection test of the root: %v", out)
	}
	if got := authFailed2443(t); got != before+1 {
		t.Errorf("auth_failed = %v after a probe of an unauthenticated root, want the refused call's %v kept", got, before+1)
	}
}
