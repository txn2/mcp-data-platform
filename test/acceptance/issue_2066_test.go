//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// #2066: a connection's own configuration names a stored secret as
// {{secret:<name>}}. Saving stores it as written, from the admin API the
// portal saves through and from the platform-admin connection an agent saves
// through, and the connection reads the secret each time it signs in.
//
// The upstream is #2015's Tableau stand-in, which accepts one personal access
// token at /api/3.22/auth/signin; the secret holds that token and is allowed
// only on the connection that names it.
//
// Wire forms: the admin API takes one JSON object per route. Through
// platform-admin, api_invoke_endpoint's body admits an object and a string of
// JSON; the connection is saved once in each form, as literal params, and
// read back unchanged both times. The secret's admin route is one JSON
// object, its value a string.

// issue2066Body is the issue's sign-in body, naming the secret.
func issue2066Body(secret string) string {
	return `{"credentials":{"personalAccessTokenName":"plexara-rest","personalAccessTokenSecret":"{{secret:` + secret + `}}","site":{"contentUrl":"acme"}}}`
}

// issue2066Config is the Tableau connection with its token named as a stored
// secret and no session_login_secret.
func issue2066Config(base, conn, secret string) map[string]any {
	cfg := issue2015Config(base, "")
	delete(cfg, "session_login_secret")
	cfg["session_login_body"] = issue2066Body(secret)
	cfg["connection_name"] = conn
	return cfg
}

// issue2066Secret stores value as a secret allowed only on connection and
// removes it after the test.
func issue2066Secret(t *testing.T, c *client, connection, value string) string {
	t.Helper()
	name := "acc2066-" + strings.ToLower(unique1579())
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+name, map[string]any{
		"description": "Acceptance #2066: a Tableau personal access token.", "value": value,
		"allow_connections": []string{connection}, "allow_personas": []string{},
	}); status != http.StatusCreated {
		t.Fatalf("creating the secret answered %d", status)
	}
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/secrets/"+name, http.NoBody) })
	return name
}

// issue2066Name is a fresh connection name, removed after the test.
func issue2066Name(t *testing.T, c *client, kind string) string {
	t.Helper()
	name := fmt.Sprintf("acc-2066-%d", time.Now().UnixNano())
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/"+kind+"/"+name, http.NoBody) })
	return name
}

// readBack2066 is the stored session_login_body, as the admin API returns it.
func readBack2066(t *testing.T, c *client, kind, conn string) string {
	t.Helper()
	status, got := c.rest(http.MethodGet, "/api/v1/admin/connection-instances/"+kind+"/"+conn, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading %s/%s answered %d", kind, conn, status)
	}
	cfg, _ := got["config"].(map[string]any)
	body, _ := cfg["session_login_body"].(string)
	return body
}

// signsIn2066 makes a call through the connection and holds it to the
// Tableau stand-in's answer, which only a signed-in session gets.
func signsIn2066(t *testing.T, c *client, conn string) {
	t.Helper()
	out := c.call("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/api/3.22/sites/{session.site_id}/workbooks",
		"purpose": "Acceptance #2066: a connection signs in with a stored secret.",
	})
	if status, _ := out["status"].(float64); status != http.StatusOK || !strings.Contains(fmt.Sprint(out["body"]), "Regional Sales") {
		t.Fatalf("the call through %s did not sign in: %v", conn, out)
	}
}

func TestIssue2066_TheBodySavesAsWrittenFromThePortalAndSignsIn(t *testing.T) {
	c := connect(t)
	_, base := issue2015Upstream(t)
	conn := issue2066Name(t, c, "api")
	secret := issue2066Secret(t, c, conn, issue2015Secret)

	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+conn, map[string]any{
		"config": issue2066Config(base, conn, secret), "description": "Acceptance #2066: Tableau with a stored token. Paths take {session.site_id}.",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d", status)
	}
	if got := readBack2066(t, c, "api", conn); got != issue2066Body(secret) {
		t.Fatalf("the body read back as %q, not as written", got)
	}
	signsIn2066(t, c, conn)
}

func TestIssue2066_TheBodySavesAsWrittenThroughPlatformAdmin(t *testing.T) {
	c := connect(t)
	_, base := issue2015Upstream(t)
	for _, form := range []string{"object", "string of JSON"} {
		t.Run(form, func(t *testing.T) {
			conn := issue2066Name(t, c, "api")
			secret := issue2066Secret(t, c, conn, issue2015Secret)
			payload := map[string]any{
				"config": issue2066Config(base, conn, secret), "description": "Acceptance #2066: saved through platform-admin. Paths take {session.site_id}.",
			}
			var body any = payload
			if form == "string of JSON" {
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				body = string(raw)
			}
			out := c.call("api_invoke_endpoint", map[string]any{
				"connection": "platform-admin", "method": http.MethodPut,
				"path": "/api/v1/admin/connection-instances/api/" + conn, "body": body,
				"headers": map[string]string{"Content-Type": "application/json"},
				"purpose": "Acceptance #2066: an agent saves a connection that names a stored secret.",
			})
			if status, _ := out["status"].(float64); status != http.StatusOK && status != http.StatusCreated {
				t.Fatalf("saving through platform-admin answered %v: %v", out["status"], out)
			}
			if got := readBack2066(t, c, "api", conn); got != issue2066Body(secret) {
				t.Fatalf("the body read back as %q, not as written: the placeholder was filled before the admin API saw it", got)
			}
			signsIn2066(t, c, conn)
		})
	}
}

func TestIssue2066_ASecretNotAllowedOnTheConnectionIsRefusedAtSave(t *testing.T) {
	c := connect(t)
	_, base := issue2015Upstream(t)
	allowed := issue2066Name(t, c, "api")
	secret := issue2066Secret(t, c, allowed, issue2015Secret)
	other := issue2066Name(t, c, "api")

	status, body := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+other,
		strings.NewReader(mustJSON2066(t, map[string]any{"config": issue2066Config(base, other, secret)})))
	if status != http.StatusBadRequest {
		t.Fatalf("saving a connection the secret is not allowed on answered %d: %v", status, body)
	}
	detail := fmt.Sprint(body["detail"])
	if !strings.Contains(detail, `secret "`+secret+`"`) || !strings.Contains(detail, `connection "`+other+`"`) {
		t.Errorf("the refusal does not name the secret and the connection: %s", detail)
	}
}

func TestIssue2066_RotatingTheSecretChangesTheNextSignIn(t *testing.T) {
	c := connect(t)
	up, base := issue2015Upstream(t)
	conn := issue2066Name(t, c, "api")
	secret := issue2066Secret(t, c, conn, issue2015Secret)
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+conn, map[string]any{
		"config": issue2066Config(base, conn, secret), "description": "Acceptance #2066: rotation. Paths take {session.site_id}.",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d", status)
	}
	signsIn2066(t, c, conn)

	// A wrong token in the store: the next sign-in sends it, and fails.
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secret, map[string]any{
		"description": "rotated wrong", "value": "not-the-token", "allow_connections": []string{conn},
	}); status != http.StatusOK {
		t.Fatalf("rotating the secret answered %d", status)
	}
	up.revoke()
	_, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
		"connection": conn, "method": "GET", "path": "/api/3.22/sites/{session.site_id}/workbooks",
		"purpose": "Acceptance #2066: the next sign-in reads the rotated secret.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "sign") || strings.Contains(text, "not-the-token") {
		t.Fatalf("the sign-in with the rotated wrong token did not fail as a sign-in, or quoted it: %s", text)
	}

	// Rotated back with no connection save: the next sign-in succeeds. A
	// failed sign-in is held for failureHold before the platform tries again.
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/secrets/"+secret, map[string]any{
		"description": "rotated back", "value": issue2015Secret, "allow_connections": []string{conn},
	}); status != http.StatusOK {
		t.Fatalf("rotating the secret back answered %d", status)
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		out := c.call("api_invoke_endpoint", map[string]any{
			"connection": conn, "method": "GET", "path": "/api/3.22/sites/{session.site_id}/workbooks",
			"purpose": "Acceptance #2066: the next sign-in reads the rotated secret.",
		})
		if status, _ := out["status"].(float64); status == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the rotated secret did not sign in: %v", out)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestIssue2066_AGraphQLConnectionSignsInWithAStoredSecret(t *testing.T) {
	c := connect(t)
	up, base := issue2015Upstream(t)
	conn := issue2066Name(t, c, "graphql")
	secret := issue2066Secret(t, c, conn, issue2015Secret)
	cfg := issue2066Config(base, conn, secret)
	delete(cfg, "base_url")
	cfg["endpoint_url"] = base + "/api/metadata/graphql"
	cfg["session_login_url"] = base + "/api/3.22/auth/signin"
	cfg["session_logout_url"] = base + "/api/3.22/auth/signout"
	if status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/graphql/"+conn, map[string]any{
		"config": cfg, "description": "Acceptance #2066: Tableau's Metadata API with a stored token.",
	}); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the graphql connection answered %d", status)
	}
	if got := readBack2066(t, c, "graphql", conn); got != issue2066Body(secret) {
		t.Fatalf("the graphql body read back as %q, not as written", got)
	}
	before, _ := up.counts()
	status, res := c.rest(http.MethodPost, "/api/v1/admin/connection-instances/graphql/"+conn+"/test", http.NoBody)
	if status != http.StatusOK || res["ok"] != true {
		t.Fatalf("the graphql connection test did not sign in: HTTP %d %v", status, res)
	}
	if after, _ := up.counts(); after <= before {
		t.Error("the graphql connection test did not sign in")
	}
}

func mustJSON2066(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
