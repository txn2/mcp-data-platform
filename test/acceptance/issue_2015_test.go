//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Acceptance for #2015: auth_mode session_login signs in with a stored
// credential, carries the returned session token, signs in again on a 401,
// and writes captured sign-in values into a call's path.
//
// Tableau is not something the dev stack can run, so the upstream is a server
// in this test that answers Tableau's sign-in as Tableau documents it:
// POST /api/3.22/auth/signin with a personal access token in a JSON body,
// answered with credentials.token and credentials.site.id, X-Tableau-Auth on
// every call, 401 for a session it does not hold, POST /auth/signout to end
// one. The platform's replicas run on the host, so they reach it on loopback.
//
// Wire forms: api_invoke_endpoint's path is a string (sent with
// {session.site_id} as written and percent-encoded); its body admits an object
// and a string of JSON, and both are sent as literal tools/call params on a
// call the upstream rejects and the platform replays. The admin API takes one
// JSON object per route.

const (
	issue2015Purpose = "Acceptance for #2015: read Tableau-shaped data through a session sign-in."
	issue2015Secret  = `pat"secret&2015`
	issue2015SiteID  = "site-2015"
)

// issue2015Tableau is the upstream.
type issue2015Tableau struct {
	mu       sync.Mutex
	valid    map[string]bool
	signIns  int
	signOuts []string
	bodies   []string
}

func (u *issue2015Tableau) serve(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch r.URL.Path {
	case "/api/3.22/auth/signin":
		var in struct {
			Credentials struct {
				Secret string `json:"personalAccessTokenSecret"`
			} `json:"credentials"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Credentials.Secret != issue2015Secret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"401001","summary":"Signin Error","detail":"Error signing in to Tableau Server"}}`)
			return
		}
		u.signIns++
		token := fmt.Sprintf("tableau-token-%d", u.signIns)
		u.valid[token] = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"credentials":{"token":%q,"site":{"id":%q,"contentUrl":"acme"},"user":{"id":"u1"}}}`, token, issue2015SiteID)
	case "/api/3.22/auth/signout":
		u.signOuts = append(u.signOuts, r.Header.Get("X-Tableau-Auth"))
		delete(u.valid, r.Header.Get("X-Tableau-Auth"))
		w.WriteHeader(http.StatusNoContent)
	case "/api/3.22/sites/" + issue2015SiteID + "/workbooks":
		if !u.valid[r.Header.Get("X-Tableau-Auth")] {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"401002","summary":"Unauthorized Access","detail":"Invalid authentication credentials were provided"}}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		u.bodies = append(u.bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workbooks":{"workbook":[{"id":"wb-1","name":"Regional Sales"}]}}`)
	case "/api/metadata/graphql":
		if !u.valid[r.Header.Get("X-Tableau-Auth")] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":[{"message":"Introspection is disabled"}]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// revoke ends every session, as Tableau's own expiry would.
func (u *issue2015Tableau) revoke() {
	u.mu.Lock()
	u.valid = map[string]bool{}
	u.mu.Unlock()
}

func (u *issue2015Tableau) counts() (signIns int, signOuts []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.signIns, append([]string(nil), u.signOuts...)
}

func issue2015Upstream(t *testing.T) (*issue2015Tableau, string) {
	t.Helper()
	u := &issue2015Tableau{valid: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(srv.Close)
	return u, srv.URL
}

// issue2015Config is the Tableau connection as docs/server/api-gateway.md
// configures it.
func issue2015Config(base, secret string) map[string]any {
	return map[string]any{
		"base_url":             base,
		"auth_mode":            "session_login",
		"session_login_url":    "/api/3.22/auth/signin",
		"session_login_body":   `{"credentials":{"personalAccessTokenName":"platform","personalAccessTokenSecret":"{{secret}}","site":{"contentUrl":"acme"}}}`,
		"session_login_secret": secret,
		"session_token_source": "body:credentials.token",
		"session_token_header": "X-Tableau-Auth",
		"session_capture":      map[string]any{"site_id": "body:credentials.site.id"},
		"session_logout_url":   "/api/3.22/auth/signout",
		"connect_timeout":      "5s", "call_timeout": "20s", "trust_level": "untrusted",
	}
}

// issue2015Connection registers a connection of kind and removes it when the
// test ends.
func issue2015Connection(t *testing.T, c *client, kind string, cfg map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-2015-%d", time.Now().UnixNano())
	cfg["connection_name"] = name
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/"+kind+"/"+name, map[string]any{
		"config": cfg, "description": "Acceptance 2015: Tableau through a session sign-in. Paths take {session.site_id}.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register %s connection %s: HTTP %d", kind, name, status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody) })
	return name
}

// TestIssue2015_ACallSignsInAndCarriesTheSession is the ticket's core
// criterion: a call through the connection signs in, carries the token in
// X-Tableau-Auth, and has the captured site id written into its path, in both
// the forms a path can name it.
func TestIssue2015_ACallSignsInAndCarriesTheSession(t *testing.T) {
	c := connect(t)
	up, base := issue2015Upstream(t)
	conn := issue2015Connection(t, c, "api", issue2015Config(base, issue2015Secret))

	for _, path := range []string{
		"/api/3.22/sites/{session.site_id}/workbooks",
		"/api/3.22/sites/%7Bsession.site_id%7D/workbooks",
	} {
		out := c.call("api_invoke_endpoint", map[string]any{
			"connection": conn, "method": "GET", "path": path, "purpose": issue2015Purpose,
		})
		if status, _ := out["status"].(float64); status != http.StatusOK {
			t.Fatalf("GET %s answered %v: %v", path, out["status"], out)
		}
		if !strings.Contains(fmt.Sprint(out["body"]), "Regional Sales") {
			t.Errorf("GET %s: the workbook list is not in the result: %v", path, out["body"])
		}
	}
	// Each replica that served a call signs in for itself, once.
	if signIns, _ := up.counts(); signIns < 1 || signIns > 2 {
		t.Errorf("%d sign-ins for two calls, want one per replica that served them", signIns)
	}
}

// TestIssue2015_TheSecretIsNotReadBack holds the secret to the encrypted,
// write-only treatment every connection credential gets, and the body to the
// readable one the ticket asks for.
func TestIssue2015_TheSecretIsNotReadBack(t *testing.T) {
	c := connect(t)
	_, base := issue2015Upstream(t)
	conn := issue2015Connection(t, c, "api", issue2015Config(base, issue2015Secret))

	status, got := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("read connection: HTTP %d", status)
	}
	cfg, _ := got["config"].(map[string]any)
	if cfg["session_login_secret"] != "[REDACTED]" {
		t.Errorf("session_login_secret read back as %v, want [REDACTED]", cfg["session_login_secret"])
	}
	if !strings.Contains(fmt.Sprint(cfg["session_login_body"]), "{{secret}}") {
		t.Errorf("session_login_body did not read back as written: %v", cfg["session_login_body"])
	}
	if strings.Contains(fmt.Sprint(got), "pat\"secret") {
		t.Error("the connection read quotes the secret")
	}
}

// TestIssue2015_AnExpiredSessionIsSignedInAgainAndTheCallReplayed revokes
// every session, then sends a POST in both body forms: each is rejected 401,
// signed in again, and replayed with the same body, and the caller sees the
// replay's answer.
func TestIssue2015_AnExpiredSessionIsSignedInAgainAndTheCallReplayed(t *testing.T) {
	c := connect(t)
	up, base := issue2015Upstream(t)
	conn := issue2015Connection(t, c, "api", issue2015Config(base, issue2015Secret))
	path := "/api/3.22/sites/{session.site_id}/workbooks"

	forms := map[string]any{
		"object": map[string]any{"workbook": map[string]any{"name": "object-form"}},
		"string": `{"workbook":{"name":"string-form"}}`,
	}
	for form, body := range forms {
		up.revoke()
		before, _ := up.counts()
		out := c.call("api_invoke_endpoint", map[string]any{
			"connection": conn, "method": "POST", "path": path, "body": body, "purpose": issue2015Purpose,
		})
		if status, _ := out["status"].(float64); status != http.StatusOK {
			t.Fatalf("%s body: a call on a revoked session answered %v: %v", form, out["status"], out)
		}
		after, _ := up.counts()
		if after <= before {
			t.Errorf("%s body: the platform did not sign in again after the 401", form)
		}
		up.mu.Lock()
		last := up.bodies[len(up.bodies)-1]
		up.mu.Unlock()
		if !strings.Contains(last, form+"-form") {
			t.Errorf("%s body: the replay carried %q, not the call's body", form, last)
		}
	}
}

// TestIssue2015_AnUncapturedSessionValueIsRefused names a value the sign-in
// does not capture; the call is refused naming the one it does.
func TestIssue2015_AnUncapturedSessionValueIsRefused(t *testing.T) {
	c := connect(t)
	_, base := issue2015Upstream(t)
	conn := issue2015Connection(t, c, "api", issue2015Config(base, issue2015Secret))

	_, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/api/3.22/sites/{session.user_id}/workbooks", "purpose": issue2015Purpose,
	})
	if err != nil {
		t.Fatalf("api_invoke_endpoint: transport error: %v", err)
	}
	if !strings.Contains(text, "{session.site_id}") || !strings.Contains(text, "{session.user_id}") {
		t.Fatalf("the refusal does not name the missing value and the captured one: %s", text)
	}
	// The caller's path is what is wrong, so the refusal is not reported as
	// an upstream to try again.
	if strings.Contains(text, "upstream_unavailable") {
		t.Errorf("an uncaptured session value was reported as the upstream being unavailable: %s", text)
	}
}

// TestIssue2015_TheConnectionTestSignsIn is the save-time check the ticket
// asks of the test action: a real sign-in, on the api kind and on the graphql
// kind (Tableau's Metadata API), and a wrong secret reported as the sign-in
// failing with the upstream's words and without the secret.
func TestIssue2015_TheConnectionTestSignsIn(t *testing.T) {
	c := connect(t)
	up, base := issue2015Upstream(t)

	apiConn := issue2015Connection(t, c, "api", issue2015Config(base, issue2015Secret))
	status, res := c.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+apiConn+"/test", http.NoBody)
	if status != http.StatusOK || res["ok"] != true {
		t.Fatalf("the api connection test did not pass: HTTP %d %v", status, res)
	}

	gqlCfg := issue2015Config(base, issue2015Secret)
	delete(gqlCfg, "base_url")
	gqlCfg["endpoint_url"] = base + "/api/metadata/graphql"
	gqlCfg["session_login_url"] = base + "/api/3.22/auth/signin"
	gqlCfg["session_logout_url"] = base + "/api/3.22/auth/signout"
	gqlConn := issue2015Connection(t, c, "graphql", gqlCfg)
	before, _ := up.counts()
	status, res = c.rest(http.MethodPost, "/api/v1/admin/connection-instances/graphql/"+gqlConn+"/test", http.NoBody)
	if status != http.StatusOK || res["ok"] != true {
		t.Fatalf("the graphql connection test did not pass: HTTP %d %v", status, res)
	}
	if after, _ := up.counts(); after <= before {
		t.Error("the graphql connection test did not sign in")
	}

	bad := issue2015Connection(t, c, "api", issue2015Config(base, "wrong-secret"))
	status, res = c.rest(http.MethodPost, "/api/v1/admin/connection-instances/api/"+bad+"/test", http.NoBody)
	// The test route answers a connection that failed its test with 503.
	if status != http.StatusServiceUnavailable || res["ok"] != false {
		t.Fatalf("a wrong secret was not reported as a failure: HTTP %d %v", status, res)
	}
	msg := fmt.Sprint(res)
	if !strings.Contains(msg, "could not sign in") || !strings.Contains(msg, "Signin Error") {
		t.Errorf("the failure does not say the sign-in failed in the upstream's words: %v", res)
	}
	if strings.Contains(msg, "wrong-secret") {
		t.Error("the failure quotes the secret")
	}
}

// TestIssue2015_DeletingTheConnectionSignsOut holds the platform to ending
// the session it opened when the connection is removed.
func TestIssue2015_DeletingTheConnectionSignsOut(t *testing.T) {
	c := connect(t)
	up, base := issue2015Upstream(t)
	conn := issue2015Connection(t, c, "api", issue2015Config(base, issue2015Secret))
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/api/3.22/sites/{session.site_id}/workbooks", "purpose": issue2015Purpose,
	})
	if status, _ := out["status"].(float64); status != http.StatusOK {
		t.Fatalf("the call answered %v: %v", out["status"], out)
	}
	if status, _ := c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+conn, http.NoBody); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("delete: HTTP %d", status)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, outs := up.counts(); len(outs) > 0 {
			if !strings.HasPrefix(outs[0], "tableau-token-") {
				t.Errorf("the sign-out carried %q, not a session token", outs[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("deleting the connection did not sign its session out")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
