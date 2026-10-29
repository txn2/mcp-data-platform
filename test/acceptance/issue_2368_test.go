//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #2368: the api connection test probed GET / and read a scoped key's
// 403 there as a rejected credential, reporting a working connection as
// broken. The test now asks for a read operation the connection's catalog
// declares when there is one, reads a 403 at an undeclared "/" as the
// credential not reaching "/", and reports a session_login connection whose
// sign-in returned a session as a working credential.
//
// The vendor in the ticket is not something the dev stack runs, so the
// upstream is a server in this test that answers as that vendor did: a
// sign-in that returns a token, 403 {"message":"Insufficient permissions"}
// at "/" for a valid session or key, 200 on the operation the key is scoped
// to, and 401 for a credential it does not hold. The platform's replicas run
// on the host, so they reach it on loopback.
//
// Wire forms: the admin API takes one JSON object per route; the test route
// takes no body.

const (
	issue2368Secret = "scoped-secret-2368"
	issue2368Key    = "scoped-key-2368"
	issue2368Token  = "session-token-2368"
	issue2368Read   = "/cameras/v1/devices"
)

// issue2368Vendor is the upstream. It records the paths it is asked for.
type issue2368Vendor struct {
	mu    sync.Mutex
	asked []string
}

func (v *issue2368Vendor) serve(w http.ResponseWriter, r *http.Request) {
	v.mu.Lock()
	v.asked = append(v.asked, r.Method+" "+r.URL.Path)
	v.mu.Unlock()
	if r.URL.Path == "/signin" {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), issue2368Secret) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"token":%q}`, issue2368Token)
		return
	}
	if r.Header.Get("X-Session") != issue2368Token && r.Header.Get("X-API-Key") != issue2368Key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/":
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"Insufficient permissions"}`)
	case issue2368Read:
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":[]}`)
	default:
		w.WriteHeader(http.StatusForbidden)
	}
}

func (v *issue2368Vendor) askedFor(path string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, a := range v.asked {
		if a == "GET "+path {
			return true
		}
	}
	return false
}

func issue2368Upstream(t *testing.T) (*issue2368Vendor, string) {
	t.Helper()
	v := &issue2368Vendor{}
	srv := httptest.NewServer(http.HandlerFunc(v.serve))
	t.Cleanup(srv.Close)
	return v, srv.URL
}

// issue2368Spec declares the one operation the key is scoped to.
const issue2368Spec = `openapi: 3.0.0
info: {title: cameras, version: "1"}
paths:
  /cameras/v1/devices:
    get: {operationId: listDevices, responses: {"200": {description: ok}}}
`

// issue2368Connection registers an api connection over cfg, with a catalog
// holding issue2368Spec when catalog is set, and returns its name.
func issue2368Connection(t *testing.T, c *client, cfg map[string]any, catalog bool) string {
	t.Helper()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "acc-2368-" + stamp
	if catalog {
		catalogID := "acc-2368-" + stamp
		if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
			"id": catalogID, "name": catalogID, "display_name": "Acceptance 2368", "description": "The scoped key's one operation.",
		}); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("create catalog: HTTP %d", status)
		}
		t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
		if status := c.restJSON(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/cameras", map[string]any{
			"source_kind": "inline", "content": issue2368Spec,
		}); status != http.StatusCreated && status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("upsert spec: HTTP %d", status)
		}
		cfg["catalog_id"] = catalogID
	}
	cfg["connection_name"] = name
	cfg["connect_timeout"], cfg["call_timeout"], cfg["trust_level"] = "5s", "20s", "untrusted"
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": cfg, "description": "Acceptance 2368: a key scoped to one operation.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection %s: HTTP %d", name, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
	return name
}

func issue2368SessionConfig(base, secret string) map[string]any {
	return map[string]any{
		"base_url": base, "auth_mode": "session_login", "session_login_url": "/signin",
		"session_login_body": `{"secret":"{{secret}}"}`, "session_login_secret": secret,
		"session_token_source": "body:token", "session_token_header": "X-Session",
	}
}

func issue2368KeyConfig(base, key string) map[string]any {
	return map[string]any{
		"base_url": base, "auth_mode": "api_key", "credential": key,
		"api_key_placement": "header", "api_key_header": "X-API-Key",
	}
}

func issue2368Test(t *testing.T, c *client, name string) (int, map[string]any) {
	t.Helper()
	return c.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+name+"/test", http.NoBody)
}

// issue2368AssertNoSecret holds the threat model's sinks: the test's answer
// carries neither the session token, the sign-in secret nor the key, as
// written, base64, hex, URL-escaped or JSON-escaped.
func issue2368AssertNoSecret(t *testing.T, res map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(res)
	answer := string(raw) + fmt.Sprint(res)
	for _, secret := range []string{issue2368Token, issue2368Secret, issue2368Key} {
		quoted, _ := json.Marshal(secret)
		for _, form := range []string{
			secret, base64.StdEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)),
			url.QueryEscape(secret), strings.Trim(string(quoted), `"`),
		} {
			if strings.Contains(answer, form) {
				t.Errorf("the connection test's answer carries %q (as %q): %s", secret, form, answer)
			}
		}
	}
}

// The ticket's case: a session_login connection whose key is scoped away from
// "/", with no catalog. The sign-in returned a session, so the test passes and
// says the credential works, and it names "/" as what the credential cannot
// reach rather than the credential as rejected.
func TestIssue2368_ASignInThatReturnsASessionIsAWorkingCredential(t *testing.T) {
	c := connect(t)
	_, base := issue2368Upstream(t)
	name := issue2368Connection(t, c, issue2368SessionConfig(base, issue2368Secret), false)

	status, res := issue2368Test(t, c, name)
	detail, _ := res["detail"].(string)
	if status != http.StatusOK || res["ok"] != true {
		t.Fatalf("the connection test reported a working connection as broken: HTTP %d %v", status, res)
	}
	if !strings.Contains(detail, "the sign-in returned a session, so the credential works") || !strings.Contains(detail, "cannot reach /") {
		t.Errorf("detail = %q, want the sign-in named as the credential check and / as out of reach", detail)
	}
	if strings.Contains(detail, "rejecting") {
		t.Errorf("detail still names the credential as rejected: %q", detail)
	}
	issue2368AssertNoSecret(t, res)

	bad := issue2368Connection(t, c, issue2368SessionConfig(base, "wrong-2368"), false)
	status, res = issue2368Test(t, c, bad)
	if status != http.StatusServiceUnavailable || !strings.Contains(fmt.Sprint(res), "could not sign in") {
		t.Errorf("a refused sign-in: HTTP %d %v, want a failure saying it could not sign in", status, res)
	}
	issue2368AssertNoSecret(t, res)
}

// With a catalog, the test asks for the read operation it declares, the route
// the key is meant to reach, and passes on its 200.
func TestIssue2368_ACatalogedReadIsWhatTheTestAsksFor(t *testing.T) {
	c := connect(t)
	vendor, base := issue2368Upstream(t)
	name := issue2368Connection(t, c, issue2368KeyConfig(base, issue2368Key), true)

	status, res := issue2368Test(t, c, name)
	if status != http.StatusOK || res["ok"] != true || !strings.Contains(fmt.Sprint(res["detail"]), "GET "+issue2368Read+" with HTTP 200") {
		t.Fatalf("the cataloged probe: HTTP %d %v", status, res)
	}
	if !vendor.askedFor(issue2368Read) || vendor.askedFor("/") {
		t.Errorf("the vendor was asked for %v; want the declared read and not /", vendor.asked)
	}
	issue2368AssertNoSecret(t, res)
}

// A scoped key with no catalog: a 403 at "/" is the key not reaching "/",
// and a key the upstream does not hold (401) still fails as rejected.
func TestIssue2368_AForbiddenBaseIsNotARejectedCredential(t *testing.T) {
	c := connect(t)
	_, base := issue2368Upstream(t)

	scoped := issue2368Connection(t, c, issue2368KeyConfig(base, issue2368Key), false)
	status, res := issue2368Test(t, c, scoped)
	if status != http.StatusOK || res["ok"] != true || !strings.Contains(fmt.Sprint(res["detail"]), "the credential cannot reach /") {
		t.Errorf("a scoped key at /: HTTP %d %v, want it passed and / named as out of reach", status, res)
	}

	revoked := issue2368Connection(t, c, issue2368KeyConfig(base, "revoked-2368"), false)
	status, res = issue2368Test(t, c, revoked)
	if status != http.StatusServiceUnavailable || !strings.Contains(fmt.Sprint(res["detail"]), "rejecting this connection's credential") {
		t.Errorf("a revoked key: HTTP %d %v, want the credential reported rejected", status, res)
	}
	issue2368AssertNoSecret(t, res)
}
