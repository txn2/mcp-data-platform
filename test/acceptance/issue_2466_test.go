//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #2466: oauth-start built an authorization-code connection's
// redirect_uri from the request's own host, so a request that reached the
// admin API on the platform's listen address (the platform-admin connection,
// a call straight to a replica) answered with a loopback callback no browser
// can be sent to, and the provider refused it. The callback is now built on
// the deployment's portal.public_base_url when it is set.
//
// The dev stack advertises http://localhost:<replica A's port> as its public
// base URL; each criterion reaches the admin API at another address and must
// get that one back.
//
// Wire forms: the oauth-start route takes an optional JSON object body, sent
// as an empty object; through api_invoke_endpoint the body admits an object
// and a string of JSON (#1548), and both are sent. The connection's config
// fields are strings.

// publicCallback2466 is the callback the dev stack's public base URL names.
func publicCallback2466() string {
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = defaultDevPort
	}
	return "http://localhost:" + port + "/api/v1/admin/oauth/callback"
}

// connection2466 saves an api connection on the authorization-code grant and
// returns its name.
func connection2466(t *testing.T, c *client) string {
	t.Helper()
	name := fmt.Sprintf("acc-2466-%d", time.Now().UnixNano())
	path := "/api/v1/admin/connection-instances/api/" + name
	t.Cleanup(func() { c.rest(http.MethodDelete, path, http.NoBody) })
	status, body := c.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url":                "https://upstream.example.com",
			"auth_mode":               "oauth",
			"oauth_grant":             "authorization_code",
			"oauth_authorization_url": "https://idp.example.com/oauth/auth",
			"oauth_token_url":         "https://idp.example.com/oauth/token",
			"oauth_client_id":         "acceptance-2466",
			"oauth_client_secret":     "acceptance-2466-secret",
			"oauth_scope":             "signature",
		},
		"description": "Acceptance #2466: an authorization-code connection.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d: %v", status, body)
	}
	return name
}

// assertPublic2466 checks an oauth-start answer names the public callback, as
// redirect_uri and inside the authorization URL the browser is sent to.
func assertPublic2466(t *testing.T, via string, out map[string]any) {
	t.Helper()
	want := publicCallback2466()
	if out["redirect_uri"] != want {
		t.Errorf("%s: redirect_uri = %v, want %s", via, out["redirect_uri"], want)
	}
	auth, _ := out["authorization_url"].(string)
	u, err := url.Parse(auth)
	if err != nil || u.Query().Get("redirect_uri") != want {
		t.Errorf("%s: the authorization URL carries redirect_uri %q, want %s", via, u.Query().Get("redirect_uri"), want)
	}
}

// TestIssue2466_OAuthStartNamesThePublicCallbackOnAReplicasAddress: the
// route called on 127.0.0.1 at replica A's port and through the stack's
// proxy answers the public callback both times.
func TestIssue2466_OAuthStartNamesThePublicCallbackOnAReplicasAddress(t *testing.T) {
	admin := connect(t)
	name := connection2466(t, admin)
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = defaultDevPort
	}
	for via, base := range map[string]string{"the replica's loopback address": "http://127.0.0.1:" + port, "the suite's address": baseURL()} {
		c := connectAtFor(t, base, devAPIKey(), 2*time.Minute)
		status, out := c.rest(http.MethodPost, "/api/v1/admin/connections/api/"+name+"/oauth-start", jsonBody(t, map[string]any{}))
		if status != http.StatusOK {
			t.Fatalf("%s: oauth-start answered %d: %v", via, status, out)
		}
		assertPublic2466(t, via, out)
	}
}

// TestIssue2466_OAuthStartThroughThePlatformAdminConnection is the report's
// path: the route called through api_invoke_endpoint on the built-in
// platform-admin connection, its body as an object and as a string, answers
// the public callback.
func TestIssue2466_OAuthStartThroughThePlatformAdminConnection(t *testing.T) {
	admin := connect(t)
	name := connection2466(t, admin)
	for form, body := range map[string]any{"object": map[string]any{}, "string": "{}"} {
		res, text, err := admin.callRaw("api_invoke_endpoint", map[string]any{
			"connection": "platform-admin", "method": http.MethodPost,
			"path":    "/api/v1/admin/connections/api/" + name + "/oauth-start",
			"body":    body,
			"purpose": "Acceptance #2466: start an authorization-code flow.",
		})
		if err != nil || res.IsError {
			t.Fatalf("body as %s: api_invoke_endpoint failed: %v %s", form, err, text)
		}
		var call map[string]any
		if err := json.Unmarshal([]byte(text), &call); err != nil {
			t.Fatalf("body as %s: the call answered no JSON: %s", form, text)
		}
		out, _ := call["body"].(map[string]any)
		if status, _ := call["status"].(float64); status != http.StatusOK || out == nil {
			t.Fatalf("body as %s: oauth-start answered %v: %s", form, call["status"], firstRunes(text, 600))
		}
		assertPublic2466(t, "platform-admin, body as "+form, out)
		if strings.Contains(fmt.Sprint(out["redirect_uri"]), "127.0.0.1") {
			t.Errorf("body as %s: the callback is still the loopback address", form)
		}
	}
}
